package syncer

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/remote/remotetest"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

func TestSyncUploadsHistory(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.drive("other")
	e.run("d1")
	e.files("d1", "Jyo/a.jpg", "Jyo/b.mov", "Jyo/old.txt", "Jyo/caf\xe9")
	e.dirs("d1", "", "Jyo")
	e.dirs("d1", "Jyo", "a.jpg", "b.mov", "old.txt", "tmp", "caf\xe9")
	e.del("d1", "Jyo/old.txt")
	e.dirs("d1", "Jyo", "a.jpg", "b.mov", "caf\xe9") // old.txt and tmp removed: tombstones
	e.files("other", "x")
	e.st.SetDriveIdentity("d1", store.DriveIdentity{FSUUID: "ABCD1234", FSType: "ext4", FSUUIDSource: "linux", SeenAt: time.Now()})

	res := e.sync("d1")
	e.checkSynced("d1", res)
	if res.Uploaded == 0 || res.Rejected != 0 || res.NewStream != "" {
		t.Errorf("result = %+v", res)
	}
	if e.srv.Live("other") != nil {
		t.Error("another drive was uploaded")
	}
	var tombs int
	for _, b := range e.srv.Batches {
		for _, c := range b.Changes {
			if c.Op == wire.OpDelete {
				tombs++
			}
		}
	}
	if tombs != 3 {
		t.Errorf("uploaded %d deletes, want 3 (a file, two listings)", tombs)
	}
	if n, _ := e.feed.Count(ctx, "d1", []wire.Range{{0, e.head()}}); n != int64(len(e.local("d1"))) {
		t.Errorf("synced tombstones not pruned: %d entries, %d live", n, len(e.local("d1")))
	}

	// The open request carried the drive's roots and identity, and the
	// answer's link came back.
	req, _ := e.srv.Last("/agent/v1/drives/d1")
	var open wire.DriveOpenRequest
	json.Unmarshal(req.Body, &open)
	if open.DriveRoot != "/mnt/d1" || open.Identity == nil || open.Identity.FSUUID != "ABCD1234" || open.StreamID != e.marker("d1").StreamID {
		t.Errorf("open request = %+v", open)
	}
	if res.Drive.PhysicalDrive == nil || len(res.Drive.PhysicalDrive.Linked) != 1 {
		t.Errorf("open response = %+v", res.Drive)
	}

	// A second sync closes the (empty) gap the other drive's writes left,
	// and sends nothing else.
	e.files("other", "y")
	n := e.posts()
	res = e.sync("d1")
	e.checkSynced("d1", res)
	if e.posts() != n+1 || res.Uploaded != 0 {
		t.Errorf("second sync: %d batches, uploaded %d", e.posts()-n, res.Uploaded)
	}
	// With nothing new anywhere, nothing is sent at all.
	n = e.posts()
	e.sync("d1")
	if e.posts() != n {
		t.Errorf("an idle sync sent %d batches", e.posts()-n)
	}
}

