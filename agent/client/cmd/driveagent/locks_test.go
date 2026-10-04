package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/creds"
	"github.com/jyothri/bhandaar/agent/client/internal/runlock"
	"github.com/jyothri/bhandaar/agent/client/internal/testutil"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
	"github.com/jyothri/bhandaar/agent/wire"
)

// Tests for the instance and physical-drive locks and the machine binding
// (docs/archive/agent-hardening.md).

// oneDisk makes every drive root the same disk for the test.
func oneDisk(t *testing.T) {
	t.Helper()
	diskKeys = func(string) []string { return []string{"serial:NA77YET6"} }
	t.Cleanup(func() { diskKeys = pathDiskKey })
}

// holdDisk takes the disk's lock as another scan (of seagate1) would.
func holdDisk(t *testing.T) (release func()) {
	t.Helper()
	dir, err := runlock.Dir()
	if err != nil {
		t.Fatal(err)
	}
	release, err = runlock.LockDisks(context.Background(), dir, []string{"serial:NA77YET6"},
		runlock.DiskHolder{DriveID: "seagate1", Path: "/media/Seagate1/Jyo", PID: 4242, Started: time.Now()}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	return release
}

// A second scan of a disk another scan is reading exits 5, whatever its
// drive id, before it touches the state dir or the server.
func TestScanOfABusyDiskExits5(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "a"})
	srv := loggedIn(t, state)
	n := len(srv.Paths())
	oneDisk(t)
	holdDisk(t)

	r := scanCmd(t, state, srv.URL, "--drive-id", "boot", "--path", root)
	if r.code() != exitBusy {
		t.Fatalf("exit %d: %v", r.code(), r.err)
	}
	for _, want := range []string{"another scan is reading this disk (serial NA77YET6): drive seagate1, /media/Seagate1/Jyo (pid 4242", "re-run with --wait"} {
		if !strings.Contains(r.err.Error(), want) {
			t.Errorf("error %q lacks %q", r.err, want)
		}
	}
	// Only the health check and the handshake, which come before the disk
	// lock (they decide whether to update first): nothing authenticated.
	if got := strings.Join(srv.Paths()[n:], " "); got != "/agent/health /agent/v1/handshake" {
		t.Errorf("the refused scan sent %s", got)
	}
	if stateDBExists(state) {
		t.Error("the refused scan created state.db")
	}
}

// With --wait, the scan waits for the disk, then runs.
func TestScanWaitsForTheDisk(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "a"})
	srv := loggedIn(t, state)
	oneDisk(t)
	release := holdDisk(t)

	var out, errOut syncBuffer
	finished := make(chan error)
	go func() {
		finished <- runScan(context.Background(), context.Background(),
			[]string{"--state-dir", state, "--remote-url", srv.URL, "--drive-id", "boot", "--path", root, "--wait"}, &out, &errOut)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(errOut.String(), "waiting: another scan is reading this disk") {
		if time.Now().After(deadline) {
			t.Fatalf("no waiting message; stderr %q", errOut.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-finished:
		t.Fatalf("scan finished while the disk was busy: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	release()
	if err := <-finished; err != nil {
		t.Fatalf("scan: %v\n%s", err, errOut.String())
	}
	if len(srv.Live("boot")) == 0 {
		t.Error("nothing uploaded")
	}
}

// A scan on a filesystem whose disk can't be found says what's kept apart.
func TestScanWithoutADiskNotes(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "a"})
	srv := loggedIn(t, state)
	r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root)
	if r.err != nil {
		t.Fatal(r.err)
	}
	if want := "note: couldn't find the disk holding " + root + "; only other scans of the same path " + root + " are kept out"; !strings.Contains(r.stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, r.stderr)
	}
}

