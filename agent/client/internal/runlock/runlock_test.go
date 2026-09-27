package runlock

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestDirOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "locks")
	t.Setenv(EnvDir, want)
	got, err := Dir()
	if err != nil || got != want {
		t.Fatalf("Dir() = %q, %v; want %q", got, err, want)
	}
	fi, err := os.Stat(want)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("lock dir: %v, %v", fi.Mode(), err)
	}
}

// The home directory comes from the account, not $HOME.
func TestDirIgnoresHOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvDir, "")
	t.Setenv("HOME", t.TempDir())
	currentUser = func() (*user.User, error) { return &user.User{HomeDir: home}, nil }
	t.Cleanup(func() { currentUser = user.Current })
	got, err := Dir()
	if want := filepath.Join(home, ".driveagent-locks"); err != nil || got != want {
		t.Fatalf("Dir() = %q, %v; want %q", got, err, want)
	}
}

func TestDirFallsBackToHOME(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvDir, "")
	t.Setenv("HOME", home)
	currentUser = func() (*user.User, error) { return nil, errors.New("no passwd entry") }
	t.Cleanup(func() { currentUser = user.Current })
	got, err := Dir()
	if want := filepath.Join(home, ".driveagent-locks"); err != nil || got != want {
		t.Fatalf("Dir() = %q, %v; want %q", got, err, want)
	}
}

func join(t *testing.T, dir, stateDir, version string, alone bool) (func(), error) {
	t.Helper()
	return JoinInstance(context.Background(), dir, stateDir, version, alone)
}

func TestInstance(t *testing.T) {
	dir, stateA, stateB := t.TempDir(), t.TempDir(), t.TempDir()

	r1, err := join(t, dir, stateA, "0.5.0", false)
	if err != nil {
		t.Fatal(err)
	}
	// The same state dir and version join.
	r2, err := join(t, dir, stateA, "0.5.0", false)
	if err != nil {
		t.Fatalf("second process: %v", err)
	}

	// Another state dir is refused, naming the running one.
	_, err = join(t, dir, stateB, "0.5.0", false)
	var busy *InstanceBusyError
	if !errors.As(err, &busy) || !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "with state dir "+stateA) {
		t.Fatalf("another state dir: %v", err)
	}
	if busy.Running.PID != os.Getpid() || busy.Running.Version != "0.5.0" {
		t.Errorf("running: %+v", busy.Running)
	}
	// Another version is refused.
	if _, err := join(t, dir, stateA, "0.5.1", false); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "driveagent 0.5.0 is running") {
		t.Fatalf("another version: %v", err)
	}
	// So is a process that needs to be alone.
	if _, err := join(t, dir, stateA, "0.5.0", true); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "to itself") {
		t.Fatalf("alone while others run: %v", err)
	}

	// Once everyone has left, another state dir takes over.
	r1()
	if _, err := join(t, dir, stateB, "0.5.0", false); !errors.Is(err, ErrBusy) {
		t.Fatalf("with one process still running: %v", err)
	}
	r2()
	r3, err := join(t, dir, stateB, "0.5.1", false)
	if err != nil {
		t.Fatalf("after everyone left: %v", err)
	}
	defer r3()
	if _, err := join(t, dir, stateA, "0.5.0", false); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), stateB) {
		t.Fatalf("instance.json wasn't rewritten: %v", err)
	}
}

func TestInstanceAlone(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	release, err := join(t, dir, state, "0.5.0", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := join(t, dir, state, "0.5.0", false); !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "login --new-agent") {
		t.Fatalf("joining a process that's alone: %v", err)
	}
	release()
	r, err := join(t, dir, state, "0.5.0", false)
	if err != nil {
		t.Fatalf("after it left: %v", err)
	}
	r()
}

// A symlink to the state dir is the same state dir.
func TestInstanceSymlinkedStateDir(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(state, link); err != nil {
		t.Skip(err)
	}
	r1, err := join(t, dir, state, "0.5.0", false)
	if err != nil {
		t.Fatal(err)
	}
	defer r1()
	r2, err := join(t, dir, link, "0.5.0", false)
	if err != nil {
		t.Fatalf("through the symlink: %v", err)
	}
	r2()
}

// A leftover instance.json with no one running is ignored.
func TestInstanceStaleInfo(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	if err := writeSidecar(filepath.Join(dir, instanceInfo), Instance{StateDir: "/elsewhere", Version: "0.1.0", PID: 1}); err != nil {
		t.Fatal(err)
	}
	r, err := join(t, dir, state, "0.5.0", false)
	if err != nil {
		t.Fatalf("stale instance.json: %v", err)
	}
	r()
}

