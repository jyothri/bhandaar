package syncer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/remote/remotetest"
)

// stream runs a scan session from S's rule while write writes the scan's
// data, and returns once it has drained.
func (e *env) stream(drive string, write func(wake chan<- struct{})) (Result, error) {
	e.t.Helper()
	S := e.head()
	var res Result
	d, err := e.u.Open(ctx, drive, &res)
	if err != nil {
		e.t.Fatal(err)
	}
	from, err := e.u.ScanFrom(ctx, d, S)
	if err != nil {
		e.t.Fatal(err)
	}
	wake, done := make(chan struct{}, 1), make(chan struct{})
	errc := make(chan error, 1)
	go func() { errc <- e.u.Stream(ctx, d, from, wake, done, &res) }()
	write(wake)
	close(done)
	return res, <-errc
}

func TestScanFrom(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.drive("other")
	e.files("d1", "a")
	e.sync("d1") // watermark at a
	var res Result
	d, _ := e.u.Open(ctx, "d1", &res)

	// Nothing of d1 since: continue the watermark, even past other drives' writes.
	e.files("other", "x")
	if from, _ := e.u.ScanFrom(ctx, d, e.head()); from != d.Marker.Watermark {
		t.Errorf("from = %d, want the watermark %d", from, d.Marker.Watermark)
	}
	// History of d1 above it: start at S.
	e.files("d1", "b")
	if from, _ := e.u.ScanFrom(ctx, d, e.head()); from != e.head() {
		t.Errorf("from = %d, want S = %d", from, e.head())
	}
	// S at the watermark itself.
	if from, _ := e.u.ScanFrom(ctx, d, d.Marker.Watermark); from != d.Marker.Watermark {
		t.Errorf("from = %d", from)
	}
}

// A scan's session uploads what the scan writes as it goes, and the rest
// when done; history below S stays pending, except what the scan supersedes.
func TestStream(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	e.files("d1", "hist1", "hist2")
	res, err := e.stream("d1", func(wake chan<- struct{}) {
		e.files("d1", "s1", "s2")
		wake <- struct{}{}
		deadline := time.Now().Add(5 * time.Second)
		for len(e.srv.Live("d1")) < 2 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if len(e.srv.Live("d1")) < 2 {
			t.Error("nothing uploaded during the scan")
		}
		e.files("d1", "hist2") // superseded
		e.del("d1", "s1")
	})
	if err != nil {
		t.Fatal(err)
	}
	live, local := e.srv.Live("d1"), e.local("d1")
	for _, k := range []string{"file\x00s2\x00", "file\x00hist2\x00"} {
		if live[k] == 0 || live[k] != local[k] {
			t.Errorf("%q: server v%d, local v%d", k, live[k], local[k])
		}
	}
	if _, ok := live["file\x00hist1\x00"]; ok {
		t.Error("history uploaded by the scan")
	}
	if _, ok := live["file\x00s1\x00"]; ok {
		t.Error("the deletion didn't reach the server")
	}
	if res.Uploaded != 4 || e.u.Status().Uploaded != 4 {
		t.Errorf("uploaded %d (status %d)", res.Uploaded, e.u.Status().Uploaded)
	}
	// sync then closes the history gap.
	e.checkSynced("d1", e.sync("d1"))
}

// 409 mid-session re-opens and carries on; a server that started the
// stream over (reset on re-opening) gets the session again from its from.
func TestStreamReopens(t *testing.T) {
	e := newEnv(t)
	e.drive("d1")
	_, err := e.stream("d1", func(chan<- struct{}) {
		e.files("d1", "a", "b")
		e.srv.Lock()
		e.srv.FailChanges = []int{409}
		e.srv.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	e.checkSynced("d1", Result{})

	e.u.Limits.MaxChangesPerBatch = 1
	res, err := e.stream("d1", func(chan<- struct{}) {
		e.files("d1", "c", "d", "f")
		// After the first batch, the server loses the drive's stream.
		e.srv.Lock()
		e.srv.AfterApply = func(s *remotetest.Server) {
			s.AfterApply = nil
			s.Drives["d1"].StreamID = "9b7ae2b4-5b1f-4c1e-9d3a-000000000009"
		}
		e.srv.Unlock()
	})
	if err != nil || !res.Drive.Reset {
		t.Fatalf("err %v, result %+v", err, res)
	}
	// The scan's own rows are all there, on the new stream; history (a, b)
	// waits for sync.
	live := e.srv.Live("d1")
	for _, p := range []string{"c", "d", "f"} {
		if _, ok := live["file\x00"+p+"\x00"]; !ok {
			t.Errorf("%s missing after the new stream", p)
		}
	}
	e.checkSynced("d1", e.sync("d1"))
}

// Idle time between requests doesn't count against --remote-timeout; only
// a run of failures does.
func TestBudgetCountsOnlyFailures(t *testing.T) {
	e := newEnv(t)
	e.u.RemoteTimeout = 10 * time.Second
	ok := func(string) error { return nil }
	fail := func(string) error { return &remote.Error{Kind: remote.ErrTransient, Status: 503} }
	if err := e.u.call(ctx, ok); err != nil {
		t.Fatal(err)
	}
	e.clock.sleep(ctx, time.Hour) // idle
	calls := 0
	err := e.u.call(ctx, func(tok string) error {
		if calls++; calls < 4 {
			if calls == 2 && e.u.Status().RetryLeft <= 0 {
				t.Error("no retry time shown while failing")
			}
			return fail(tok)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("an idle hour used up the budget: %v", err)
	}
	if e.u.Status().RetryLeft != 0 {
		t.Error("still retrying after a success")
	}
	err = e.u.call(ctx, fail)
	if !errors.Is(err, remote.ErrTransient) || !strings.Contains(err.Error(), "remote unavailable for 10s") {
		t.Errorf("err = %v", err)
	}
}
