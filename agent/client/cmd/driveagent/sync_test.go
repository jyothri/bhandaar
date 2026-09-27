package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/remote/remotetest"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/client/internal/syncer"
	"github.com/jyothri/bhandaar/agent/wire"
)

func syncCmd(t *testing.T, ctx context.Context, stateDir, url string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	args = append([]string{"--state-dir", stateDir, "--remote-url", url}, args...)
	err := runSync(ctx, args, &out, &errOut)
	return result{err, out.String(), errOut.String()}
}

// scanned fills a state dir the way scans would: drives with files,
// listings, a deletion and a scan run. It returns each drive's live row
// count (files + listings + runs).
func scanned(t *testing.T, dir string, drives ...string) map[string]int {
	t.Helper()
	st, err := store.OpenWith(dir, store.Options{Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	live := map[string]int{}
	for _, d := range drives {
		st.UpsertDrive(d, "/mnt/"+d, "")
		run, _ := st.StartScanRun(d)
		var recs []store.FileRecord
		for _, p := range []string{"a", "b", "c", "gone"} {
			recs = append(recs, store.FileRecord{DriveID: d, RelPath: p, Size: 1, Mode: 0o644, ContentHash: "h", HashAlgo: "blake3",
				Status: store.StatusHashed, ScannedAt: time.Now()})
		}
		if err := st.UpsertFiles(context.Background(), recs); err != nil {
			t.Fatal(err)
		}
		st.DeleteFiles(context.Background(), d, []string{"gone"})
		st.SyncDirListings(context.Background(), d, map[string][]store.DirChild{"": {{Name: "a"}, {Name: "b"}, {Name: "c"}}}, true)
		st.FinishScanRun(run, d, 3, 3, false)
		live[d] = 3 + 3 + 1
	}
	return live
}

func TestSyncUploadsNothingBeforeHandshake(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()
	scanned(t, dir, "d1")

	// Not logged in.
	r := syncCmd(t, context.Background(), dir, srv.URL)
	if r.code() != exitRemote || !strings.Contains(r.err.Error(), "not logged in") {
		t.Errorf("not logged in: code %d, %v", r.code(), r.err)
	}
	login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")

	srv.Decision = wire.DecisionUpgradeRequired
	if r := syncCmd(t, context.Background(), dir, srv.URL); r.code() != exitUpgrade {
		t.Errorf("upgrade required: code %d, %v", r.code(), r.err)
	}
	srv.Decision = ""
	srv.Down = true
	if r := syncCmd(t, context.Background(), dir, srv.URL); r.code() != exitRemote {
		t.Errorf("server down: code %d, %v", r.code(), r.err)
	}
	for _, p := range srv.Paths() {
		if strings.HasPrefix(p, "/agent/v1/drives") {
			t.Errorf("request to %s before a successful preflight", p)
		}
	}
}

func TestSyncUploadsEveryDrive(t *testing.T) {
	srv := remotetest.New(t)
	srv.Limits = &wire.Limits{MaxChangesPerBatch: 2, MaxBatchBytes: 1 << 20}
	dir := t.TempDir()
	live := scanned(t, dir, "d2", "d1")
	login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")

	// --drive-id picks drives; an unknown one is a usage error.
	if r := syncCmd(t, context.Background(), dir, srv.URL, "--drive-id", "nope"); r.code() != exitUsage {
		t.Errorf("unknown drive: code %d, %v", r.code(), r.err)
	}
	r := syncCmd(t, context.Background(), dir, srv.URL, "--drive-id", "d2")
	if r.err != nil || !strings.Contains(r.stdout, "d2: uploaded 8 changes, fully synced") || srv.Live("d1") != nil {
		t.Fatalf("sync d2: %v\n%s%s", r.err, r.stdout, r.stderr)
	}

	r = syncCmd(t, context.Background(), dir, srv.URL)
	if r.err != nil {
		t.Fatalf("sync: %v\n%s%s", r.err, r.stdout, r.stderr)
	}
	// In drive_id order, one line each.
	want := "d1: uploaded 8 changes, fully synced\nd2: uploaded 0 changes, fully synced\n"
	if r.stdout != want {
		t.Errorf("stdout = %q, want %q", r.stdout, want)
	}
	for d, n := range live {
		if got := len(srv.Live(d)); got != n {
			t.Errorf("%s: server has %d rows, want %d", d, got, n)
		}
	}
}

func TestSyncSkipsLockedDrive(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()
	scanned(t, dir, "d1", "d2")
	login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")

	lock, ok, err := syncer.TryLock(dir, "d1") // a scan of d1
	if err != nil || !ok {
		t.Fatal(err)
	}
	r := syncCmd(t, context.Background(), dir, srv.URL)
	lock.Unlock()
	if r.err != nil || !strings.Contains(r.stdout, "d1: skipped") || srv.Live("d1") != nil || len(srv.Live("d2")) != 7 {
		t.Errorf("%v\n%s", r.err, r.stdout)
	}

	// remote-status doesn't reconcile a locked drive either.
	syncCmd(t, context.Background(), dir, srv.URL)
	lock, _, _ = syncer.TryLock(dir, "d1")
	defer lock.Unlock()
	n := strings.Count(strings.Join(srv.Paths(), " "), "/agent/v1/drives/d1 ")
	s := status(t, dir, srv.URL)
	if s.err != nil || !strings.Contains(s.stdout, "being uploaded by another driveagent") ||
		strings.Count(strings.Join(srv.Paths(), " "), "/agent/v1/drives/d1 ") != n {
		t.Errorf("status of a locked drive: %v\n%s", s.err, s.stdout)
	}
}

// An interrupted sync, whether between batches or between the server's
// commit and the local marker update, resumes to the same server state.
func TestSyncInterruptedResumes(t *testing.T) {
	for _, lost := range []bool{false, true} {
		srv := remotetest.New(t)
		srv.Limits = &wire.Limits{MaxChangesPerBatch: 2, MaxBatchBytes: 1 << 20}
		dir := t.TempDir()
		live := scanned(t, dir, "d1")
		login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")

		ctx, cancel := context.WithCancel(context.Background())
		applied := 0
		srv.AfterApply = func(s *remotetest.Server) {
			if applied++; applied == 2 {
				if lost {
					s.DropChanges = 1 // committed, but the answer never arrives
				}
				cancel()
			}
		}
		r := syncCmd(t, ctx, dir, srv.URL)
		cancel()
		if r.err == nil {
			t.Fatalf("lost=%v: the interrupted sync succeeded\n%s", lost, r.stdout)
		}
		srv.AfterApply = nil
		ranges := srv.DriveRanges("d1")
		if len(ranges) != 1 || ranges[0][1] == 0 {
			t.Fatalf("lost=%v: server ranges after the interruption %v", lost, ranges)
		}

		r = syncCmd(t, context.Background(), dir, srv.URL)
		if r.err != nil || !strings.Contains(r.stdout, "fully synced") {
			t.Fatalf("lost=%v: resumed sync: %v\n%s", lost, r.err, r.stdout)
		}
		if got := len(srv.Live("d1")); got != live["d1"] {
			t.Errorf("lost=%v: server has %d rows, want %d", lost, got, live["d1"])
		}
		for _, b := range srv.Batches {
			if b.StreamID != srv.Batches[0].StreamID {
				t.Errorf("lost=%v: the drive started over on a new stream", lost)
			}
		}
	}
}

// A state.db from before the change feed (PR 3's migration) uploads in full.
func TestSyncMigratedStateDB(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE drives (drive_id TEXT PRIMARY KEY, drive_root TEXT NOT NULL, backup_root TEXT NOT NULL DEFAULT '',
			last_scan_started_at TIMESTAMP, last_scan_completed_at TIMESTAMP)`,
		`CREATE TABLE files (drive_id TEXT NOT NULL, relative_path TEXT NOT NULL, size INTEGER NOT NULL, mtime_unix INTEGER NOT NULL,
			mode INTEGER NOT NULL, quick_sig TEXT, content_hash TEXT, hash_algo TEXT, status TEXT NOT NULL, error_message TEXT,
			scanned_at TIMESTAMP NOT NULL, comparison_status TEXT, compared_against_drive_id TEXT, counterpart_relative_path TEXT,
			compared_at TIMESTAMP, PRIMARY KEY (drive_id, relative_path))`,
		`CREATE TABLE scan_runs (id INTEGER PRIMARY KEY AUTOINCREMENT, drive_id TEXT NOT NULL, started_at TIMESTAMP NOT NULL,
			finished_at TIMESTAMP, files_seen INTEGER, bytes_hashed INTEGER, interrupted BOOLEAN)`,
		`CREATE TABLE dir_listings (drive_id TEXT NOT NULL, relative_path TEXT NOT NULL, child_name TEXT NOT NULL, is_dir BOOLEAN NOT NULL,
			first_seen_at TIMESTAMP NOT NULL, last_seen_at TIMESTAMP NOT NULL, PRIMARY KEY (drive_id, relative_path, child_name))`,
		`INSERT INTO drives (drive_id, drive_root) VALUES ('seagate1', '/media/jyothri/Seagate1')`,
		`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 2500)
		 INSERT INTO files (drive_id, relative_path, size, mtime_unix, mode, content_hash, hash_algo, status, scanned_at)
		 SELECT 'seagate1', 'Jyo/f' || i, i, 1726000000, 420, 'h' || i, 'blake3', 'hashed', '2026-01-01 10:00:00+00:00' FROM n`,
		`INSERT INTO dir_listings VALUES ('seagate1', '', 'Jyo', 1, '2026-01-01', '2026-01-01')`,
		`INSERT INTO scan_runs (drive_id, started_at, finished_at, files_seen, bytes_hashed, interrupted)
		 VALUES ('seagate1', '2026-01-01', '2026-01-01', 2500, 99, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()
	// Migrate it (and log in) before the sync, as the first command after
	// upgrading would.
	st, err := store.OpenWith(dir, store.Options{Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")

	r := syncCmd(t, context.Background(), dir, srv.URL)
	if r.err != nil || !strings.Contains(r.stdout, "seagate1: uploaded 2,502 changes, fully synced") {
		t.Fatalf("%v\n%s%s", r.err, r.stdout, r.stderr)
	}
	if got := len(srv.Live("seagate1")); got != 2502 {
		t.Errorf("server has %d rows", got)
	}
	if len(srv.Batches) != 3 {
		t.Errorf("%d batches, want 3 of at most 1,000", len(srv.Batches))
	}
}

func TestRemoteStatusDrives(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()

	login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")
	if r := status(t, dir, srv.URL); r.err != nil || !strings.Contains(r.stdout, "drives     none (no state.db") {
		t.Errorf("no state.db: %v\n%s", r.err, r.stdout)
	}

	scanned(t, dir, "d1", "d2")
	st, _ := store.OpenWith(dir, store.Options{Log: io.Discard})
	st.SetDriveIdentity("d1", store.DriveIdentity{FSUUID: "E12AD136", FSType: "ext4", FSUUIDSource: "linux", HWSerial: "WD-W1", SeenAt: time.Now()})
	st.Close()
	r := status(t, dir, srv.URL)
	for _, want := range []string{
		"drive d1\n", "identity  filesystem E12AD136 (ext4, linux), serial WD-W1",
		"synced    watermark 0, 0 range(s) above it, last synced never", "pending   8\n", // and a tombstone
		`server    not uploaded yet: run "driveagent sync"`,
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("before sync: missing %q\n%s", want, r.stdout)
		}
	}
	if r.err != nil || strings.Contains(strings.Join(srv.Paths(), " "), "/agent/v1/drives/") {
		t.Errorf("status before any sync: %v, requests %v", r.err, srv.Paths())
	}

	srv.Reject = func(c wire.Change) string {
		if c.Path != nil && *c.Path == "b" && c.Kind == wire.KindFile {
			return "mtime out of range"
		}
		return ""
	}
	if r := syncCmd(t, context.Background(), dir, srv.URL); r.err != nil || !strings.Contains(r.stdout, "d1: uploaded 8 changes (1 rejected by the server") {
		t.Fatalf("sync: %v\n%s", r.err, r.stdout)
	}
	r = status(t, dir, srv.URL)
	for _, want := range []string{
		"pending   0\n", "server    acked [[0 ", "linked    also scanned by: macbook as mac-E12AD136, never synced",
		`rejected  file "b" (version `, "): mtime out of range",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Errorf("after sync: missing %q\n%s", want, r.stdout)
		}
	}

	// The server has a drive this state.db doesn't; and with the server
	// down, the local part still shows.
	srv.Lock()
	srv.Drives["elsewhere"] = &remotetest.Drive{StreamID: "x", Rows: map[string]remotetest.Row{}}
	srv.Unlock()
	if r := status(t, dir, srv.URL); !strings.Contains(r.stdout, "the server also has drives not in this state.db: elsewhere") {
		t.Errorf("extra server drive:\n%s", r.stdout)
	}
	srv.Down = true
	r = status(t, dir, srv.URL)
	if r.code() != exitRemote || !strings.Contains(r.stdout, "server    not checked") || !strings.Contains(r.stdout, "pending   0") {
		t.Errorf("server down: code %d\n%s", r.code(), r.stdout)
	}
}

func TestCount(t *testing.T) {
	for n, want := range map[int64]string{0: "0", 999: "999", 1000: "1,000", 18532: "18,532", 1234567: "1,234,567", -1204: "-1,204"} {
		if got := count(n); got != want {
			t.Errorf("count(%d) = %q", n, got)
		}
	}
}
