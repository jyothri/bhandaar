package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/creds"
	"github.com/jyothri/bhandaar/agent/client/internal/identity"
	"github.com/jyothri/bhandaar/agent/client/internal/runlock"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/client/internal/update"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
)

// joinInstance takes the instance lock for a command using stateDir
// (docs/archive/agent-hardening.md, "Goal 1: the instance lock"): every
// driveagent running on the machine uses one state dir and one version.
// With alone, no other driveagent may run until release. A refusal exits
// 5.
//
// A driveagent just updated adopts the lock its old binary handed it
// (docs/specs/agent-auto-update.md, "Handing the lock to the new binary"),
// or else joins as usual. While another driveagent updates itself, it
// waits.
func joinInstance(ctx context.Context, stateDir string, alone bool) (*runlock.Membership, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating state dir: %w", err)
	}
	dir, err := runlock.Dir()
	if err != nil {
		return nil, err
	}
	if s := os.Getenv(update.EnvInstanceFD); s != "" {
		os.Unsetenv(update.EnvInstanceFD)
		if fd, err := strconv.Atoi(s); err == nil && fd > 2 && !alone {
			if m, err := runlock.Adopt(ctx, dir, uintptr(fd), stateDir, version.Version); err == nil {
				return m, nil
			}
		}
	}
	m, err := runlock.Join(ctx, dir, stateDir, version.Version, alone, func(i runlock.Instance) {
		fmt.Fprintf(os.Stderr, "waiting for driveagent to finish updating itself (pid %d)\n", i.PID)
	})
	if errors.Is(err, runlock.ErrBusy) {
		return nil, &exitError{code: exitBusy, err: err}
	}
	return m, err
}

// diskKeys is identity.DiskKeys; tests replace it.
var diskKeys = identity.DiskKeys

// lockDisk takes the physical-drive lock of the disk(s) holding root, for
// a scan of path as driveID (docs/archive/agent-hardening.md, "Goal 2: the
// physical-drive lock"): one scan per disk. If another scan holds it, that
// exits 5, or with wait, waits for it.
func lockDisk(ctx context.Context, root, driveID, path string, wait bool, stderr io.Writer) (release func(), err error) {
	keys := diskKeys(root)
	if !identity.IsDiskKey(keys[0]) {
		fmt.Fprintf(stderr, "note: couldn't find the disk holding %s; only other scans of the same %s are kept out\n",
			root, runlock.DescribeKey(keys[0]))
	}
	dir, err := runlock.Dir()
	if err != nil {
		return nil, err
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	holder := runlock.DiskHolder{DriveID: driveID, Path: path, PID: os.Getpid(), Started: time.Now().UTC()}
	release, err = runlock.LockDisks(ctx, dir, keys, holder, wait, func(b *runlock.DiskBusyError) {
		fmt.Fprintf(stderr, "waiting: %v\n", b)
	})
	if errors.Is(err, runlock.ErrBusy) {
		return nil, &exitError{code: exitBusy, err: fmt.Errorf("%w\nOne scan per disk at a time: wait for it, or re-run with --wait", err)}
	}
	return release, err
}

// startNewAgent makes the state dir a new agent on this machine ("login
// --new-agent", docs/archive/agent-hardening.md, "Goal 1: machine binding"):
// every drive forgets its stream and marker, so it's uploaded again in
// full under the new agent; the login is forgotten, without revoking it,
// since the old agent may still be in use on the machine the state dir was
// copied from; and agent.json gets a new agent id and this machine. It
// runs alone (joinInstance). Each step can be repeated if one fails.
func startNewAgent(stateDir string) (agentID string, err error) {
	if stateDBExists(stateDir) {
		st, err := store.Open(stateDir)
		if err != nil {
			return "", err
		}
		err = st.ResetStreams()
		st.Close()
		if err != nil {
			return "", fmt.Errorf("resetting the drives' uploads: %w", err)
		}
	}
	if err := creds.Delete(stateDir); err != nil {
		return "", err
	}
	return creds.NewAgent(stateDir)
}

// agentIDErr words a creds.AgentID failure; a machine mismatch speaks for
// itself.
func agentIDErr(err error) error {
	var mm *creds.MachineMismatchError
	if errors.As(err, &mm) {
		return err
	}
	return fmt.Errorf("agent identity: %w", err)
}
