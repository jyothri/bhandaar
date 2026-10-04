package runlock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The helper process for TestNoGapAtTheExec: the re-exec'd driveagent,
// adopting the instance lock it was handed.
func TestMain(m *testing.M) {
	if os.Getenv("RUNLOCK_TEST_HELPER") == "adopt" {
		adoptHelper()
		return
	}
	os.Exit(m.Run())
}

func adoptHelper() {
	fd, _ := strconv.Atoi(os.Getenv("DRIVEAGENT_INSTANCE_FD"))
	// Give the test's joiner time to arrive in the gap.
	time.Sleep(300 * time.Millisecond)
	m, err := Adopt(context.Background(), os.Getenv("RUNLOCK_TEST_DIR"), uintptr(fd), os.Getenv("RUNLOCK_TEST_STATE"), "0.7.1")
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(5)
	}
	fmt.Println("adopted")
	// Stay joined until the test closes stdin.
	bufio.NewReader(os.Stdin).ReadString('\n')
	m.Release()
}

func joinM(t *testing.T, dir, state, version string) *Membership {
	t.Helper()
	m, err := Join(context.Background(), dir, state, version, false, nil)
	if err != nil {
		t.Fatalf("joining as %s: %v", version, err)
	}
	t.Cleanup(m.Release)
	return m
}

// Required (docs/specs/agent-auto-update.md, "Tests"): a TryAlone that
// fails because others run leaves the process joined, so a different
// version is still refused.
func TestFailedTryAloneKeepsTheProcessJoined(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	m1 := joinM(t, dir, state, "0.7.0")
	m2 := joinM(t, dir, state, "0.7.0")
	if ok, err := m1.TryAlone(context.Background()); ok || err != nil {
		t.Fatalf("TryAlone beside another process = %v, %v", ok, err)
	}
	if _, err := join(t, dir, state, "0.7.1", false); !errors.Is(err, ErrBusy) {
		t.Fatalf("another version, right after: %v", err)
	}
	// m1 still holds its place once m2 has gone.
	m2.Release()
	if _, err := join(t, dir, state, "0.7.1", false); !errors.Is(err, ErrBusy) {
		t.Fatalf("another version, with m1 alone: %v", err)
	}
	// And now m1 is alone.
	if ok, err := m1.TryAlone(context.Background()); !ok || err != nil {
		t.Fatalf("TryAlone alone = %v, %v", ok, err)
	}
	m1.EndUpdate()
}

