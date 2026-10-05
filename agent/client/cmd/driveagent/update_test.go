package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/runlock"
	"github.com/jyothri/bhandaar/agent/client/internal/testutil"
	"github.com/jyothri/bhandaar/agent/client/internal/update"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
	"github.com/jyothri/bhandaar/agent/wire"
	"golang.org/x/sys/unix"
)

// Tests for driveagent updating itself (docs/specs/agent-auto-update.md).

// fakeRelease is a release server, a test key, an installed driveagent in
// a temp dir, and a recorder in place of the re-exec.
type fakeRelease struct {
	exe     string
	mu      sync.Mutex
	fetches int
	execs   [][]string // argv
	envs    [][]string
}

// releaseScript is a stand-in driveagent that reports v.
func releaseScript(v string) []byte {
	return []byte("#!/bin/sh\necho \"driveagent " + v + " (test), protocols [1]\"\n")
}

// addRelease adds release v, holding bin and signed with priv, to files,
// keyed as the release server serves them ("<V>/<name>").
func addRelease(files map[string][]byte, priv ed25519.PrivateKey, v string, bin []byte) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "driveagent", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg})
	tw.Write(bin)
	tw.Close()
	gz.Close()
	asset := update.AssetName(runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(buf.Bytes())
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	files[v+"/"+asset] = buf.Bytes()
	files[v+"/SHA256SUMS"] = sums
	files[v+"/SHA256SUMS.sig"] = ed25519.Sign(priv, update.Message(v, sums))
}