// The wire mapping: every kind, the base64 form for a name that isn't
// UTF-8, and nothing else in _b64.
func TestFeedMapping(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.run("d1")
	e.files("d1", "caf\xe9", "ok")
	e.dirs("d1", "", "caf\xe9", "ok")
	e.del("d1", "caf\xe9")
	page, more, err := e.feed.Page(ctx, "d1", 0, e.head(), 100)
	if err != nil || more {
		t.Fatalf("page: %v, more %v", err, more)
	}
	byKind := map[string][]wire.Change{}
	for i, en := range page {
		if i > 0 && en.Change.V <= page[i-1].Change.V {
			t.Errorf("page not sorted by version")
		}
		byKind[en.Change.Kind+" "+en.Change.Op] = append(byKind[en.Change.Kind+" "+en.Change.Op], en.Change)
	}
	run := byKind["scan_run upsert"]
	if len(run) != 1 || *run[0].RunID != 1 || run[0].FinishedAt == nil || *run[0].FilesSeen != 3 || *run[0].Interrupted {
		t.Errorf("scan_run = %+v", run)
	}
	files := byKind["file upsert"]
	if len(files) != 1 || *files[0].Path != "ok" || *files[0].Size != 2 || *files[0].Mode != 0o644 ||
		files[0].ContentHash != "h-ok" || files[0].Status != "hashed" || files[0].ScannedAt.Location() != time.UTC {
		t.Errorf("files = %+v", files)
	}
	del := byKind["file delete"]
	if len(del) != 1 || del[0].Path != nil || del[0].PathB64 != "Y2Fm6Q==" {
		t.Errorf("file delete = %+v", del)
	}
	dirs := byKind["dir_child upsert"]
	if len(dirs) != 2 || *dirs[0].Path != "" || dirs[0].IsDir == nil || dirs[0].FirstSeenAt == nil {
		t.Errorf("dir_children = %+v", dirs)
	}
	for _, d := range dirs {
		if (d.Child == nil) == (d.ChildB64 == "") {
			t.Errorf("dir_child %+v: want exactly one of child and child_b64", d)
		}
	}

	// A page stops at its limit and says there's more.
	page, more, _ = e.feed.Page(ctx, "d1", 0, e.head(), 2)
	if len(page) != 2 || !more {
		t.Errorf("limited page: %d entries, more %v", len(page), more)
	}
	if n, err := e.feed.Count(ctx, "d1", []wire.Range{{0, e.head()}}); err != nil || n != 5 { // the run, ok, two listings, a tombstone
		t.Errorf("count = %d, %v", n, err)
	}
}

func TestBatchBuild(t *testing.T) {
	s := session{agentID: "a", driveID: "d", streamID: "s", upper: 20, closeGap: true}
	entry := func(v int64) Entry {
		p := "f"
		return Entry{Change: wire.Change{V: v, Kind: wire.KindFile, Op: wire.OpDelete, Path: &p}}
	}

	// The end of the gap stretches to_version to its upper end.
	b, err := s.build(10, []Entry{entry(12), entry(15)}, false, DefaultMaxBytes)
	if err != nil || b.from != 10 || b.to != 20 || len(b.entries) != 2 {
		t.Fatalf("closing batch = %+v, %v", b, err)
	}
	// Not at the end: to_version is the last entry.
	if b, _ := s.build(10, []Entry{entry(12), entry(15)}, true, DefaultMaxBytes); b.to != 15 {
		t.Errorf("mid-gap batch to = %d", b.to)
	}
	// An empty page at the end of the gap still closes it...
	if b, _ := s.build(15, nil, false, DefaultMaxBytes); b == nil || b.from != 15 || b.to != 20 || len(b.entries) != 0 {
		t.Errorf("empty closing batch = %+v", b)
	}
	// ...unless that would be from == to.
	if b, _ := s.build(20, nil, false, DefaultMaxBytes); b != nil {
		t.Errorf("from == to batch built: %+v", b)
	}
	// A scan session never stretches.
	scan := session{agentID: "a", driveID: "d", streamID: "s", upper: 1 << 62}
	if b, _ := scan.build(10, []Entry{entry(12)}, false, DefaultMaxBytes); b.to != 12 {
		t.Errorf("scan batch to = %d", b.to)
	}
	if b, _ := scan.build(10, nil, false, DefaultMaxBytes); b != nil {
		t.Errorf("empty scan batch built")
	}

	// The byte limit cuts the page (keeping at least one entry), and then
	// the batch doesn't close the gap.
	one, _ := json.Marshal(entry(12).Change)
	b, _ = s.build(10, []Entry{entry(12), entry(13), entry(14)}, false, envelopeBytes+2*(len(one)+1))
	if len(b.entries) != 2 || b.to != 13 || b.json > envelopeBytes+2*(len(one)+1) {
		t.Errorf("byte-limited batch: %d entries, to %d, %d bytes", len(b.entries), b.to, b.json)
	}
	if b, _ := s.build(10, []Entry{entry(12), entry(13)}, false, 1); len(b.entries) != 1 {
		t.Errorf("tiny limit: %d entries", len(b.entries))
	}

	// The body is gzipped JSON of the batch; the key depends only on the
	// agent, drive, stream and range.
	b, _ = s.build(10, []Entry{entry(12)}, false, DefaultMaxBytes)
	zr, err := gzip.NewReader(bytes.NewReader(b.body))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(zr)
	var got wire.ChangeBatch
	if err := json.Unmarshal(raw, &got); err != nil || got.StreamID != "s" || got.FromVersion != 10 || got.ToVersion != 20 || len(got.Changes) != 1 {
		t.Errorf("body = %s (%v)", raw, err)
	}
	b2, _ := s.build(10, []Entry{entry(12)}, false, DefaultMaxBytes)
	if !bytes.Equal(b.body, b2.body) || b.key != b2.key || len(b.key) != 64 {
		t.Error("same batch, different bytes or key")
	}
	if b.key == s.key(10, 21) || b.key == (session{agentID: "a", driveID: "d", streamID: "t"}).key(10, 20) {
		t.Error("key doesn't depend on the range and stream")
	}
}