// Many processes joining at once: with one state dir, all get in; with two,
// exactly one state dir wins.
func TestInstanceConcurrentJoins(t *testing.T) {
	dir := t.TempDir()
	states := []string{t.TempDir(), t.TempDir()}
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		joined   = map[string]int{}
		releases []func()
	)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state := states[i%2]
			r, err := JoinInstance(context.Background(), dir, state, "0.5.0", false)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				joined[state]++
				releases = append(releases, r)
			case !errors.Is(err, ErrBusy):
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(joined) != 1 {
		t.Errorf("state dirs that got in: %v", joined)
	}
	for _, n := range joined {
		if n != 10 {
			t.Errorf("%d of 10 processes with the winning state dir got in", n)
		}
	}
	for _, r := range releases {
		r()
	}
}

func TestInstanceGateTimeout(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	old := gateTimeout
	gateTimeout = 100 * time.Millisecond
	t.Cleanup(func() { gateTimeout = old })
	// A process stuck holding the gate.
	holdExclusive(t, filepath.Join(dir, instanceGate))
	if _, err := join(t, dir, state, "0.5.0", false); err == nil || errors.Is(err, ErrBusy) {
		t.Fatalf("stuck gate: %v", err)
	}
}

// holdExclusive locks path, as another process would, until the test ends.
func holdExclusive(t *testing.T, path string) {
	t.Helper()
	l := flock.New(path)
	if ok, err := l.TryLock(); err != nil || !ok {
		t.Fatalf("locking %s: %v", path, err)
	}
	t.Cleanup(func() { l.Unlock() })
}

func holder(id, path string) DiskHolder {
	return DiskHolder{DriveID: id, Path: path, PID: os.Getpid(), Started: time.Now()}
}

func TestLockDisks(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	r1, err := LockDisks(ctx, dir, []string{"serial:NA77YET6"}, holder("seagate1", "/media/Seagate1/Jyo"), false, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Another disk is free.
	r2, err := LockDisks(ctx, dir, []string{"serial:NA95K2KP"}, holder("seagate2", "/mnt/seagate2"), false, nil)
	if err != nil {
		t.Fatalf("another disk: %v", err)
	}
	r2()

	// The same disk is busy, whatever the drive id; the error names the holder.
	_, err = LockDisks(ctx, dir, []string{"dev:8:16", "serial:NA77YET6"}, holder("boot", "/media/DBR_BOOT"), false, nil)
	var busy *DiskBusyError
	if !errors.As(err, &busy) || !errors.Is(err, ErrBusy) {
		t.Fatalf("same disk: %v", err)
	}
	if busy.Holder.DriveID != "seagate1" || !strings.Contains(err.Error(), "serial NA77YET6): drive seagate1, /media/Seagate1/Jyo") {
		t.Errorf("busy: %v", err)
	}
	// The keys it took before the busy one were released.
	r3, err := LockDisks(ctx, dir, []string{"dev:8:16"}, holder("x", "/x"), false, nil)
	if err != nil {
		t.Fatalf("dev:8:16 was left locked: %v", err)
	}
	r3()

	// With wait, it waits, says so once, and gets the lock once it's free.
	var waited []string
	done := make(chan error)
	go func() {
		r, err := LockDisks(ctx, dir, []string{"serial:NA77YET6"}, holder("boot", "/media/DBR_BOOT"), true,
			func(b *DiskBusyError) { waited = append(waited, b.Holder.DriveID) })
		if r != nil {
			r()
		}
		done <- err
	}()
	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("finished while the disk was busy: %v", err)
	default:
	}
	r1()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(waited) != 1 || waited[0] != "seagate1" {
		t.Errorf("waiting calls: %v", waited)
	}
}

func TestLockDisksWaitCancelled(t *testing.T) {
	dir := t.TempDir()
	r, err := LockDisks(context.Background(), dir, []string{"serial:A"}, holder("a", "/a"), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := LockDisks(ctx, dir, []string{"serial:A"}, holder("b", "/b"), true, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled wait: %v", err)
	}
}

// A sidecar that can't be read still gives a busy error, naming the key.
func TestLockDisksNoSidecar(t *testing.T) {
	dir := t.TempDir()
	holdExclusive(t, diskLockPath(dir, "path:/x"))
	_, err := LockDisks(context.Background(), dir, []string{"path:/x"}, holder("b", "/b"), false, nil)
	if !errors.Is(err, ErrBusy) || err.Error() != "another scan is reading this disk (path /x)" {
		t.Fatalf("%v", err)
	}
}

func TestDescribeKey(t *testing.T) {
	for key, want := range map[string]string{
		"serial:NA77YET6":     "serial NA77YET6",
		"dev:8:16":            "device 8:16",
		"dev:disk4":           "device disk4",
		"fs:linux:E85600BF7E": "filesystem linux:E85600BF7E",
		"path:/mnt/x":         "path /mnt/x",
		"odd":                 "odd",
	} {
		if got := DescribeKey(key); got != want {
			t.Errorf("DescribeKey(%q) = %q, want %q", key, got, want)
		}
	}
}
