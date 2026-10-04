package main

import (
	"bytes"
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/remote/remotetest"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/client/internal/syncer"
	"github.com/jyothri/bhandaar/agent/client/internal/testutil"
	"github.com/jyothri/bhandaar/agent/wire"
)

// Tests for uploading during scan (M6).

func scanCmd(t *testing.T, state, url string, args ...string) result {
	t.Helper()
	var out, errOut syncBuffer
	args = append([]string{"--state-dir", state, "--remote-url", url}, args...)
	err := runScan(context.Background(), context.Background(), args, &out, &errOut)
	return result{err, out.String(), errOut.String()}
}

// syncBuffer is a bytes.Buffer safe to read while another goroutine writes.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// localLive is the drive's live feed entries in state.db, keyed like the
// fake server's rows.
func localLive(t *testing.T, state, drive string) map[string]int64 {
	t.Helper()
	feed, err := syncer.OpenFeed(state)
	if err != nil {
		t.Fatal(err)
	}
	defer feed.Close()
	page, _, err := feed.Page(context.Background(), drive, 0, math.MaxInt64, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, e := range page {
		if e.Change.Op == wire.OpUpsert {
			out[remotetest.Key(e.Change)] = e.Change.V
		}
	}
	return out
}

func fileKey(p string) string { return "file\x00" + p + "\x00" }

// A scan uploads only what it writes. History (here, what an earlier scan
// failed to upload) is left for sync, except rows the scan supersedes.
func TestScanUploadsOnlyItsData(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"old1": "1", "old2": "2", "keep": "k"})
	srv := loggedIn(t, state)

	// A scan whose upload fails: its rows become history.
	srv.FailChanges = []int{400}
	r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root)
	if r.code() != exitRemote || !strings.Contains(r.err.Error(), `run "driveagent sync"`) {
		t.Fatalf("failed upload: code %d, %v", r.code(), r.err)
	}
	if len(srv.Live("d1")) != 0 {
		t.Fatalf("server has %v", srv.Live("d1"))
	}

	// The next scan: one file changed, one added.
	testutil.WriteFile(t, filepath.Join(root, "old1"), "changed", testutil.BaseTime.Add(time.Hour))
	testutil.WriteFile(t, filepath.Join(root, "new"), "n", testutil.BaseTime)
	r = scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root)
	if r.err != nil {
		t.Fatalf("scan: %v\n%s%s", r.err, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, `1 drive(s) have history not yet uploaded; run "driveagent sync"`) {
		t.Errorf("no history hint:\n%s", r.stdout)
	}
	live, local := srv.Live("d1"), localLive(t, state, "d1")
	for _, p := range []string{"old1", "new"} {
		if live[fileKey(p)] == 0 || live[fileKey(p)] != local[fileKey(p)] {
			t.Errorf("%s: server v%d, local v%d", p, live[fileKey(p)], local[fileKey(p)])
		}
	}
	for _, p := range []string{"old2", "keep"} {
		if _, ok := live[fileKey(p)]; ok {
			t.Errorf("history %s was uploaded by the scan", p)
		}
	}
	// The scan's data is a range above the history gap.
	if rs := srv.DriveRanges("d1"); len(rs) != 1 || rs[0][0] == 0 {
		t.Errorf("ranges after the scan: %v", rs)
	}

	// sync fills the gap; the drive is then one watermark.
	if r := syncCmd(t, context.Background(), state, srv.URL); r.err != nil {
		t.Fatalf("sync: %v", r.err)
	}
	if got, want := srv.Live("d1"), localLive(t, state, "d1"); !reflect.DeepEqual(got, want) {
		t.Errorf("after sync, server %v\nlocal %v", got, want)
	}

	// With no history pending, a scan continues the watermark: no new range.
	testutil.WriteFile(t, filepath.Join(root, "newer"), "nn", testutil.BaseTime)
	if r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root); r.err != nil || strings.Contains(r.stdout, "history") {
		t.Fatalf("scan: %v\n%s", r.err, r.stdout)
	}
	if rs := srv.DriveRanges("d1"); len(rs) != 1 || rs[0][0] != 0 {
		t.Errorf("ranges = %v, want one watermark", rs)
	}
	if got, want := srv.Live("d1"), localLive(t, state, "d1"); !reflect.DeepEqual(got, want) {
		t.Errorf("server %v\nlocal %v", got, want)
	}
}

