package syncer

import (
	"context"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/creds"
	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/remote/remotetest"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

var ctx = context.Background()

// env is a state dir, logged in to a fake agentserver, with an uploader.
type env struct {
	t      *testing.T
	dir    string
	srv    *remotetest.Server
	st     *store.Store
	feed   *Feed
	client *remote.Client
	u      *Uploader
	clock  *fakeClock
}

// fakeClock stands in for time: sleeping advances it.
type fakeClock struct {
	mu     sync.Mutex
	t      time.Time
	sleeps []time.Duration
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	c.sleeps = append(c.sleeps, d)
	return ctx.Err()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{t: t, dir: t.TempDir(), srv: remotetest.New(t), clock: &fakeClock{t: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)}}
	e.open()
	agentID, err := creds.AgentID(e.dir)
	if err != nil {
		t.Fatal(err)
	}
	if e.client, err = remote.New(remote.Options{RemoteURL: e.srv.URL, AgentID: agentID}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.Handshake(ctx); err != nil {
		t.Fatal(err)
	}
	tr, err := e.client.Login(ctx, "jyothri", "correct horse battery", "testhost")
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.Save(e.dir, creds.FromTokens(e.client.BaseURL(), tr, time.Now())); err != nil {
		t.Fatal(err)
	}
	e.u = e.uploader()
	return e
}

// uploader is a fresh Uploader, as a new process would have.
func (e *env) uploader() *Uploader {
	agentID, _ := creds.AgentID(e.dir)
	return &Uploader{
		Store: e.st, Feed: e.feed, Client: e.client, AgentID: agentID,
		Tokens:        &creds.Session{StateDir: e.dir, RemoteURL: e.client.BaseURL(), Refresh: e.client.Refresh},
		Limits:        wire.Limits{MaxChangesPerBatch: 1000, MaxBatchBytes: 1 << 20},
		RemoteTimeout: time.Minute,
		now:           e.clock.now, sleep: e.clock.sleep,
	}
}

// open opens the store and the feed reader.
func (e *env) open() {
	e.t.Helper()
	var err error
	if e.st, err = store.OpenWith(e.dir, store.Options{Log: io.Discard}); err != nil {
		e.t.Fatal(err)
	}
	if e.feed, err = OpenFeed(e.dir); err != nil {
		e.t.Fatal(err)
	}
	st, feed := e.st, e.feed
	e.t.Cleanup(func() { feed.Close(); st.Close() })
	if e.u != nil {
		e.u.Store, e.u.Feed = e.st, e.feed
	}
}

func (e *env) close() {
	e.feed.Close()
	e.st.Close()
}

// copyStateDB copies state.db (closed, so the WAL is checkpointed into it).
func (e *env) copyStateDB(to string) {
	e.t.Helper()
	e.close()
	b, err := os.ReadFile(filepath.Join(e.dir, "state.db"))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(to, b, 0o600); err != nil {
		e.t.Fatal(err)
	}
	e.open()
}

// restoreStateDB puts back a copy made by copyStateDB.
func (e *env) restoreStateDB(from string) {
	e.t.Helper()
	e.close()
	b, err := os.ReadFile(from)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(filepath.Join(e.dir, "state.db"+suffix))
	}
	if err := os.WriteFile(filepath.Join(e.dir, "state.db"), b, 0o600); err != nil {
		e.t.Fatal(err)
	}
	e.open()
}

func (e *env) drive(id string) {
	e.t.Helper()
	if err := e.st.UpsertDrive(id, "/mnt/"+id, ""); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) files(drive string, paths ...string) {
	e.t.Helper()
	var recs []store.FileRecord
	for _, p := range paths {
		recs = append(recs, store.FileRecord{DriveID: drive, RelPath: p, Size: int64(len(p)), MTimeUnix: 1726000000, Mode: 0o644,
			ContentHash: "h-" + p, HashAlgo: "blake3", MD5: "m-" + p, Status: store.StatusHashed, ScannedAt: time.Date(2026, 9, 24, 9, 58, 1, 0, time.UTC)})
	}
	if err := e.st.UpsertFiles(ctx, recs); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) del(drive string, paths ...string) {
	e.t.Helper()
	if err := e.st.DeleteFiles(ctx, drive, paths); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) dirs(drive, parent string, children ...string) {
	e.t.Helper()
	var cs []store.DirChild
	for _, c := range children {
		cs = append(cs, store.DirChild{Name: c})
	}
	if err := e.st.SyncDirListings(ctx, drive, map[string][]store.DirChild{parent: cs}, true); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) run(drive string) {
	e.t.Helper()
	id, err := e.st.StartScanRun(drive)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.FinishScanRun(id, drive, 3, 100, false); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) head() int64 {
	e.t.Helper()
	v, err := e.st.Clock()
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func (e *env) marker(drive string) store.Marker {
	e.t.Helper()
	d, err := e.st.SyncDrive(drive)
	if err != nil {
		e.t.Fatal(err)
	}
	return d.Marker
}

// local is the drive's live feed entries' versions, keyed like the fake
// server's rows.
func (e *env) local(drive string) map[string]int64 {
	e.t.Helper()
	page, _, err := e.feed.Page(ctx, drive, 0, math.MaxInt64, 1<<30)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]int64{}
	for _, en := range page {
		if en.Change.Op == wire.OpUpsert {
			out[remotetest.Key(en.Change)] = en.Change.V
		}
	}
	return out
}

func (e *env) sync(drive string) Result {
	e.t.Helper()
	res, err := e.u.SyncDrive(ctx, drive)
	if err != nil {
		e.t.Fatalf("sync %s: %v", drive, err)
	}
	return res
}

// checkSynced checks the server has exactly the drive's live entries, and
// the marker is one watermark at the clock with nothing pending.
func (e *env) checkSynced(drive string, res Result) {
	e.t.Helper()
	if got, want := e.srv.Live(drive), e.local(drive); !reflect.DeepEqual(got, want) {
		e.t.Errorf("server has %v\nlocal has %v", got, want)
	}
	m := e.marker(drive)
	if m.Watermark != e.head() || len(m.Ranges) != 0 {
		e.t.Errorf("marker = %+v, clock %d", m, e.head())
	}
	if res.Pending != 0 {
		e.t.Errorf("pending = %d", res.Pending)
	}
	if got := e.srv.DriveRanges(drive); !reflect.DeepEqual(got, m.AckedRanges()) {
		e.t.Errorf("server ranges %v, marker %v", got, m.AckedRanges())
	}
}

// posts counts the change batches the server received for the drive.
func (e *env) posts() int {
	n := 0
	for _, p := range e.srv.Paths() {
		if filepath.Base(p) == "changes" {
			n++
		}
	}
	return n
}
