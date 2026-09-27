package runlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// Files of the instance lock, in the lock dir.
const (
	instanceGate = "instance.gate" // exclusive, held for milliseconds while joining
	instanceLock = "instance.lock" // shared, held for each process's lifetime
	instanceInfo = "instance.json" // what the running processes are
)

// gateTimeout bounds the wait for instance.gate; it's only ever held for
// the few file operations of a join.
var gateTimeout = 10 * time.Second

// Instance describes the driveagent processes running on the machine: the
// state dir and version they all share. It's written by the first of them.
type Instance struct {
	StateDir string    `json:"state_dir"`
	Version  string    `json:"version"`
	PID      int       `json:"pid"`
	Started  time.Time `json:"started"`
	// Alone is set by a process that needs the state dir to itself
	// ("login --new-agent"); nothing else can join while it runs.
	Alone bool `json:"alone,omitempty"`
}

// InstanceBusyError is a refused join: a driveagent with another state dir
// or version is running, or one that needs to be alone. Running is the
// zero Instance if its description couldn't be read.
type InstanceBusyError struct {
	Running Instance
	// Ours is what the refused process would have run as.
	Ours Instance
}

func (e *InstanceBusyError) Is(target error) bool { return target == ErrBusy }

func (e *InstanceBusyError) Error() string {
	r := e.Running
	if r.StateDir == "" {
		return "another driveagent is running on this machine; wait for it to finish"
	}
	who := fmt.Sprintf("driveagent %s, pid %d, since %s", r.Version, r.PID, since(r.Started))
	switch {
	case r.Alone:
		return fmt.Sprintf("\"driveagent login --new-agent\" is running on this machine (%s); wait for it to finish", who)
	case e.Ours.Alone:
		return fmt.Sprintf("another driveagent is running on this machine (%s, state dir %s).\n"+
			"\"login --new-agent\" needs the state dir to itself: wait for it to finish", who, r.StateDir)
	case r.StateDir != e.Ours.StateDir:
		return fmt.Sprintf("another driveagent is running on this machine with state dir %s (%s).\n"+
			"One machine runs one state dir: wait for it to finish, or pass the same --state-dir", r.StateDir, who)
	default:
		return fmt.Sprintf("driveagent %s is running on this machine (pid %d, since %s); this is %s.\n"+
			"Wait for it to finish before running a different version", r.Version, r.PID, since(r.Started), e.Ours.Version)
	}
}

// JoinInstance takes the instance lock for a process with stateDir and
// version, in the lock dir dir, and returns its release. Every driveagent
// running at once on the machine must share one state dir and one version;
// a process that doesn't is refused with an *InstanceBusyError. With
// alone, the process must be the only one running, and keeps everyone else
// out until it releases.
//
// Joining happens under instance.gate: try instance.lock exclusively; if
// that works, nothing else is running, so write instance.json and hold the
// lock shared (exclusively if alone). Otherwise read instance.json and join
// (shared) only if it matches. Every join passes through the gate, so the
// switch from exclusive to shared can't interleave with another's check,
// and instance.json is only rewritten when no one else is running.
func JoinInstance(ctx context.Context, dir, stateDir, version string, alone bool) (release func(), err error) {
	ours := Instance{StateDir: canonical(stateDir), Version: version, PID: os.Getpid(), Started: time.Now().UTC(), Alone: alone}

	gate := flock.New(filepath.Join(dir, instanceGate))
	gctx, cancel := context.WithTimeout(ctx, gateTimeout)
	ok, err := gate.TryLockContext(gctx, 20*time.Millisecond)
	cancel()
	if err != nil || !ok {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("taking the instance lock (%s): %v", filepath.Join(dir, instanceGate), firstNonNil(err, gctx.Err()))
	}
	defer gate.Unlock()

	lock := flock.New(filepath.Join(dir, instanceLock))
	info := filepath.Join(dir, instanceInfo)
	ok, err = lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("taking the instance lock: %w", err)
	}
	if ok {
		// Nothing else is running.
		if err := writeSidecar(info, ours); err != nil {
			lock.Unlock()
			return nil, fmt.Errorf("writing %s: %w", info, err)
		}
		if alone {
			return func() { lock.Unlock() }, nil
		}
		if err := lock.Unlock(); err != nil {
			return nil, err
		}
	} else {
		var running Instance
		if !readSidecar(info, &running) || alone || running.Alone ||
			running.StateDir != ours.StateDir || running.Version != ours.Version {
			return nil, &InstanceBusyError{Running: running, Ours: ours}
		}
	}
	if ok, err = lock.TryRLock(); err != nil || !ok {
		// Only a process joining alone holds it exclusively, and it can't
		// have got in since the check above: we hold the gate.
		return nil, fmt.Errorf("taking the instance lock shared: %v", firstNonNil(err, errors.New("busy")))
	}
	return func() { lock.Unlock() }, nil
}

// canonical makes a state dir comparable: absolute, with symlinks resolved
// where it exists.
func canonical(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

func firstNonNil(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