// With the remote down, or no login, scan exits 3 before touching state.db.
func TestScanNeedsTheRemote(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "a"})

	state := t.TempDir()
	srv := loggedIn(t, state)
	srv.Down = true
	r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root)
	if r.code() != exitRemote || !strings.Contains(r.err.Error(), "not reachable") {
		t.Errorf("remote down: code %d, %v", r.code(), r.err)
	}
	if _, err := os.Stat(filepath.Join(state, "state.db")); err == nil {
		t.Error("state.db created with the remote down")
	}

	state = t.TempDir()
	srv = remotetest.New(t)
	r = scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root)
	if r.code() != exitRemote || !strings.Contains(r.err.Error(), `not logged in: run "driveagent login"`) {
		t.Errorf("not logged in: code %d, %v", r.code(), r.err)
	}
	if _, err := os.Stat(filepath.Join(state, "state.db")); err == nil {
		t.Error("state.db created without a login")
	}

	srv.Decision = wire.DecisionUpgradeRequired
	if r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root); r.code() != exitUpgrade {
		t.Errorf("upgrade required: code %d, %v", r.code(), r.err)
	}
}

// The remote dies mid-scan: after --remote-timeout the scan stops with
// exit 3, its checkpoint intact, and sync uploads what it recorded.
func TestScanStopsWhenTheRemoteDies(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	fail := make([]int, 1000)
	for i := range fail {
		fail[i] = 503
	}
	srv.FailChanges = fail
	root := bigTree(t)
	start := time.Now()
	r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root, "--workers", "1", "--remote-timeout", "2s")
	if r.code() != exitRemote || !strings.Contains(r.err.Error(), "remote unavailable for 2s") ||
		!strings.Contains(r.err.Error(), "scan stopped. Local checkpoint is intact") {
		t.Fatalf("code %d, %v\n%s", r.code(), r.err, r.stdout)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("took %s", took)
	}
	st, _ := store.OpenWith(state, store.Options{Log: io.Discard})
	d, _ := st.SyncDrive("d1")
	st.Close()
	if d.Marker.Watermark != 0 || len(d.Marker.Ranges) != 0 {
		t.Errorf("marker moved: %+v", d.Marker)
	}

	srv.Lock()
	srv.FailChanges = nil
	srv.Unlock()
	if r := syncCmd(t, context.Background(), state, srv.URL); r.err != nil {
		t.Fatalf("sync: %v", r.err)
	}
	if got, want := srv.Live("d1"), localLive(t, state, "d1"); !reflect.DeepEqual(got, want) || len(got) == 0 {
		t.Errorf("after sync, server %v\nlocal %v", got, want)
	}
}

func TestScanUpgradeRequiredMidScanExits4(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "a"})
	srv := loggedIn(t, state)
	srv.FailChanges = []int{426}
	r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root)
	if r.code() != exitUpgrade {
		t.Errorf("code %d, %v", r.code(), r.err)
	}
}