func TestMarkerRules(t *testing.T) {
	m := store.Marker{StreamID: "s", Watermark: 10, Ranges: []wire.Range{{20, 30}}}
	resp := func(rs ...wire.Range) wire.DriveOpenResponse { return wire.DriveOpenResponse{AckedRanges: rs} }
	cases := []struct {
		name  string
		clock int64
		resp  wire.DriveOpenResponse
		want  action
	}{
		{"same", 40, resp(wire.Range{0, 10}, wire.Range{20, 30}), keepMarker},
		{"server ahead within the clock", 40, resp(wire.Range{0, 30}), adoptServer},
		{"server beyond the clock: state.db restored", 25, resp(wire.Range{0, 10}, wire.Range{20, 30}), mintStream},
		{"server lost a range: server restored", 40, resp(wire.Range{0, 10}), mintStream},
		{"server lost part of the watermark", 40, resp(wire.Range{0, 8}, wire.Range{20, 30}), mintStream},
		{"reset", 40, wire.DriveOpenResponse{Reset: true}, clearMarker},
	}
	for _, c := range cases {
		if got, why := reconcile(m, c.clock, c.resp); got != c.want || (got == mintStream) != (why != "") {
			t.Errorf("%s: %v (%q), want %v", c.name, got, why, c.want)
		}
	}
	if a, _ := reconcile(store.Marker{StreamID: "s"}, 0, resp()); a != keepMarker {
		t.Errorf("new drive: %v", a)
	}

	acked := []wire.Range{{0, 10}, {20, 30}, {35, 40}}
	if g := gaps(acked, 50); !reflect.DeepEqual(g, []wire.Range{{10, 20}, {30, 35}, {40, 50}}) {
		t.Errorf("gaps = %v", g)
	}
	if g, ok := firstGap(acked, 50); !ok || g != (wire.Range{10, 20}) {
		t.Errorf("first gap = %v %v", g, ok)
	}
	if g, ok := firstGap([]wire.Range{{5, 9}}, 12); !ok || g != (wire.Range{0, 5}) {
		t.Errorf("gap below the first range = %v", g)
	}
	if _, ok := firstGap([]wire.Range{{0, 12}}, 12); ok {
		t.Error("gap found in a fully synced drive")
	}
	if g := gaps(nil, 0); g != nil {
		t.Errorf("empty feed has gaps %v", g)
	}

	if !covers([]wire.Range{{0, 30}}, wire.Range{0, 10}, wire.Range{20, 30}) || covers([]wire.Range{{0, 25}}, wire.Range{20, 30}) {
		t.Error("covers")
	}
	for _, bad := range [][]wire.Range{{{5, 5}}, {{-1, 3}}, {{0, 5}, {5, 8}}, {{6, 8}, {0, 5}}} {
		if checkRanges(bad) == nil {
			t.Errorf("checkRanges accepted %v", bad)
		}
	}
}

