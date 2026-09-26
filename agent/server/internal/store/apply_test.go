package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jyothri/bhandaar/agent/server/internal/store"
	"github.com/jyothri/bhandaar/agent/server/internal/testdb"
	"github.com/jyothri/bhandaar/agent/wire"
)

var bg = context.Background()

type applyFixture struct {
	st      *store.Store
	user    int64
	agent   string
	drive   string
	stream  string
	counter int
}

func newApply(t *testing.T) *applyFixture {
	t.Helper()
	st := testdb.New(t)
	uid, err := st.CreateUser(bg, "jyothri", "h")
	if err != nil {
		t.Fatal(err)
	}
	f := &applyFixture{st: st, user: uid, agent: uuid.NewString(), drive: "seagate2", stream: uuid.NewString()}
	if err := st.UpsertAgent(bg, store.Agent{ID: f.agent, UserID: uid, Hostname: "h"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.OpenDrive(bg, uid, f.agent, f.drive, wire.DriveOpenRequest{StreamID: f.stream, DriveRoot: "/mnt/seagate2"}); err != nil {
		t.Fatal(err)
	}
	return f
}

var t0 = time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

func fileUp(v int64, path string, size int64) wire.Change {
	return wire.Change{V: v, Kind: wire.KindFile, Op: wire.OpUpsert, Path: wire.Ptr(path), Size: wire.Ptr(size),
		MTimeUnix: wire.Ptr(int64(1726000000)), Mode: wire.Ptr(int64(0o644)), ContentHash: fmt.Sprintf("h%d", v),
		HashAlgo: "blake3", Status: "hashed", ScannedAt: wire.Ptr(t0)}
}
func fileDel(v int64, path string) wire.Change {
	return wire.Change{V: v, Kind: wire.KindFile, Op: wire.OpDelete, Path: wire.Ptr(path)}
}
func dirUp(v int64, parent, child string, isDir bool) wire.Change {
	return wire.Change{V: v, Kind: wire.KindDirChild, Op: wire.OpUpsert, Path: wire.Ptr(parent), Child: wire.Ptr(child),
		IsDir: wire.Ptr(isDir), FirstSeenAt: wire.Ptr(t0)}
}
func dirDel(v int64, parent, child string) wire.Change {
	return wire.Change{V: v, Kind: wire.KindDirChild, Op: wire.OpDelete, Path: wire.Ptr(parent), Child: wire.Ptr(child)}
}
func runUp(v, id int64) wire.Change {
	return wire.Change{V: v, Kind: wire.KindScanRun, Op: wire.OpUpsert, RunID: wire.Ptr(id), StartedAt: wire.Ptr(t0)}
}

func (f *applyFixture) batch(from, to int64, cs ...wire.Change) wire.ChangeBatch {
	if cs == nil {
		cs = []wire.Change{}
	}
	return wire.ChangeBatch{StreamID: f.stream, FromVersion: from, ToVersion: to, Changes: cs}
}

// send applies b with the agent's deterministic key.
func (f *applyFixture) send(t *testing.T, b wire.ChangeBatch) (wire.ChangesResponse, error) {
	t.Helper()
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%s|%d|%d", f.agent, f.drive, b.StreamID, b.FromVersion, b.ToVersion))))
	return f.sendKey(t, b, key)
}

func (f *applyFixture) sendKey(t *testing.T, b wire.ChangeBatch, key string) (wire.ChangesResponse, error) {
	t.Helper()
	body, _ := json.Marshal(b)
	sum := sha256.Sum256(body)
	return f.st.ApplyChanges(bg, store.ChangesRequest{UserID: f.user, AgentID: f.agent, DriveID: f.drive,
		IdempotencyKey: key, RequestSHA256: sum[:], Batch: b})
}

func (f *applyFixture) mustSend(t *testing.T, b wire.ChangeBatch) wire.ChangesResponse {
	t.Helper()
	r, err := f.send(t, b)
	if err != nil {
		t.Fatalf("apply (%d,%d]: %v", b.FromVersion, b.ToVersion, err)
	}
	return r
}

// state is everything stored for the drive, for comparisons.
type state struct {
	Files      map[string]int64 // path -> version
	Dirs       map[string]int64 // parent/child -> version
	Runs       map[int64]int64
	Tombstones map[string]int64 // kind:hex(key) -> version
	Ranges     []wire.Range
	Watermark  int64
}

func (f *applyFixture) state(t *testing.T) state {
	t.Helper()
	s := state{Files: map[string]int64{}, Dirs: map[string]int64{}, Runs: map[int64]int64{}, Tombstones: map[string]int64{}}
	q := func(sql string, each func(scan func(...any) error)) {
		rows, err := f.st.Pool.Query(bg, sql)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			each(rows.Scan)
		}
	}
	q(`SELECT relative_path, row_version FROM agent_files`, func(scan func(...any) error) {
		var p string
		var v int64
		scan(&p, &v)
		s.Files[p] = v
	})
	q(`SELECT relative_path || '/' || child_name, row_version FROM agent_dir_listings`, func(scan func(...any) error) {
		var p string
		var v int64
		scan(&p, &v)
		s.Dirs[p] = v
	})
	q(`SELECT run_id, row_version FROM agent_scan_runs`, func(scan func(...any) error) {
		var id, v int64
		scan(&id, &v)
		s.Runs[id] = v
	})
	q(`SELECT kind || ':' || encode(key, 'hex'), row_version FROM agent_tombstones`, func(scan func(...any) error) {
		var k string
		var v int64
		scan(&k, &v)
		s.Tombstones[k] = v
	})
	q(`SELECT from_version, to_version FROM agent_sync_ranges ORDER BY from_version`, func(scan func(...any) error) {
		var r wire.Range
		scan(&r[0], &r[1])
		s.Ranges = append(s.Ranges, r)
	})
	f.st.Pool.QueryRow(bg, `SELECT acked_version FROM agent_drives`).Scan(&s.Watermark)
	return s
}

func tombKey(kind string, parts ...string) string {
	if kind == wire.KindFile {
		return kind + ":" + fmt.Sprintf("%x", store.Hash([]byte(parts[0])))
	}
	return kind + ":" + fmt.Sprintf("%x", store.Hash([]byte(parts[0]), []byte{0}, []byte(parts[1])))
}

func TestGoldenBatch(t *testing.T) {
	f := newApply(t)
	b, err := os.ReadFile("../../../wire/testdata/changes-batch.json")
	if err != nil {
		t.Fatal(err)
	}
	var batch wire.ChangeBatch
	json.Unmarshal(b, &batch)
	f.stream = batch.StreamID
	f.st.OpenDrive(bg, f.user, f.agent, f.drive, wire.DriveOpenRequest{StreamID: f.stream, DriveRoot: "/mnt/seagate2"})

	r := f.mustSend(t, batch)
	if r.Applied != 8 || r.Skipped != 0 || len(r.Rejected) != 0 || r.Duplicate {
		t.Fatalf("response = %+v", r)
	}
	if !reflect.DeepEqual(r.AckedRanges, []wire.Range{{18234, 19233}}) {
		t.Errorf("ranges = %v", r.AckedRanges)
	}
	s := f.state(t)
	if s.Files["Jyo/Backup/a.jpg"] != 18240 || s.Files["Jyo/Backup/b.mov"] != 18241 || s.Files[`Jyo/caf\xE9`] != 18250 {
		t.Errorf("files = %v", s.Files)
	}
	if s.Dirs["Jyo/Backup/photos"] != 18400 || s.Dirs["/Jyo"] != 18401 || s.Runs[42] != 19233 {
		t.Errorf("dirs = %v runs = %v", s.Dirs, s.Runs)
	}
	if s.Tombstones[tombKey(wire.KindFile, "Jyo/Backup/old.txt")] != 18302 || s.Tombstones[tombKey(wire.KindDirChild, "Jyo/Backup", "tmp")] != 18402 {
		t.Errorf("tombstones = %v", s.Tombstones)
	}
	var raw []byte
	var mtime time.Time
	f.st.Pool.QueryRow(bg, `SELECT raw_path FROM agent_files WHERE relative_path = 'Jyo/caf\xE9'`).Scan(&raw)
	f.st.Pool.QueryRow(bg, `SELECT mtime FROM agent_files WHERE relative_path = 'Jyo/Backup/a.jpg'`).Scan(&mtime)
	if string(raw) != "Jyo/caf\xe9" || mtime.Unix() != 1726000000 {
		t.Errorf("raw = %q, mtime = %v", raw, mtime)
	}
}

func TestDuplicates(t *testing.T) {
	f := newApply(t)
	f.mustSend(t, f.batch(0, 10, fileUp(5, "a", 1)))
	before := f.state(t)

	r := f.mustSend(t, f.batch(0, 10, fileUp(5, "a", 1)))
	if !r.Duplicate || r.Applied != 0 {
		t.Errorf("replay = %+v", r)
	}
	// Same range, different contents (a re-read page): still a duplicate, not 422.
	r, err := f.send(t, f.batch(0, 10, fileUp(5, "a", 1), fileUp(7, "b", 2)))
	if err != nil || !r.Duplicate {
		t.Errorf("same range, other contents: %+v, %v", r, err)
	}
	// A sub-range too.
	if r := f.mustSend(t, f.batch(2, 6)); !r.Duplicate {
		t.Errorf("sub-range = %+v", r)
	}
	if !reflect.DeepEqual(f.state(t), before) {
		t.Error("a duplicate changed the state")
	}
}

func TestIdempotencyKey(t *testing.T) {
	f := newApply(t)
	first := f.batch(0, 10, fileUp(5, "a", 1))
	r1, err := f.sendKey(t, first, "K")
	if err != nil {
		t.Fatal(err)
	}
	// The same key for another, uncovered batch: 422.
	if _, err := f.sendKey(t, f.batch(10, 20, fileUp(15, "b", 1)), "K"); !errors.Is(err, store.ErrIdempotencyKeyReused) {
		t.Errorf("reuse: %v", err)
	}
	// With the coverage gone (only possible by hand), the stored response comes back.
	f.st.Pool.Exec(bg, `DELETE FROM agent_sync_ranges`)
	r2, err := f.sendKey(t, first, "K")
	if err != nil || !reflect.DeepEqual(r1, r2) {
		t.Errorf("stored response: %+v, %v; want %+v", r2, err, r1)
	}
}

func TestPartialOverlapAndWatermark(t *testing.T) {
	f := newApply(t)
	f.mustSend(t, f.batch(0, 10, fileUp(5, "a", 1)))
	r := f.mustSend(t, f.batch(5, 15, fileUp(7, "x", 1), fileUp(12, "b", 1)))
	if r.Applied != 1 || r.Skipped != 1 || !reflect.DeepEqual(r.AckedRanges, []wire.Range{{0, 15}}) {
		t.Errorf("response = %+v", r)
	}
	s := f.state(t)
	if _, ok := s.Files["x"]; ok {
		t.Error("an entry inside an acked range was applied")
	}
	if s.Watermark != 15 {
		t.Errorf("watermark = %d", s.Watermark)
	}
}

func TestOutOfOrderAndGapClosing(t *testing.T) {
	f := newApply(t)
	// A scan uploads its own changes first; history comes later.
	f.mustSend(t, f.batch(20, 30, fileUp(25, "new", 1)))
	if s := f.state(t); s.Watermark != 0 || !reflect.DeepEqual(s.Ranges, []wire.Range{{20, 30}}) {
		t.Errorf("after the scan: %+v", s)
	}
	f.mustSend(t, f.batch(0, 10, fileUp(5, "old", 1)))
	// The gap (10, 20] has nothing pending: an empty batch closes it.
	r := f.mustSend(t, f.batch(10, 20))
	if !reflect.DeepEqual(r.AckedRanges, []wire.Range{{0, 30}}) || r.Applied != 0 {
		t.Errorf("gap closed: %+v", r)
	}
	if f.state(t).Watermark != 30 {
		t.Errorf("watermark = %d", f.state(t).Watermark)
	}
}

func TestHigherVersionWins(t *testing.T) {
	f := newApply(t)
	// A newer delete arrives before an older upsert: the tombstone wins.
	f.mustSend(t, f.batch(10, 20, fileDel(15, "gone"), dirDel(16, "d", "gone")))
	r := f.mustSend(t, f.batch(0, 10, fileUp(5, "gone", 1), dirUp(6, "d", "gone", false)))
	if r.Applied != 0 || r.Skipped != 2 {
		t.Errorf("stale upserts: %+v", r)
	}
	s := f.state(t)
	if _, ok := s.Files["gone"]; ok || s.Tombstones[tombKey(wire.KindFile, "gone")] != 15 {
		t.Errorf("stale upsert resurrected the file: %+v", s)
	}
	if _, ok := s.Dirs["d/gone"]; ok {
		t.Error("stale upsert resurrected the listing")
	}

	// A delete older than the live row is ignored.
	f.mustSend(t, f.batch(30, 40, fileUp(35, "live", 1)))
	f.mustSend(t, f.batch(20, 30, fileDel(25, "live")))
	s = f.state(t)
	if s.Files["live"] != 35 {
		t.Error("an older delete removed a newer row")
	}
	if _, ok := s.Tombstones[tombKey(wire.KindFile, "live")]; ok {
		t.Error("an older delete left a tombstone next to a newer row")
	}

	// A newer upsert replaces the tombstone.
	f.mustSend(t, f.batch(40, 50, fileUp(45, "gone", 2)))
	s = f.state(t)
	if s.Files["gone"] != 45 {
		t.Error("newer upsert not applied")
	}
	if _, ok := s.Tombstones[tombKey(wire.KindFile, "gone")]; ok {
		t.Error("the superseded tombstone survived")
	}
}

func TestScanRunUpserts(t *testing.T) {
	f := newApply(t)
	f.mustSend(t, f.batch(0, 10, runUp(5, 1)))
	finished := runUp(15, 1)
	finished.FinishedAt, finished.FilesSeen, finished.Interrupted = wire.Ptr(t0.Add(time.Hour)), wire.Ptr(int64(9)), wire.Ptr(false)
	f.mustSend(t, f.batch(10, 20, finished))
	var fa *time.Time
	var seen *int64
	f.st.Pool.QueryRow(bg, `SELECT finished_at, files_seen FROM agent_scan_runs WHERE run_id = 1`).Scan(&fa, &seen)
	if fa == nil || seen == nil || *seen != 9 || f.state(t).Runs[1] != 15 {
		t.Errorf("run = %v %v %v", fa, seen, f.state(t).Runs)
	}
}

func TestRejectedEntries(t *testing.T) {
	f := newApply(t)
	f.mustSend(t, f.batch(0, 10, fileUp(5, "a", 1)))

	bad := fileUp(15, "a", 2)
	bad.MTimeUnix = wire.Ptr(int64(1) << 40)
	noKey := wire.Change{V: 16, Kind: wire.KindFile, Op: wire.OpUpsert, PathB64: "!!!"}
	r := f.mustSend(t, f.batch(10, 20, bad, noKey, fileUp(17, "b", 1), wire.Change{V: 18, Kind: "bogus", Op: "upsert"}))
	if r.Applied != 1 || len(r.Rejected) != 3 || !reflect.DeepEqual(r.AckedRanges, []wire.Range{{0, 20}}) {
		t.Fatalf("response = %+v", r)
	}
	if r.Rejected[0].V != 15 || !strings.Contains(r.Rejected[0].Reason, "mtime") {
		t.Errorf("rejected = %+v", r.Rejected)
	}
	// The rejected upsert's older row is replaced by a tombstone at its version.
	s := f.state(t)
	if _, ok := s.Files["a"]; ok || s.Tombstones[tombKey(wire.KindFile, "a")] != 15 {
		t.Errorf("stale row kept after a rejected upsert: %+v", s)
	}
}

func TestBulkFailureFallsBackPerEntry(t *testing.T) {
	f := newApply(t)
	f.mustSend(t, f.batch(0, 10, fileUp(5, "bad", 1)))
	// Postgres rejects NUL in text (SQLSTATE 22021); validation doesn't
	// look at error_message, so this reaches the bulk statement.
	bad := fileUp(15, "bad", 2)
	bad.Status, bad.ErrorMessage = "error", "boom\x00"
	r := f.mustSend(t, f.batch(10, 20, fileUp(12, "good1", 1), bad, fileUp(18, "good2", 1)))
	if r.Applied != 2 || len(r.Rejected) != 1 || r.Rejected[0].V != 15 || !strings.Contains(r.Rejected[0].Reason, "22021") {
		t.Fatalf("response = %+v", r)
	}
	s := f.state(t)
	if s.Files["good1"] != 12 || s.Files["good2"] != 18 {
		t.Errorf("files = %v", s.Files)
	}
	if _, ok := s.Files["bad"]; ok || s.Tombstones[tombKey(wire.KindFile, "bad")] != 15 {
		t.Errorf("the failed upsert should leave a tombstone: %+v", s)
	}
}

func TestNonDataErrorAppliesNothing(t *testing.T) {
	f := newApply(t)
	f.mustSend(t, f.batch(0, 10, fileUp(5, "a", 1)))
	before := f.state(t)
	store.SetApplyHook(func(group string) error {
		if group == "dir_upserts" {
			return &pgconn.PgError{Code: "40001", Message: "could not serialize access"}
		}
		return nil
	})
	defer store.SetApplyHook(nil)
	_, err := f.send(t, f.batch(10, 20, fileUp(12, "b", 1), dirUp(15, "", "d", true)))
	if err == nil || store.IsDataError(err) {
		t.Fatalf("err = %v, want the serialization failure", err)
	}
	if !reflect.DeepEqual(f.state(t), before) {
		t.Error("a failed request left partial changes")
	}
}

func TestLongPaths(t *testing.T) {
	f := newApply(t)
	long := strings.Repeat("d/", 2000) + "file" // 4004 bytes
	parent := strings.Repeat("p", 4000)
	child := strings.Repeat("c", 255)
	r := f.mustSend(t, f.batch(0, 10, fileUp(5, long, 1), dirUp(6, parent, child, false)))
	if r.Applied != 2 {
		t.Fatalf("response = %+v", r)
	}
	s := f.state(t)
	if s.Files[long] != 5 || s.Dirs[parent+"/"+child] != 6 {
		t.Error("long paths not stored")
	}
}

func TestNamesThatArentUTF8(t *testing.T) {
	f := newApply(t)
	b64 := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	raw := wire.Change{V: 1, Kind: wire.KindFile, Op: wire.OpUpsert, PathB64: b64("caf\xe9")}
	literal := fileUp(2, `caf\xE9`, 1) // a file literally named with a backslash
	for _, c := range []*wire.Change{&raw} {
		c.Size, c.MTimeUnix, c.Mode, c.Status, c.ScannedAt = wire.Ptr(int64(1)), wire.Ptr(int64(1)), wire.Ptr(int64(420)), "error", wire.Ptr(t0)
	}
	validUTF8 := wire.Change{V: 3, Kind: wire.KindFile, Op: wire.OpDelete, PathB64: b64("plain")}
	withNUL := wire.Change{V: 4, Kind: wire.KindFile, Op: wire.OpDelete, PathB64: b64("a\x00\xff")}
	both := wire.Change{V: 5, Kind: wire.KindFile, Op: wire.OpDelete, Path: wire.Ptr("x"), PathB64: b64("\xff")}
	badChild := wire.Change{V: 6, Kind: wire.KindDirChild, Op: wire.OpUpsert, Path: wire.Ptr(""), ChildB64: b64("x\xffy"),
		IsDir: wire.Ptr(false), FirstSeenAt: wire.Ptr(t0)}
	r := f.mustSend(t, f.batch(0, 10, raw, literal, validUTF8, withNUL, both, badChild))
	if r.Applied != 3 || len(r.Rejected) != 3 {
		t.Fatalf("response = %+v", r)
	}
	var n int
	f.st.Pool.QueryRow(bg, `SELECT count(*) FROM agent_files WHERE relative_path = 'caf\xE9'`).Scan(&n)
	if n != 2 {
		t.Errorf("the raw name and the literal one should be two rows with the same display form, got %d", n)
	}
	var child string
	var rawChild []byte
	f.st.Pool.QueryRow(bg, `SELECT child_name, raw_child_name FROM agent_dir_listings`).Scan(&child, &rawChild)
	if child != `x\xFFy` || string(rawChild) != "x\xffy" {
		t.Errorf("child = %q raw = %q", child, rawChild)
	}
}

func TestDriveAndStreamChecks(t *testing.T) {
	f := newApply(t)
	f.drive = "never-opened"
	if _, err := f.send(t, f.batch(0, 10)); !errors.Is(err, store.ErrDriveNotOpen) {
		t.Errorf("unopened drive: %v", err)
	}
	f.drive = "seagate2"
	current := f.stream
	f.stream = uuid.NewString()
	_, err := f.send(t, f.batch(0, 10))
	var mm *store.StreamMismatchError
	if !errors.As(err, &mm) || mm.StreamID != current {
		t.Errorf("stream mismatch: %v", err)
	}
}

func TestMergeRange(t *testing.T) {
	r := func(pairs ...int64) []wire.Range {
		out := []wire.Range{}
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, wire.Range{pairs[i], pairs[i+1]})
		}
		return out
	}
	cases := []struct {
		have []wire.Range
		add  wire.Range
		want []wire.Range
	}{
		{r(), wire.Range{0, 10}, r(0, 10)},
		{r(0, 10), wire.Range{10, 20}, r(0, 20)},         // touching
		{r(0, 10), wire.Range{5, 15}, r(0, 15)},          // overlapping
		{r(0, 10, 20, 30), wire.Range{10, 20}, r(0, 30)}, // closing a gap
		{r(0, 10, 20, 30), wire.Range{12, 15}, r(0, 10, 12, 15, 20, 30)},
		{r(20, 30), wire.Range{0, 10}, r(0, 10, 20, 30)},
		{r(0, 10, 20, 30, 40, 50), wire.Range{5, 45}, r(0, 50)},
	}
	for _, c := range cases {
		if got := store.MergeRange(c.have, c.add); !reflect.DeepEqual(got, c.want) {
			t.Errorf("merge %v + %v = %v, want %v", c.have, c.add, got, c.want)
		}
	}
	if store.Watermark(r(0, 30, 40, 50)) != 30 || store.Watermark(r(10, 30)) != 0 || store.Watermark(r()) != 0 {
		t.Error("watermark")
	}
}