// TryAlone keeps everyone out until EndUpdate; a joiner waits meanwhile.
func TestTryAloneHoldsJoinersUntilEndUpdate(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	setTimes(t, 50*time.Millisecond, time.Minute)
	m := joinM(t, dir, state, "0.7.0")
	if ok, err := m.TryAlone(context.Background()); !ok || err != nil {
		t.Fatalf("TryAlone = %v, %v", ok, err)
	}
	var info Instance
	readSidecar(filepath.Join(dir, instanceInfo), &info)
	if info.UpdatingSince == nil || info.Version != "0.7.0" {
		t.Fatalf("instance.json while updating: %+v", info)
	}

	waited := make(chan Instance, 1)
	joined := make(chan error, 1)
	go func() {
		m2, err := Join(context.Background(), dir, state, "0.7.0", false, func(i Instance) { waited <- i })
		if err == nil {
			m2.Release()
		}
		joined <- err
	}()
	select {
	case i := <-waited:
		if i.PID != os.Getpid() {
			t.Errorf("waiting for %+v", i)
		}
	case err := <-joined:
		t.Fatalf("joined while updating: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("the joiner neither waited nor joined")
	}
	if err := m.EndUpdate(); err != nil {
		t.Fatal(err)
	}
	if err := <-joined; err != nil {
		t.Fatalf("after the update: %v", err)
	}
	info = Instance{}
	readSidecar(filepath.Join(dir, instanceInfo), &info)
	if info.UpdatingSince != nil {
		t.Errorf("instance.json still says updating: %+v", info)
	}
}

// A joiner gives up on an update that has taken too long (a hung updater,
// or a stale instance.json).
func TestJoinerGivesUpOnAStaleUpdate(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	setTimes(t, 50*time.Millisecond, 300*time.Millisecond)
	m := joinM(t, dir, state, "0.7.0")
	if ok, _ := m.TryAlone(context.Background()); !ok {
		t.Fatal("not alone")
	}
	defer m.EndUpdate()
	start := time.Now()
	_, err := join(t, dir, state, "0.7.0", false)
	if err == nil {
		t.Fatal("joined while updating")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("gave up after %v", d)
	}
}

// Required: no gap at the exec. The lock is handed to a new process (the
// test binary run as a helper, inheriting the fd as the re-exec'd
// driveagent does), and the old process lets go. A joiner of the old
// version arriving in between waits, then is refused; the new process
// isn't.
func TestNoGapAtTheExec(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	setTimes(t, time.Second, time.Minute)
	old, err := Join(context.Background(), dir, state, "0.7.0", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := old.TryAlone(context.Background()); !ok || err != nil {
		t.Fatalf("TryAlone = %v, %v", ok, err)
	}
	if _, err := old.HandOver(); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.ExtraFiles = []*os.File{old.lock.f} // fd 3 in the helper
	cmd.Env = append(os.Environ(), "RUNLOCK_TEST_HELPER=adopt", "DRIVEAGENT_INSTANCE_FD=3",
		"RUNLOCK_TEST_DIR="+dir, "RUNLOCK_TEST_STATE="+state)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cmd.Wait() })
	// The old process is gone, as after exec: only the helper holds the
	// lock now.
	old.lock.close()

	// An old-version joiner (another cron job) arrives in the gap.
	refused := make(chan error, 1)
	go func() {
		m, err := Join(context.Background(), dir, state, "0.7.0", false, nil)
		if err == nil {
			m.Release()
		}
		refused <- err
	}()

	line, _ := bufio.NewReader(stdout).ReadString('\n')
	if strings.TrimSpace(line) != "adopted" {
		t.Fatalf("the new process: %q", line)
	}
	select {
	case err := <-refused:
		if !errors.Is(err, ErrBusy) || !strings.Contains(err.Error(), "driveagent 0.7.1 is running") {
			t.Fatalf("the old-version joiner: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the old-version joiner is still waiting")
	}
	// A joiner of the new version gets in beside it.
	m := joinM(t, dir, state, "0.7.1")
	m.Release()
}

func TestAdoptRefusesAnotherFile(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	f, err := os.CreateTemp(t.TempDir(), "other")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// A fd of its own, as one handed over by exec is: Adopt closes it.
	fd, err := unix.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Adopt(context.Background(), dir, uintptr(fd), state, "0.7.1"); !errors.Is(err, ErrNotInstanceLock) {
		t.Errorf("Adopt of another file: %v", err)
	}
	// It left the fd alone: still open, still that file.
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		t.Errorf("Adopt closed a fd that wasn't the lock: %v", err)
	}
	unix.Close(fd)
}

func TestTakeBackAfterAFailedExec(t *testing.T) {
	dir, state := t.TempDir(), t.TempDir()
	m := joinM(t, dir, state, "0.7.0")
	if ok, _ := m.TryAlone(context.Background()); !ok {
		t.Fatal("not alone")
	}
	if _, err := m.HandOver(); err != nil {
		t.Fatal(err)
	}
	if err := m.TakeBack(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Back to normal: the same version joins, another is refused.
	m2 := joinM(t, dir, state, "0.7.0")
	m2.Release()
	if _, err := join(t, dir, state, "0.7.1", false); !errors.Is(err, ErrBusy) {
		t.Errorf("another version after TakeBack: %v", err)
	}
}

func setTimes(t *testing.T, poll, wait time.Duration) {
	t.Helper()
	oldPoll, oldWait, oldGate := pollInterval, updateWait, gateTimeout
	pollInterval, updateWait, gateTimeout = poll/2, wait, poll
	t.Cleanup(func() { pollInterval, updateWait, gateTimeout = oldPoll, oldWait, oldGate })
}