// A scan uploads only its own data (M6); here a session from S stands in
// for it, leaving history gaps below and above that sync closes.
func TestSyncClosesGaps(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.drive("other")
	e.files("d1", "a", "b")
	e.sync("d1") // watermark at b

	e.files("d1", "c", "d") // history, the last of it at S
	S := e.head()
	e.files("d1", "e", "f") // the "scan"'s data
	var res Result
	d, err := e.u.Open(ctx, "d1", &res)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.u.upload(ctx, d, S, 1<<62, false, &res); err != nil {
		t.Fatal(err)
	}
	f := e.head()
	if m := e.marker("d1"); len(m.Ranges) != 1 || m.Ranges[0] != (wire.Range{S, f}) {
		t.Fatalf("after the scan session: %+v", m)
	}
	e.files("other", "x") // the clock moves on; d1's last gap is empty

	n := e.posts()
	res = e.sync("d1")
	e.checkSynced("d1", res)
	batches := e.srv.Batches[n:]
	if len(batches) != 2 {
		t.Fatalf("sent %d batches, want 2: %+v", len(batches), batches)
	}
	// The first gap's last entry is at its upper end: one batch, no extra
	// empty one. The second gap has no entries: one empty batch.
	if b := batches[0]; b.ToVersion != S || len(b.Changes) != 2 {
		t.Errorf("first gap batch (%d, %d] with %d changes", b.FromVersion, b.ToVersion, len(b.Changes))
	}
	if b := batches[1]; b.FromVersion != f || b.ToVersion != e.head() || len(b.Changes) != 0 {
		t.Errorf("second gap batch (%d, %d] with %d changes", b.FromVersion, b.ToVersion, len(b.Changes))
	}

	// History superseded by the scan leaves an empty gap, closed the same way.
	e.files("d1", "g")
	S = e.head()
	e.files("d1", "g") // rehashed by the "scan": its history version is gone
	d, _ = e.u.Open(ctx, "d1", &res)
	if err := e.u.upload(ctx, d, S, 1<<62, false, &res); err != nil {
		t.Fatal(err)
	}
	n = len(e.srv.Batches)
	res = e.sync("d1")
	e.checkSynced("d1", res)
	if b := e.srv.Batches[n:]; len(b) != 1 || len(b[0].Changes) != 0 || b[0].ToVersion != S {
		t.Errorf("superseded gap: %+v", b)
	}
}

// Batches are at most the handshake's size, contiguous, and the last one
// ends at the clock.
func TestSyncPages(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b", "c", "d", "e", "f", "g")
	e.u.Limits = wire.Limits{MaxChangesPerBatch: 3, MaxBatchBytes: 1 << 20}
	var progress []int
	e.u.Progress = func(_ string, n int) { progress = append(progress, n) }
	res := e.sync("d1")
	e.checkSynced("d1", res)
	if len(e.srv.Batches) != 3 || !reflect.DeepEqual(progress, []int{3, 6, 7}) {
		t.Errorf("%d batches, progress %v", len(e.srv.Batches), progress)
	}
	prev := int64(0)
	for _, b := range e.srv.Batches {
		if b.FromVersion != prev || len(b.Changes) > 3 {
			t.Errorf("batch (%d, %d] with %d changes after %d", b.FromVersion, b.ToVersion, len(b.Changes), prev)
		}
		prev = b.ToVersion
	}
}