// joinAs holds the instance lock as another driveagent would.
func joinAs(t *testing.T, stateDir, ver string) {
	t.Helper()
	dir, err := runlock.Dir()
	if err != nil {
		t.Fatal(err)
	}
	release, err := runlock.JoinInstance(context.Background(), dir, stateDir, ver, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
}

// While a driveagent runs with one state dir, commands with another exit 5,
// and commands with the same one run.
func TestAnotherStateDirExits5(t *testing.T) {
	state, other := t.TempDir(), t.TempDir()
	srv := loggedIn(t, state)
	joinAs(t, other, version.Version)

	r := syncCmd(t, context.Background(), state, srv.URL)
	if r.code() != exitBusy || !strings.Contains(r.err.Error(), "with state dir "+other) {
		t.Fatalf("sync: exit %d: %v", r.code(), r.err)
	}
	if r := status(t, state, srv.URL); r.code() != exitBusy {
		t.Errorf("remote-status: exit %d: %v", r.code(), r.err)
	}
	if r := login(t, state, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin"); r.code() != exitBusy {
		t.Errorf("login: exit %d: %v", r.code(), r.err)
	}
	if err := runReport([]string{"--state-dir", state, "--drives", "d1", "--type", "text"}); exitCode(context.Background(), err) != exitBusy {
		t.Errorf("report: %v", err)
	}
	if r := syncCmd(t, context.Background(), other, srv.URL); r.code() == exitBusy {
		t.Errorf("sync with the running state dir: %v", r.err)
	}
}

// While one version runs, another exits 5.
func TestAnotherVersionExits5(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	joinAs(t, state, "0.0.1")
	r := syncCmd(t, context.Background(), state, srv.URL)
	if r.code() != exitBusy || !strings.Contains(r.err.Error(), "driveagent 0.0.1 is running on this machine") {
		t.Fatalf("exit %d: %v", r.code(), r.err)
	}
}

// onMachine makes creds.MachineID return id for the test.
func onMachine(t *testing.T, id string) {
	t.Helper()
	old := creds.MachineID
	creds.MachineID = func() string { return id }
	t.Cleanup(func() { creds.MachineID = old })
}

// A state dir copied to another machine: remote commands refuse, and
// "login --new-agent" makes it a new agent that uploads everything again.
func TestStateDirOnAnotherMachine(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "a", "b/c": "c"})
	onMachine(t, "linux-box")
	srv := loggedIn(t, state)
	if r := scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root); r.err != nil {
		t.Fatal(r.err)
	}
	oldAgent, _ := creds.AgentID(state)
	oldStream := srv.Batches[0].StreamID
	n := len(srv.Paths())

	onMachine(t, "the-mac")
	for name, r := range map[string]result{
		"sync":          syncCmd(t, context.Background(), state, srv.URL),
		"scan":          scanCmd(t, state, srv.URL, "--drive-id", "d1", "--path", root),
		"remote-status": status(t, state, srv.URL),
		"logout":        logoutCmd(t, state),
	} {
		if r.err == nil || !strings.Contains(r.err.Error(), "this state dir belongs to another machine") || r.code() != exitLocal {
			t.Errorf("%s: exit %d: %v", name, r.code(), r.err)
		}
	}
	if got := len(srv.Paths()); got != n {
		t.Errorf("the refused commands sent %d requests", got-n)
	}
	if c, _ := creds.Load(state); c == nil {
		t.Error("the refused logout forgot the login")
	}

	r := login(t, state, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin", "--new-agent")
	if r.err != nil {
		t.Fatalf("login --new-agent: %v\n%s", r.err, r.stderr)
	}
	newAgent, err := creds.AgentID(state)
	if err != nil || newAgent == oldAgent || !strings.Contains(r.stdout, "this machine is now agent "+newAgent) {
		t.Fatalf("agent %q (was %q), %v\n%s", newAgent, oldAgent, err, r.stdout)
	}
	if req, _ := srv.Last("/agent/v1/auth/login"); req.Header.Get(wire.HeaderAgentID) != newAgent {
		t.Errorf("logged in as agent %q", req.Header.Get(wire.HeaderAgentID))
	}

	// sync uploads the whole drive again, on a new stream, as the new agent.
	m := len(srv.Batches)
	if r := syncCmd(t, context.Background(), state, srv.URL); r.err != nil {
		t.Fatalf("sync: %v\n%s", r.err, r.stdout)
	}
	if len(srv.Batches) == m {
		t.Fatal("sync uploaded nothing")
	}
	for _, b := range srv.Batches[m:] {
		if b.StreamID == oldStream {
			t.Error("a batch went to the old agent's stream")
		}
	}
	if got, want := srv.Live("d1"), localLive(t, state, "d1"); !reflect.DeepEqual(got, want) {
		t.Errorf("server %v\nlocal %v", got, want)
	}
}

// login --new-agent needs the state dir to itself.
func TestNewAgentRunsAlone(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	joinAs(t, state, version.Version) // a scan, say
	r := login(t, state, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin", "--new-agent")
	if r.code() != exitBusy || !strings.Contains(r.err.Error(), "to itself") {
		t.Fatalf("exit %d: %v", r.code(), r.err)
	}
}

func logoutCmd(t *testing.T, stateDir string) result {
	t.Helper()
	var out, errOut syncBuffer
	err := runLogout(context.Background(), []string{"--state-dir", stateDir}, &out, &errOut)
	return result{err, out.String(), errOut.String()}
}

// The real CLI: a second state dir exits 5, and "version" runs regardless.
func TestBusyExitCodeFromTheCLI(t *testing.T) {
	joinAs(t, t.TempDir(), version.Version)
	cmd := driveagent(t, "report", "--drives", "d1", "--state-dir", t.TempDir())
	out, err := cmd.CombinedOutput()
	if code := exitStatus(t, err); code != exitBusy || !strings.Contains(string(out), "another driveagent is running on this machine") {
		t.Errorf("report: exit %d\n%s", code, out)
	}
	cmd = driveagent(t, "version")
	if out, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(out), version.Version) {
		t.Errorf("version: %v\n%s", err, out)
	}
}