// The property the design rests on: whatever order batches arrive in, the
// server ends in the same state, the one the full history implies.
func TestAnyBatchOrderGivesTheSameState(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	// A history of 60 operations on 8 files and 4 listings.
	type op struct {
		v      int64
		change wire.Change
		key    string
	}
	var history []op
	for v := int64(1); v <= 60; v++ {
		if rng.Intn(3) == 0 {
			parent, child := fmt.Sprintf("dir%d", rng.Intn(2)), fmt.Sprintf("c%d", rng.Intn(2))
			c := dirUp(v, parent, child, rng.Intn(2) == 0)
			if rng.Intn(3) == 0 {
				c = dirDel(v, parent, child)
			}
			history = append(history, op{v, c, "d:" + parent + "/" + child})
			continue
		}
		p := fmt.Sprintf("f%d", rng.Intn(8))
		c := fileUp(v, p, v)
		if rng.Intn(3) == 0 {
			c = fileDel(v, p)
		}
		history = append(history, op{v, c, "f:" + p})
	}
	// Batches cover consecutive ranges; each holds, per key, its latest
	// operation inside the range (what an agent reads right after writing).
	bounds := []int64{0, 7, 15, 22, 30, 41, 50, 60}
	type rawBatch struct {
		from, to int64
		changes  []wire.Change
	}
	var batches []rawBatch
	for i := 0; i+1 < len(bounds); i++ {
		latest := map[string]op{}
		for _, o := range history {
			if o.v > bounds[i] && o.v <= bounds[i+1] {
				latest[o.key] = o
			}
		}
		var cs []wire.Change
		for _, o := range latest {
			cs = append(cs, o.change)
		}
		sort.Slice(cs, func(a, b int) bool { return cs[a].V < cs[b].V })
		batches = append(batches, rawBatch{bounds[i], bounds[i+1], cs})
	}

	var reference *state
	for round := 0; round < 8; round++ {
		f := newApply(t)
		order := rng.Perm(len(batches))
		for _, i := range order {
			b := batches[i]
			f.mustSend(t, f.batch(b.from, b.to, b.changes...))
		}
		s := f.state(t)
		if !reflect.DeepEqual(s.Ranges, []wire.Range{{0, 60}}) || s.Watermark != 60 {
			t.Fatalf("round %d: ranges %v watermark %d", round, s.Ranges, s.Watermark)
		}
		// Expected: each key's last operation in the whole history.
		final := map[string]op{}
		for _, o := range history {
			final[o.key] = o
		}
		for key, o := range final {
			name := key[2:]
			if strings.HasPrefix(key, "f:") {
				if o.change.Op == wire.OpUpsert && s.Files[name] != o.v {
					t.Fatalf("round %d (order %v): file %s at %d, want %d", round, order, name, s.Files[name], o.v)
				}
				if o.change.Op == wire.OpDelete {
					if _, ok := s.Files[name]; ok {
						t.Fatalf("round %d (order %v): deleted file %s present", round, order, name)
					}
				}
			} else if o.change.Op == wire.OpUpsert && s.Dirs[name] != o.v {
				t.Fatalf("round %d (order %v): listing %s at %d, want %d", round, order, name, s.Dirs[name], o.v)
			}
		}
		if reference == nil {
			reference = &s
		} else if !reflect.DeepEqual(reference.Files, s.Files) || !reflect.DeepEqual(reference.Dirs, s.Dirs) {
			t.Fatalf("round %d (order %v): state differs from the first order", round, order)
		}
	}
}