func TestRetrySendsIdenticalBytes(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b")
	e.srv.FailChanges = []int{503, 502, 504}
	var log bytes.Buffer
	e.u.Log = &log
	e.checkSynced("d1", e.sync("d1"))

	var bodies [][]byte
	var keys []string
	for _, r := range e.srv.Requests {
		if strings.HasSuffix(r.Path, "/changes") {
			bodies = append(bodies, r.Body)
			keys = append(keys, r.Header.Get(wire.HeaderIdempotencyKey))
		}
	}
	if len(bodies) != 4 {
		t.Fatalf("%d posts, want 4", len(bodies))
	}
	for i := range bodies[1:] {
		if !bytes.Equal(bodies[i+1], bodies[0]) || keys[i+1] != keys[0] {
			t.Errorf("retry %d differs from the first try", i+1)
		}
	}
	// Backoff: about 1, 2, 4 s (±25%).
	if s := e.clock.sleeps; len(s) != 3 || s[0] < 750*time.Millisecond || s[0] > 1250*time.Millisecond ||
		s[2] < 3*time.Second || s[2] > 5*time.Second {
		t.Errorf("sleeps = %v", s)
	}
	if strings.Count(log.String(), "remote: retrying") != 3 {
		t.Errorf("log = %q", log.String())
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a")
	e.srv.FailChanges = []int{429}
	// The fake doesn't send Retry-After; check call() directly.
	err := e.u.call(ctx, func(string) error {
		if len(e.clock.sleeps) == 0 {
			return &remote.Error{Kind: remote.ErrTransient, Status: 429, RetryAfter: 7 * time.Second}
		}
		return nil
	})
	if err != nil || len(e.clock.sleeps) != 1 || e.clock.sleeps[0] != 7*time.Second {
		t.Errorf("err %v, sleeps %v", err, e.clock.sleeps)
	}
}

func TestRemoteTimeout(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b")
	fail := make([]int, 100)
	for i := range fail {
		fail[i] = 503
	}
	e.srv.FailChanges = fail
	e.u.RemoteTimeout = 2 * time.Minute
	res, err := e.u.SyncDrive(ctx, "d1")
	if !errors.Is(err, remote.ErrTransient) || !strings.Contains(err.Error(), "remote unavailable for 2m0s") {
		t.Fatalf("err = %v", err)
	}
	if res.Pending != 2 || e.marker("d1").Watermark != 0 {
		t.Errorf("pending %d, marker %+v", res.Pending, e.marker("d1"))
	}
	var total time.Duration
	for _, s := range e.clock.sleeps {
		total += s
		if s > maxBackoff*5/4 {
			t.Errorf("slept %s, over the cap", s)
		}
	}
	if total != 2*time.Minute {
		t.Errorf("gave up after %s of sleeping", total)
	}
}

func TestLostResponse(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b")
	e.srv.DropChanges = 1
	e.checkSynced("d1", e.sync("d1"))
	// Applied once; the retry got the stored response.
	if n := e.posts(); n != 2 {
		t.Errorf("%d posts", n)
	}
}

// A run that dies between the server's commit and the local marker update
// resumes without re-sending: re-opening adopts the server's ranges.
func TestResumeAfterServerCommit(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b")
	e.srv.DropChanges = 1
	e.srv.AfterApply = func(s *remotetest.Server) {
		s.FailChanges = []int{503, 503, 503, 503, 503, 503, 503, 503, 503, 503}
		s.AfterApply = nil
	}
	e.u.RemoteTimeout = 5 * time.Second
	if _, err := e.u.SyncDrive(ctx, "d1"); !errors.Is(err, remote.ErrTransient) {
		t.Fatalf("err = %v", err)
	}
	if m := e.marker("d1"); m.Watermark != 0 {
		t.Fatalf("marker moved without an acknowledgement: %+v", m)
	}

	// A new process: re-opening adopts, nothing is re-sent.
	e.srv.FailChanges = nil
	n := e.posts()
	e.u = e.uploader()
	e.checkSynced("d1", e.sync("d1"))
	if e.posts() != n {
		t.Errorf("re-sent %d batches", e.posts()-n)
	}
}

// After a restart, a re-read page with an already-acked range is a
// duplicate, even with its idempotency record gone.
func TestRereadPageIsDuplicate(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b")
	var res Result
	d, err := e.u.Open(ctx, "d1", &res)
	if err != nil {
		t.Fatal(err)
	}
	e.checkSynced("d1", e.sync("d1"))
	e.srv.ForgetIdempotencyKeys()

	// d still has the marker from before the upload.
	res = Result{}
	if err := e.u.upload(ctx, d, 0, e.head(), true, &res); err != nil {
		t.Fatal(err)
	}
	if res.Uploaded != 0 {
		t.Errorf("duplicate counted as uploaded: %+v", res)
	}
	e.checkSynced("d1", Result{})
}

func Test413HalvesTheBatch(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b", "c", "d", "e", "f", "g", "h", "i", "j")
	e.srv.MaxChanges = 3
	var log bytes.Buffer
	e.u.Log = &log
	e.checkSynced("d1", e.sync("d1"))
	for _, b := range e.srv.Batches {
		if len(b.Changes) > 3 {
			t.Errorf("a batch of %d was accepted", len(b.Changes))
		}
	}
	if !strings.Contains(log.String(), "sending 2 changes per batch") {
		t.Errorf("log = %q", log.String())
	}

	// A batch of one that's still too large is a failure.
	e.files("d1", "k")
	e.srv.MaxChanges = -1
	e.srv.FailChanges = []int{413}
	if _, err := e.u.SyncDrive(ctx, "d1"); err == nil || !strings.Contains(err.Error(), "batch of one") {
		t.Errorf("err = %v", err)
	}
}

func TestExpiredTokenRefreshedOnce(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a")
	e.srv.ExpireAccessTokens()
	e.checkSynced("d1", e.sync("d1"))
	if e.srv.Refreshes != 1 {
		t.Errorf("%d refreshes", e.srv.Refreshes)
	}

	// Refused again after the refresh: log in again.
	e.files("d1", "b")
	e.srv.FailChanges = []int{401, 401}
	_, err := e.u.SyncDrive(ctx, "d1")
	if !errors.Is(err, remote.ErrAuth) || !strings.Contains(err.Error(), "driveagent login") {
		t.Errorf("err = %v", err)
	}
}

// 404 DRIVE_NOT_OPEN and 409 STREAM_MISMATCH re-open the drive.
func TestReopen(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a")
	e.srv.FailChanges = []int{404, 409}
	e.checkSynced("d1", e.sync("d1"))
	if got := strings.Count(strings.Join(e.srv.Paths(), " "), "/agent/v1/drives/d1 "); got != 3 {
		t.Errorf("opened the drive %d times, want 3", got)
	}

	// The server holds another stream for the drive (say a copy of this
	// state dir uploaded it): opening starts the stream over, and the drive
	// is re-uploaded.
	e.files("d1", "b")
	e.srv.Lock()
	e.srv.Drives["d1"].StreamID = "9b7ae2b4-5b1f-4c1e-9d3a-000000000009"
	e.srv.Unlock()
	res := e.sync("d1")
	e.checkSynced("d1", res)
	if res.Uploaded != 2 {
		t.Errorf("uploaded %d, want both files", res.Uploaded)
	}
}

// state.db restored from an older copy: the server has versions above the
// local clock, so the drive starts over on a new stream.
func TestStateDBRestored(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b", "c")
	e.sync("d1")
	old := filepath.Join(t.TempDir(), "old.db")
	e.copyStateDB(old)
	stream := e.marker("d1").StreamID

	e.del("d1", "a")
	e.files("d1", "d", "e", "f")
	e.sync("d1")

	e.restoreStateDB(old)
	e.u = e.uploader()
	res := e.sync("d1")
	if !strings.Contains(res.NewStream, "state.db was restored") || e.marker("d1").StreamID == stream {
		t.Errorf("no new stream: %+v", res)
	}
	e.checkSynced("d1", res)
	if _, ok := e.srv.Live("d1")["file\x00d\x00"]; ok {
		t.Error("the server kept a file the restored state.db doesn't have")
	}
}

// The server restored from an older copy: it lost coverage the marker has,
// including a deletion, so the drive starts over.
func TestServerRestored(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b", "c")
	e.sync("d1")
	snap := e.srv.Snapshot()

	e.del("d1", "a") // uploaded, then its tombstone is pruned locally
	e.files("d1", "d")
	e.sync("d1")

	e.srv.Restore(snap)
	res := e.sync("d1")
	if !strings.Contains(res.NewStream, "server was restored") {
		t.Errorf("no new stream: %+v", res)
	}
	e.checkSynced("d1", res)
	if _, ok := e.srv.Live("d1")["file\x00a\x00"]; ok {
		t.Error("the server still has the deleted file")
	}
}

// The server restored while a session runs: the next acknowledgement
// doesn't cover the marker, which is kept, and the drive starts over.
func TestServerRestoredMidSession(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b")
	e.sync("d1")
	snap := e.srv.Snapshot()

	e.files("d1", "c", "d", "e", "f", "g", "h")
	e.u.Limits.MaxChangesPerBatch = 2
	var log bytes.Buffer
	e.u.Log = &log
	e.srv.AfterApply = func(s *remotetest.Server) {
		// After the first batch of the session: its ack is recorded, then
		// the server goes back to the snapshot.
		s.AfterApply = func(s *remotetest.Server) {
			s.AfterApply = nil
			s.RestoreLocked(snap)
		}
	}
	res := e.sync("d1")
	// Had the marker copied the shrunken ranges, re-opening would have
	// matched them and not started over.
	if !strings.Contains(log.String(), "no longer cover") || !strings.Contains(res.NewStream, "server was restored") {
		t.Errorf("log = %q, result %+v", log.String(), res)
	}
	e.checkSynced("d1", res)
}

func TestRejectedEntries(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "good", "bad")
	e.srv.Reject = func(c wire.Change) string {
		if c.Path != nil && *c.Path == "bad" {
			return "mtime out of range"
		}
		return ""
	}
	res := e.sync("d1")
	if res.Rejected != 1 {
		t.Errorf("result = %+v", res)
	}
	rej, _ := e.st.Rejected("d1")
	if len(rej) != 1 || rej[0].Path != "bad" || rej[0].Reason != "mtime out of range" || rej[0].Kind != "file" {
		t.Fatalf("rejected = %+v", rej)
	}
	// Acked all the same; the server holds the key as missing.
	if m := e.marker("d1"); m.Watermark != e.head() {
		t.Errorf("marker = %+v", m)
	}
	if _, ok := e.srv.Live("d1")["file\x00bad\x00"]; ok {
		t.Error("server kept the rejected entry")
	}

	// A new stream clears the table; the same entry is rejected again
	// without colliding with its old row.
	e.srv.Restore(nil)
	res = e.sync("d1")
	if res.NewStream == "" || res.Rejected != 1 {
		t.Errorf("result = %+v", res)
	}
	if rej, _ := e.st.Rejected("d1"); len(rej) != 1 {
		t.Errorf("rejected = %+v", rej)
	}

	// Once a newer version of the key is stored, the row goes.
	e.srv.Reject = nil
	e.files("d1", "bad")
	e.checkSynced("d1", e.sync("d1"))
	if rej, _ := e.st.Rejected("d1"); len(rej) != 0 {
		t.Errorf("rejected = %+v", rej)
	}
}

