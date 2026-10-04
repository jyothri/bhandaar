package update

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Executable is the running driveagent's path, symlinks resolved: the file
// an update replaces, and the one it re-runs.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// NotWritableError is a binary whose directory this user can't write to,
// so it can't be replaced.
type NotWritableError struct {
	Dir string
	Err error
}

func (e *NotWritableError) Error() string {
	return fmt.Sprintf("%s isn't writable by this user", e.Dir)
}

func (e *NotWritableError) Unwrap() error { return e.Err }

// CheckWritable tells whether exe can be replaced, by creating a file
// beside it: that answers for ACLs, read-only mounts and immutable flags,
// which access(2) doesn't.
func CheckWritable(exe string) error {
	f, err := os.CreateTemp(filepath.Dir(exe), ".driveagent-update-*")
	if err != nil {
		return &NotWritableError{Dir: filepath.Dir(exe), Err: err}
	}
	f.Close()
	return os.Remove(f.Name())
}

// smokeTimeout bounds "driveagent version" on the new binary.
const smokeTimeout = 10 * time.Second

// Install replaces exe with bin, once bin runs and reports version want
// (docs/specs/agent-auto-update.md, "Replacing the binary"): bin is
// written beside exe, run, the current binary is copied to exe.prev, and
// bin is renamed over exe. Until the rename, exe is untouched; if any step
// fails, nothing changes but a refreshed .prev.
func Install(ctx context.Context, exe string, bin []byte, want string) (err error) {
	f, err := os.CreateTemp(filepath.Dir(exe), ".driveagent-update-*")
	if err != nil {
		return &NotWritableError{Dir: filepath.Dir(exe), Err: err}
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	_, err = f.Write(bin)
	if err == nil {
		err = f.Chmod(0o755)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing the new driveagent: %w", err)
	}

	got, err := runVersion(ctx, tmp)
	if err != nil {
		return fmt.Errorf("the new driveagent doesn't run: %w", err)
	}
	if got != want {
		return fmt.Errorf("the new driveagent says it's %s, not %s", got, want)
	}
	// A way back first: without it, no update.
	if err := copyFile(exe, exe+".prev"); err != nil {
		return fmt.Errorf("keeping the current driveagent as %s.prev: %w", filepath.Base(exe), err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		return fmt.Errorf("replacing %s: %w", exe, err)
	}
	// Make the rename durable. The update is done either way; at worst a
	// power cut leaves the old binary, which still works.
	if d, err := os.Open(filepath.Dir(exe)); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// runVersion runs "<bin> version" and returns the version it reports;
// tests replace it.
var runVersion = func(ctx context.Context, bin string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, smokeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "version")
	cmd.Env = cleanEnv(os.Environ())
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// "driveagent 0.7.0 (abc1234), protocols [1]"
	f := strings.Fields(string(out))
	if len(f) < 2 || f[0] != "driveagent" {
		return "", fmt.Errorf("unexpected \"version\" output %q", strings.TrimSpace(string(out)))
	}
	return f[1], nil
}

// cleanEnv drops what a re-exec hands on, which the smoke test mustn't
// see.
func cleanEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, EnvUpdatedFrom+"=") && !strings.HasPrefix(kv, EnvInstanceFD+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// copyFile copies src to dst through a temp file and a rename, keeping
// src's mode.
func copyFile(src, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(dst), ".driveagent-prev-*")
	if err != nil {
		return err
	}
	tmp := out.Name()
	defer func() {
		if err != nil {
			os.Remove(tmp)
		}
	}()
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(fi.Mode().Perm())
	}
	if err == nil {
		err = out.Sync()
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