func newFakeRelease(t *testing.T, versions ...string) *fakeRelease {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	files := map[string][]byte{}
	for _, v := range versions {
		addRelease(files, priv, v, releaseScript(v))
	}
	f := &fakeRelease{exe: filepath.Join(t.TempDir(), "driveagent")}
	os.WriteFile(f.exe, releaseScript(version.Version), 0o755)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.fetches++
		f.mu.Unlock()
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/driveagent/v")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)

	oldSource, oldExe, oldRelease, oldExec, oldLine := updateSource, executable, releaseBuild, reexec, commandLine
	t.Cleanup(func() {
		updateSource, executable, releaseBuild, reexec, commandLine = oldSource, oldExe, oldRelease, oldExec, oldLine
	})
	updateSource = func() update.Source {
		return update.Source{BaseURL: srv.URL, Client: srv.Client(), Keys: []ed25519.PublicKey{pub}, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	}
	executable = func() (string, error) { return f.exe, nil }
	releaseBuild = func() bool { return true }
	reexec = func(path string, argv, env []string) error {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.execs = append(f.execs, argv)
		f.envs = append(f.envs, env)
		return nil
	}
	t.Setenv(update.EnvNoAutoUpdate, "")
	t.Setenv(update.EnvUpdatedFrom, "")
	os.Unsetenv(update.EnvNoAutoUpdate)
	os.Unsetenv(update.EnvUpdatedFrom)
	t.Cleanup(func() {
		if dir, err := runlock.Dir(); err == nil {
			update.ClearFailure(dir)
		}
	})
	return f
}

func (f *fakeRelease) version(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(f.exe)
	for _, v := range []string{version.Version, older} {
		if bytes.Equal(b, releaseScript(v)) {
			return v
		}
	}
	return "?"
}

// newer is a version after this one, and older one before it.
const (
	newer = "99.0.0"
	older = "0.0.1"
)

func TestSyncUpdatesItselfAndRunsAgain(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	f := newFakeRelease(t, newer)
	srv.Lock()
	srv.Latest = newer
	srv.Unlock()
	commandLine = []string{"sync", "--state-dir", state}

	r := syncCmd(t, context.Background(), state, srv.URL)
	if !errors.Is(r.err, errReexeced) {
		t.Fatalf("sync: %v\n%s", r.err, r.stderr)
	}
	b, _ := os.ReadFile(f.exe)
	if !bytes.Equal(b, releaseScript(newer)) {
		t.Errorf("driveagent is now %q", b)
	}
	if b, _ := os.ReadFile(f.exe + ".prev"); !bytes.Equal(b, releaseScript(version.Version)) {
		t.Errorf("driveagent.prev is %q", b)
	}
	if len(f.execs) != 1 || strings.Join(f.execs[0], " ") != f.exe+" sync --state-dir "+state {
		t.Fatalf("re-exec'd %v", f.execs)
	}
	env := strings.Join(f.envs[0], "\n")
	if !strings.Contains(env, update.EnvUpdatedFrom+"="+version.Version) || !strings.Contains(env, update.EnvInstanceFD+"=") {
		t.Errorf("the re-exec's environment lacks the hand-over")
	}
	if !strings.Contains(r.stderr, "updated driveagent "+version.Version+" → "+newer) {
		t.Errorf("stderr = %q", r.stderr)
	}
	// Nothing was uploaded by the old binary.
	for _, p := range srv.Paths() {
		if strings.Contains(p, "/changes") {
			t.Errorf("the old binary uploaded: %s", p)
		}
	}
}

func TestScanUpdatesBeforeTouchingTheDrive(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	f := newFakeRelease(t, newer)
	srv.Lock()
	srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRecommended
	srv.Unlock()
	root := t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a.txt": "a"})
	args := []string{"--drive-id", "d1", "--path", root}
	commandLine = append([]string{"scan"}, args...)

	r := scanCmd(t, state, srv.URL, args...)
	if !errors.Is(r.err, errReexeced) {
		t.Fatalf("scan: %v\n%s", r.err, r.stderr)
	}
	if stateDBExists(state) {
		t.Error("the old binary opened state.db")
	}
	if len(f.execs) != 1 || f.execs[0][1] != "scan" {
		t.Errorf("re-exec'd %v", f.execs)
	}
}

func TestNoUpdate(t *testing.T) {
	cases := map[string]func(t *testing.T, state string) (want string){
		"already updated in this run": func(t *testing.T, state string) string {
			t.Setenv(update.EnvUpdatedFrom, "0.6.1")
			return "this run already updated driveagent from 0.6.1"
		},
		"turned off": func(t *testing.T, state string) string {
			t.Setenv(update.EnvNoAutoUpdate, "1")
			return "automatic updates are off"
		},
		"a development build": func(t *testing.T, state string) string {
			releaseBuild = func() bool { return false }
			return "this build isn't a release"
		},
		"another driveagent running": func(t *testing.T, state string) string {
			dir, _ := runlock.Dir()
			m, err := runlock.Join(context.Background(), dir, state, version.Version, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(m.Release)
			return "it will be installed when no other driveagent is running"
		},
		"a directory it can't write": func(t *testing.T, state string) string {
			if os.Geteuid() == 0 {
				t.Skip("root writes anywhere")
			}
			exe, _ := executable()
			os.Chmod(filepath.Dir(exe), 0o555)
			t.Cleanup(func() { os.Chmod(filepath.Dir(exe), 0o755) })
			return "isn't writable by this user"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			state := t.TempDir()
			srv := loggedIn(t, state)
			f := newFakeRelease(t, newer)
			srv.Lock()
			srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRecommended
			srv.Unlock()
			want := setup(t, state)
			r := syncCmd(t, context.Background(), state, srv.URL)
			if r.err != nil {
				t.Fatalf("sync: %v\n%s", r.err, r.stderr)
			}
			if !strings.Contains(r.stderr, "note: driveagent "+newer+" is available") || !strings.Contains(r.stderr, want) {
				t.Errorf("stderr = %q, want a note with %q", r.stderr, want)
			}
			if len(f.execs) != 0 || f.version(t) != version.Version {
				t.Errorf("updated anyway: execs %v, installed %s", f.execs, f.version(t))
			}
		})
	}
}

func TestRequiredUpdateThatCantHappenExits4(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	newFakeRelease(t, newer)
	t.Setenv(update.EnvNoAutoUpdate, "1")
	srv.Lock()
	srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRequired
	srv.Unlock()
	r := syncCmd(t, context.Background(), state, srv.URL)
	if r.code() != exitUpgrade || !strings.Contains(r.err.Error(), "Install it by hand: "+update.ReleasePage(newer)) {
		t.Errorf("exit %d: %v", r.code(), r.err)
	}
}

func TestRequiredUpdateWaitsToBeAlone(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	f := newFakeRelease(t, newer)
	oldWait, oldPoll := requiredWait, requiredPoll
	requiredWait, requiredPoll = 300*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { requiredWait, requiredPoll = oldWait, oldPoll })
	srv.Lock()
	srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRequired
	srv.Unlock()
	commandLine = []string{"sync"}

	dir, _ := runlock.Dir()
	other, err := runlock.Join(context.Background(), dir, state, version.Version, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Still busy when the wait ends: exit 4, to be installed by the next run.
	r := syncCmd(t, context.Background(), state, srv.URL)
	if r.code() != exitUpgrade || !strings.Contains(r.err.Error(), "will be installed by the next run") ||
		!strings.Contains(r.stderr, "waiting for the other driveagent to finish") {
		t.Fatalf("exit %d: %v\n%s", r.code(), r.err, r.stderr)
	}
	// The other one finishes during the wait: it updates.
	requiredWait = 5 * time.Second
	go func() {
		time.Sleep(200 * time.Millisecond)
		other.Release()
	}()
	r = syncCmd(t, context.Background(), state, srv.URL)
	if !errors.Is(r.err, errReexeced) || len(f.execs) != 1 {
		t.Fatalf("after the other finished: %v, execs %v\n%s", r.err, f.execs, r.stderr)
	}
}

func TestAFailedUpdateWaitsAnHour(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	f := newFakeRelease(t) // no release published
	srv.Lock()
	srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRecommended
	srv.Unlock()
	r := syncCmd(t, context.Background(), state, srv.URL)
	if r.err != nil || !strings.Contains(r.stderr, "couldn't update: driveagent "+newer+" has no SHA256SUMS") {
		t.Fatalf("sync: %v\n%s", r.err, r.stderr)
	}
	fetches := f.fetches
	r = syncCmd(t, context.Background(), state, srv.URL)
	if r.err != nil || !strings.Contains(r.stderr, "updating to it failed at") {
		t.Fatalf("second sync: %v\n%s", r.err, r.stderr)
	}
	if f.fetches != fetches {
		t.Error("tried again within the hour")
	}
	// The instance isn't left "updating": another version is refused as
	// usual, and the same version joins.
	dir, _ := runlock.Dir()
	m, err := runlock.Join(context.Background(), dir, state, version.Version, false, nil)
	if err != nil {
		t.Fatalf("joining after the failed update: %v", err)
	}
	m.Release()
}

func TestRemoteStatusOnlyReports(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	f := newFakeRelease(t, newer)
	srv.Lock()
	srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRecommended
	srv.Unlock()
	r := status(t, state, srv.URL)
	if !strings.Contains(r.stdout, "update     driveagent "+newer+" is available; it will be installed by the next scan, sync or login") {
		t.Errorf("stdout = %q", r.stdout)
	}
	if f.fetches != 0 || len(f.execs) != 0 {
		t.Error("remote-status updated")
	}
}

func updateCmd(t *testing.T, state, url string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	args = append([]string{"--state-dir", state, "--remote-url", url}, args...)
	err := runUpdate(context.Background(), args, &out, &errOut)
	return result{err, out.String(), errOut.String()}
}

func TestUpdateCommand(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	f := newFakeRelease(t, newer, older)
	if r := updateCmd(t, state, srv.URL); r.err != nil || r.stdout != "driveagent "+version.Version+" is current\n" {
		t.Errorf("current: %v %q", r.err, r.stdout)
	}
	srv.Lock()
	srv.Latest = newer
	srv.Unlock()
	if r := updateCmd(t, state, srv.URL, "--check"); r.err != nil || !strings.Contains(r.stdout, "driveagent "+newer+" is available") || f.fetches != 0 {
		t.Errorf("--check: %v %q, %d fetches", r.err, r.stdout, f.fetches)
	}
	// Turned off, it still updates by hand, and doesn't re-run anything.
	t.Setenv(update.EnvNoAutoUpdate, "1")
	r := updateCmd(t, state, srv.URL)
	if r.err != nil || !strings.Contains(r.stdout, "updated driveagent "+version.Version+" → "+newer) {
		t.Fatalf("update: %v\n%s%s", r.err, r.stdout, r.stderr)
	}
	if b, _ := os.ReadFile(f.exe); !bytes.Equal(b, releaseScript(newer)) || len(f.execs) != 0 {
		t.Errorf("installed %q, execs %v", b, f.execs)
	}
	// --version installs exactly that, older or not.
	if r := updateCmd(t, state, srv.URL, "--version", older); r.err != nil {
		t.Fatalf("--version: %v\n%s", r.err, r.stderr)
	}
	if b, _ := os.ReadFile(f.exe); !bytes.Equal(b, releaseScript(older)) {
		t.Errorf("after --version %s: %q", older, b)
	}
	// Busy: exit 5.
	dir, _ := runlock.Dir()
	m, err := runlock.Join(context.Background(), dir, state, version.Version, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Release()
	if r := updateCmd(t, state, srv.URL); r.code() != exitBusy {
		t.Errorf("busy: exit %d, %v", r.code(), r.err)
	}
}

// An unusable handed-over fd falls back to joining.
func TestJoinFallsBackWhenTheHandedFDIsWrong(t *testing.T) {
	state := t.TempDir()
	t.Setenv(update.EnvInstanceFD, "999")
	m, err := joinInstance(context.Background(), state, false)
	if err != nil {
		t.Fatal(err)
	}
	m.Release()
	if os.Getenv(update.EnvInstanceFD) != "" {
		t.Error("the hand-over is still in the environment")
	}
}

// updateTestChild sets up a child driveagent (the test binary run as
// driveagent) of TestUpdateEndToEnd: the test's key, its "installed"
// binary, a release build.
func updateTestChild() {
	if k := os.Getenv("DRIVEAGENT_TEST_KEY"); k != "" {
		b, _ := hex.DecodeString(k)
		update.Keys = []ed25519.PublicKey{b}
	}
	if exe := os.Getenv("DRIVEAGENT_TEST_EXE"); exe != "" {
		executable = func() (string, error) { return exe, nil }
		releaseBuild = func() bool { return true }
	}
}

// The real thing, across processes: a driveagent (this test binary) finds
// a newer version, downloads and installs it, and execs it, handing over
// the instance lock; the new binary (a script that reports the new
// version, and otherwise runs this test binary again) adopts the lock and
// does the sync.
func TestUpdateEndToEnd(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	scanned(t, state, "d1")
	srv.Lock()
	srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRecommended
	srv.Unlock()

	pub, priv, _ := ed25519.GenerateKey(nil)
	newBinary := []byte(fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then echo 'driveagent %s (e2e), protocols [1]'; exit 0; fi\nexec %q \"$@\"\n",
		newer, os.Args[0]))
	files := map[string][]byte{}
	addRelease(files, priv, newer, newBinary)
	rel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[strings.TrimPrefix(r.URL.Path, "/driveagent/v")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	defer rel.Close()
	exe := filepath.Join(t.TempDir(), "driveagent")
	os.WriteFile(exe, releaseScript(version.Version), 0o755)

	cmd := driveagent(t, "sync", "--state-dir", state, "--remote-url", srv.URL)
	cmd.Env = append(cmd.Env, "DRIVEAGENT_TEST_KEY="+hex.EncodeToString(pub), "DRIVEAGENT_TEST_EXE="+exe,
		update.EnvURL+"="+rel.URL, update.EnvNoAutoUpdate+"=", update.EnvUpdatedFrom+"=")
	out, err := cmd.CombinedOutput()
	if code := exitStatus(t, err); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{
		"updated driveagent " + version.Version + " → " + newer,
		// The new process, after the exec: it doesn't update again.
		"this run already updated driveagent from " + version.Version,
		"d1: uploaded",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, newBinary) {
		t.Errorf("installed: %q", b)
	}
	// The sync ran once, in the new process.
	n := 0
	for _, p := range srv.Paths() {
		if strings.HasSuffix(p, "/changes") {
			n++
		}
	}
	if n == 0 {
		t.Errorf("nothing uploaded: %v", srv.Paths())
	}
	// It left the instance as it should: not "updating", and joinable.
	dir, _ := runlock.Dir()
	m, err := runlock.Join(context.Background(), dir, state, version.Version, false, nil)
	if err != nil {
		t.Fatalf("joining afterwards: %v", err)
	}
	m.Release()
}

// A verified instance-lock fd that isn't adopted (a process joining
// alone) is closed, so Join doesn't wait for it.
func TestJoinAloneLetsGoOfAHandedLock(t *testing.T) {
	state := t.TempDir()
	dir, _ := runlock.Dir()
	fd, err := unix.Open(filepath.Join(dir, "instance.lock"), unix.O_RDWR|unix.O_CREAT, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	t.Setenv(update.EnvInstanceFD, strconv.Itoa(fd))
	done := make(chan error, 1)
	go func() {
		m, err := joinInstance(context.Background(), state, true)
		if err == nil {
			m.Release()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("joining alone waited for its own handed lock")
	}
	if unix.Fstat(fd, &unix.Stat_t{}) == nil {
		t.Error("the handed fd is still open")
		unix.Close(fd)
	}
}

// Interrupted mid-download, the update isn't recorded as failed.
func TestACancelledDownloadIsntAFailure(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	newFakeRelease(t, newer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inner := updateSource
	updateSource = func() update.Source {
		src := inner()
		src.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			cancel()
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		return src
	}
	srv.Lock()
	srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRecommended
	srv.Unlock()
	r := syncCmd(t, ctx, state, srv.URL)
	if !errors.Is(r.err, context.Canceled) {
		t.Errorf("sync: %v\n%s", r.err, r.stderr)
	}
	dir, _ := runlock.Dir()
	if f := update.LoadFailure(dir); f.Version != "" {
		t.Errorf("recorded %+v", f)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Only an upgrade decision makes an update required: unsupported_protocol
// does, deliberately; an unknown decision is an error, not an upgrade.
func TestWhichHandshakeErrorsUpdate(t *testing.T) {
	for decision, wantUpdate := range map[string]bool{wire.DecisionUnsupportedProtocol: true, "bogus": false} {
		t.Run(decision, func(t *testing.T) {
			state := t.TempDir()
			srv := loggedIn(t, state)
			f := newFakeRelease(t, newer)
			srv.Lock()
			srv.Latest, srv.Decision = newer, decision
			srv.Unlock()
			r := syncCmd(t, context.Background(), state, srv.URL)
			if got := errors.Is(r.err, errReexeced); got != wantUpdate {
				t.Errorf("updated %v (%v), want %v\n%s", got, r.err, wantUpdate, r.stderr)
			}
			if !wantUpdate && (r.code() != exitRemote || f.fetches != 0) {
				t.Errorf("exit %d, %d fetches", r.code(), f.fetches)
			}
		})
	}
}

func TestUpdateVersionMustBeAVersion(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	f := newFakeRelease(t, newer)
	if r := updateCmd(t, state, srv.URL, "--version", "../x"); r.code() != exitUsage || f.fetches != 0 {
		t.Errorf("--version ../x: exit %d, %v, %d fetches", r.code(), r.err, f.fetches)
	}
}

// A platform without builds never downloads anything.
func TestNoBuildsForThisPlatform(t *testing.T) {
	state := t.TempDir()
	srv := loggedIn(t, state)
	f := newFakeRelease(t, newer)
	inner := updateSource
	updateSource = func() update.Source {
		src := inner()
		src.GOOS, src.GOARCH = "linux", "arm64"
		return src
	}
	srv.Lock()
	srv.Latest, srv.Decision = newer, wire.DecisionUpgradeRecommended
	srv.Unlock()
	r := syncCmd(t, context.Background(), state, srv.URL)
	if r.err != nil || !strings.Contains(r.stderr, "no driveagent builds are published for linux/arm64") || f.fetches != 0 {
		t.Errorf("sync: %v, %d fetches\n%s", r.err, f.fetches, r.stderr)
	}
}