// A concurrent --replace-root (ClearDrive) makes the uploader's next marker
// write fail; the re-open then starts a new stream.
func TestClearDriveDuringSync(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "a", "b", "c", "d")
	e.u.Limits.MaxChangesPerBatch = 2
	cleared := false
	e.u.Progress = func(string, int) {
		if !cleared {
			cleared = true
			if err := e.st.ClearDrive("d1"); err != nil {
				t.Error(err)
			}
			e.files("d1", "new")
		}
	}
	e.checkSynced("d1", e.sync("d1"))
	if got := e.srv.Live("d1"); len(got) != 1 {
		t.Errorf("server has %v, want only the new root's file", got)
	}
}

func TestUploadLock(t *testing.T) {
	dir := t.TempDir()
	l, ok, err := TryLock(dir, "seagate1")
	if err != nil || !ok {
		t.Fatalf("first lock: %v %v", ok, err)
	}
	if _, ok, _ := TryLock(dir, "seagate1"); ok {
		t.Error("second TryLock got the lock")
	}
	other, ok, _ := TryLock(dir, "seagate2")
	if !ok {
		t.Error("another drive's lock was busy")
	}
	other.Unlock()
	if p := LockPath(dir, "seagate1"); !strings.HasPrefix(filepath.Base(p), "upload-") || len(filepath.Base(p)) != len("upload-")+16+len(".lock") {
		t.Errorf("lock path = %s", p)
	}

	waited := make(chan struct{})
	got := make(chan error)
	go func() {
		l2, err := WaitLock(ctx, dir, "seagate1", func() { close(waited) })
		if err == nil {
			l2.Unlock()
		}
		got <- err
	}()
	<-waited
	l.Unlock()
	if err := <-got; err != nil {
		t.Errorf("WaitLock: %v", err)
	}

	l, _, _ = TryLock(dir, "seagate1")
	defer l.Unlock()
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := WaitLock(cctx, dir, "seagate1", nil); err == nil {
		t.Error("WaitLock returned without the lock")
	}
	if _, err := os.Stat(LockPath(dir, "seagate1")); err != nil {
		t.Error(err)
	}
}