// The first Ctrl-C drains the upload, then exits 130.
func TestCtrlCDrainsTheUpload(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	code, out, errOut := interruptWith(t, srv, state, os.Interrupt)
	if code != exitSIGINT || !strings.Contains(out, "uploaded ") {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	// The interrupted scan run reached the server.
	if got, want := srv.Live("d1"), localLive(t, state, "d1"); !reflect.DeepEqual(got, want) {
		t.Errorf("server %v\nlocal %v", got, want)
	}
	if _, ok := srv.Live("d1")["scan_run\x001"]; !ok {
		t.Error("the scan run wasn't uploaded")
	}
}

// A second Ctrl-C aborts a drain that's stuck retrying, with the same exit code.
func TestSecondCtrlCAbortsTheDrain(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	fail := make([]int, 1000)
	for i := range fail {
		fail[i] = 503
	}
	srv.FailChanges = fail
	code, out, errOut := interruptWith(t, srv, state, os.Interrupt, os.Interrupt)
	if code != exitSIGINT || !strings.Contains(out, `run "driveagent sync"`) {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
}

func TestSIGTERMDrainsAndExits143(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	if code, out, errOut := interruptWith(t, srv, state, syscall.SIGTERM); code != exitSIGTERM {
		t.Errorf("exit %d\n%s%s", code, out, errOut)
	}
}

// A scan waits for the drive's upload lock before its preflight.
func TestScanWaitsForTheUploadLock(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "a"})
	srv := loggedIn(t, state)
	n := len(srv.Paths())

	lock, ok, err := syncer.TryLock(state, "d1") // a sync of d1
	if err != nil || !ok {
		t.Fatal(err)
	}
	var out, errOut syncBuffer
	finished := make(chan error)
	go func() {
		finished <- runScan(context.Background(), context.Background(),
			[]string{"--state-dir", state, "--remote-url", srv.URL, "--drive-id", "d1", "--path", root}, &out, &errOut)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(errOut.String(), `waiting for "driveagent sync" to finish uploading d1`) {
		if time.Now().After(deadline) {
			t.Fatalf("no waiting message; stderr %q", errOut.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	// Only the health check and the handshake, which come before the
	// locks: nothing authenticated while it waits.
	if got := strings.Join(srv.Paths()[n:], " "); got != "/agent/health /agent/v1/handshake" {
		t.Errorf("the waiting scan sent %s", got)
	}
	select {
	case err := <-finished:
		t.Fatalf("scan finished while the lock was held: %v", err)
	default:
	}
	lock.Unlock()
	if err := <-finished; err != nil {
		t.Fatalf("scan: %v\n%s", err, errOut.String())
	}
	if len(srv.Live("d1")) == 0 {
		t.Error("nothing uploaded")
	}
}

// --replace-root starts a new stream before the first upload: the server
// ends with none of the old root's files.
func TestReplaceRootStartsANewStream(t *testing.T) {
	rootA, rootB, state := t.TempDir(), t.TempDir(), t.TempDir()
	testutil.WriteTree(t, rootA, testutil.Tree{"a1": "1", "a2": "2"})
	testutil.WriteTree(t, rootB, testutil.Tree{"b1": "1"})
	srv := loggedIn(t, state)
	if r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", rootA); r.err != nil {
		t.Fatal(r.err)
	}
	oldStream := srv.Batches[0].StreamID
	n := len(srv.Batches)

	if r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", rootB, "--replace-root"); r.err != nil {
		t.Fatalf("%v\n%s", r.err, r.stdout)
	}
	for _, b := range srv.Batches[n:] {
		if b.StreamID == oldStream {
			t.Error("a batch of the new root went to the old stream")
		}
	}
	if got, want := srv.Live("d1"), localLive(t, state, "d1"); !reflect.DeepEqual(got, want) {
		t.Errorf("server %v\nlocal %v", got, want)
	}
	if _, ok := srv.Live("d1")[fileKey("a1")]; ok {
		t.Error("the server kept the old root's files")
	}
}

// Two scans of different drives, sharing a state dir, both upload
// everything they write.
func TestTwoScansAtOnce(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	roots := map[string]string{"d1": t.TempDir(), "d2": t.TempDir()}
	for _, root := range roots {
		tree := testutil.Tree{}
		for i := 0; i < 300; i++ {
			tree["dir"+string(rune('a'+i%5))+"/f"+strings.Repeat("x", i%7)+string(rune('0'+i%10))+"-"+time.Duration(i).String()] = "content"
		}
		testutil.WriteTree(t, root, tree)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for id, root := range roots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, errOut syncBuffer
			errs <- runScan(context.Background(), context.Background(),
				[]string{"--state-dir", state, "--remote-url", srv.URL, "--drive-id", id, "--path", root}, &out, &errOut)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for id := range roots {
		if got, want := srv.Live(id), localLive(t, state, id); !reflect.DeepEqual(got, want) || len(got) < 300 {
			t.Errorf("%s: server has %d rows, local %d", id, len(got), len(want))
		}
	}
}
