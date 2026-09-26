package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/testutil"
)

// With DRIVEAGENT_TEST_MAIN set, the test binary runs driveagent's main
// with its own arguments instead of the tests (m.Run, which would parse
// them as test flags, never runs). That lets a test run the real CLI as a
// child process and send it signals.
func TestMain(m *testing.M) {
	if os.Getenv("DRIVEAGENT_TEST_MAIN") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

func driveagent(t *testing.T, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), "DRIVEAGENT_TEST_MAIN=1")
	return cmd
}

func exitStatus(t *testing.T, err error) int {
	t.Helper()
	var ee *exec.ExitError
	if err == nil {
		return 0
	}
	if !errors.As(err, &ee) {
		t.Fatalf("run: %v", err)
	}
	return ee.ExitCode()
}

// bigTree has a large sparse file, so hashing takes long enough to be
// interrupted without using disk space.
func bigTree(t *testing.T) string {
	root := t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "alpha", "b/c": "charlie"})
	f, err := os.Create(filepath.Join(root, "big.img"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(64 << 30); err != nil {
		t.Skip("sparse files unsupported:", err)
	}
	f.Close()
	return root
}

// interrupt starts a scan, waits until it's running, and sends sig.
func interrupt(t *testing.T, sig os.Signal) (code int, stdout, stderr string) {
	root := bigTree(t)
	cmd := driveagent(t, "scan", "--drive-id", "d1", "--path", root, "--state-dir", t.TempDir(), "--workers", "1")
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var outBuf bytes.Buffer
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		outBuf.WriteString(sc.Text() + "\n")
		if strings.HasPrefix(sc.Text(), "scanning ") {
			break
		}
	}
	time.Sleep(300 * time.Millisecond) // into the 64 GiB file
	start := time.Now()
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatal(err)
	}
	for sc.Scan() {
		outBuf.WriteString(sc.Text() + "\n")
	}
	code = exitStatus(t, cmd.Wait())
	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("stopping took %s; a cancel should stop hashing mid-file", took)
	}
	return code, outBuf.String(), errBuf.String()
}

func TestCtrlCExits130(t *testing.T) {
	code, out, errOut := interrupt(t, os.Interrupt)
	if code != exitSIGINT {
		t.Errorf("exit %d, want 130\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(out, "scan stopped early") || !strings.Contains(out, "re-run the same command to resume") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(errOut, "interrupted by SIGINT") {
		t.Errorf("stderr = %q", errOut)
	}
}

func TestSIGTERMExits143(t *testing.T) {
	code, out, errOut := interrupt(t, syscall.SIGTERM)
	if code != exitSIGTERM {
		t.Errorf("exit %d, want 143\nstdout: %s\nstderr: %s", code, out, errOut)
	}
}

func TestScanExitCodes(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, rootA, testutil.Tree{"a": "a"})
	testutil.WriteTree(t, rootB, testutil.Tree{"b": "b"})
	state := t.TempDir()
	run := func(args ...string) (int, string) {
		cmd := driveagent(t, append([]string{"scan", "--state-dir", state}, args...)...)
		b, err := cmd.CombinedOutput()
		return exitStatus(t, err), string(b)
	}
	if code, out := run("--drive-id", "d1", "--path", rootA); code != 0 || !strings.Contains(out, "seen=1") {
		t.Errorf("scan: exit %d\n%s", code, out)
	}
	if code, out := run("--drive-id", "d1", "--path", rootB); code != exitLocal || !strings.Contains(out, "refusing to rescan") {
		t.Errorf("root conflict: exit %d\n%s", code, out)
	}
	if code, out := run("--drive-id", "d1", "--path", rootB, "--replace-root"); code != 0 || !strings.Contains(out, "discarded drive") {
		t.Errorf("replace-root: exit %d\n%s", code, out)
	}
	if code, out := run("--drive-id", "d2", "--path", filepath.Join(rootA, "unplugged")); code != exitLocal || !strings.Contains(out, "not accessible") {
		t.Errorf("missing path: exit %d\n%s", code, out)
	}
	if code, _ := run("--path", rootA); code != exitUsage {
		t.Errorf("missing --drive-id: exit %d", code)
	}
}

func TestExitCodeMapping(t *testing.T) {
	plain := context.Background()
	for _, c := range []struct {
		ctx  context.Context
		err  error
		want int
	}{
		{plain, nil, 0},
		{plain, errors.New("boom"), 1},
		{plain, usageErr("bad flag"), 2},
		{plain, &exitError{code: exitRemote, err: errors.New("down")}, 3},
		{cancelledWith(signalCause{sig: os.Interrupt}), errors.New("whatever it surfaced as"), 130},
		{cancelledWith(signalCause{sig: syscall.SIGTERM}), nil, 143},
		{cancelledWith(errors.New("not a signal")), errors.New("boom"), 1},
	} {
		if got := exitCode(c.ctx, c.err); got != c.want {
			t.Errorf("exitCode(%v, %v) = %d, want %d", context.Cause(c.ctx), c.err, got, c.want)
		}
	}
}

func cancelledWith(cause error) context.Context {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	return ctx
}
