// Package runlock keeps driveagent processes apart across state dirs: the
// instance lock (every driveagent running on the machine uses one state dir
// and one version) and the physical-drive lock (one scan per disk). Both
// live in the per-user lock dir. See docs/archive/agent-hardening.md.
//
// Every lock is a flock on a file that is never deleted, so a crashed
// process releases its locks and nothing goes stale. A lock may have a JSON
// sidecar describing its holder, written after the lock is taken and read
// only while the lock is busy, to say who holds it; a leftover sidecar is
// never trusted.
package runlock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

// EnvDir overrides the lock dir. Each test uses its own; so can a throwaway
// dev state dir. Nothing is coordinated across lock dirs.
const EnvDir = "DRIVEAGENT_LOCK_DIR"

// dirName is the lock dir's name in the home directory.
const dirName = ".driveagent-locks"

// ErrBusy is what every busy-lock error matches (errors.Is): another
// driveagent holds the lock.
var ErrBusy = errors.New("busy")

// currentUser is user.Current; tests replace it.
var currentUser = user.Current

// Dir returns the lock dir, creating it 0700: $DRIVEAGENT_LOCK_DIR, else
// <home>/.driveagent-locks. The home directory comes from the account
// database, not $HOME, so every process of the user finds the same dir
// however it was started (cron, sudo -E, tmux); os.UserHomeDir is only a
// fallback for when the account can't be looked up.
func Dir() (string, error) {
	dir := os.Getenv(EnvDir)
	if dir == "" {
		home := ""
		if u, err := currentUser(); err == nil {
			home = u.HomeDir
		}
		if home == "" {
			h, err := os.UserHomeDir()
			if err != nil || h == "" {
				return "", fmt.Errorf("finding the home directory for the lock dir (set $%s): %v", EnvDir, err)
			}
			home = h
		}
		dir = filepath.Join(home, dirName)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating the lock dir: %w", err)
	}
	return dir, nil
}

// writeSidecar writes v as JSON to path atomically (temp file + rename).
func writeSidecar(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// readSidecar reads the JSON sidecar at path into v; ok is false if there
// is none or it can't be parsed.
func readSidecar(path string, v any) (ok bool) {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// since formats a holder's start time for messages.
func since(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	return t.Local().Format("2006-01-02 15:04")
}
