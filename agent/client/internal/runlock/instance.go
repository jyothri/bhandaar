package runlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"golang.org/x/sys/unix"
)

// Files of the instance lock, in the lock dir.
const (
	instanceGate = "instance.gate" // exclusive, held for milliseconds while joining
	instanceLock = "instance.lock" // shared, held for each process's lifetime
	instanceInfo = "instance.json" // what the running processes are
)

// gateTimeout bounds the wait for instance.gate; it's only ever held for
// the few file operations of a join, except during an update (below).
var gateTimeout = 10 * time.Second

// While a driveagent updates itself it holds instance.gate and instance.lock
// exclusively, for as long as the download takes, and says so in
// instance.json (docs/specs/agent-auto-update.md, "The instance lock during
// an update"). A joiner waits for it, until updateWait after it began, then
// gives up: a hung updater or a stale instance.json mustn't block cron
// forever. The download timeouts bound an update at about 6.5 minutes.
// pollInterval is how often a waiting joiner looks again. Tests change
// both.
var (
	updateWait   = 10 * time.Minute
	pollInterval = 200 * time.Millisecond
)

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
	// UpdatingSince is set while the process replaces its binary
	// (TryAlone); joiners wait for it.
	UpdatingSince *time.Time `json:"updating_since,omitempty"`
}

// updating reports whether the instance is updating itself, and for how
// much longer joiners should wait for it.
func (i Instance) updating(now time.Time) (bool, time.Duration) {
	if i.UpdatingSince == nil {
		return false, 0
	}
	left := i.UpdatingSince.Add(updateWait).Sub(now)
	return left > 0, left
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
// version, in the lock dir dir, and returns its release. See Join.
func JoinInstance(ctx context.Context, dir, stateDir, version string, alone bool) (release func(), err error) {
	m, err := Join(ctx, dir, stateDir, version, alone, nil)
	if err != nil {
		return nil, err
	}
	return m.Release, nil
}

// Membership is a process's place in the instance: its hold on
// instance.lock, shared (exclusive if alone, or while it updates itself).
type Membership struct {
	dir   string
	ours  Instance
	lock  *fileLock
	gate  *flock.Flock // held while updating, between TryAlone and the exec
	alone bool         // joined alone: instance.lock is exclusive throughout
}

// Join takes the instance lock for a process with stateDir and version, in
// the lock dir dir. Every driveagent running at once on the machine must
// share one state dir and one version; a process that doesn't is refused
// with an *InstanceBusyError. With alone, the process must be the only one
// running, and keeps everyone else out until it releases. While another
// driveagent updates itself, Join waits for it, calling onWait (if not nil)
// once.
//
// Joining happens under instance.gate: try instance.lock exclusively; if
// that works, nothing else is running, so write instance.json and hold the
// lock shared (exclusively if alone). Otherwise read instance.json and join
// (shared) only if it matches. Every join passes through the gate, so the
// switch from exclusive to shared can't interleave with another's check,
// and instance.json is only rewritten when no one else is running.
func Join(ctx context.Context, dir, stateDir, version string, alone bool, onWait func(Instance)) (*Membership, error) {
	ours := Instance{StateDir: canonical(stateDir), Version: version, PID: os.Getpid(), Started: time.Now().UTC(), Alone: alone}
	info := filepath.Join(dir, instanceInfo)
	waited := false
	wait := func(running Instance) {
		if !waited && onWait != nil {
			onWait(running)
		}
		waited = true
	}
	for {
		gate, err := takeGate(ctx, dir, wait)
		if err != nil {
			return nil, err
		}
		m, running, err := joinUnderGate(dir, info, ours, alone)
		gate.Unlock()
		if err != nil || m != nil {
			return m, err
		}
		// Busy, and updating: wait for the updater to finish, then look
		// again.
		wait(running)
		if err := sleep(ctx, pollInterval); err != nil {
			return nil, err
		}
	}
}

// joinUnderGate is one attempt at joining, holding the gate. It returns
// the membership, or the running instance when that is updating itself
// and the joiner should wait, or an error.
func joinUnderGate(dir, info string, ours Instance, alone bool) (*Membership, Instance, error) {
	lock, err := openFileLock(filepath.Join(dir, instanceLock))
	if err != nil {
		return nil, Instance{}, fmt.Errorf("opening the instance lock: %w", err)
	}
	ok, err := lock.try(true)
	if err != nil {
		lock.close()
		return nil, Instance{}, fmt.Errorf("taking the instance lock: %w", err)
	}
	if ok {
		// Nothing else is running.
		if err := writeSidecar(info, ours); err != nil {
			lock.close()
			return nil, Instance{}, fmt.Errorf("writing %s: %w", info, err)
		}
		if alone {
			return &Membership{dir: dir, ours: ours, lock: lock, alone: true}, Instance{}, nil
		}
		if err := lock.unlock(); err != nil {
			lock.close()
			return nil, Instance{}, err
		}
	} else {
		var running Instance
		readable := readSidecar(info, &running)
		if up, _ := running.updating(time.Now()); readable && up {
			lock.close()
			return nil, running, nil
		}
		if !readable || alone || running.Alone ||
			running.StateDir != ours.StateDir || running.Version != ours.Version {
			lock.close()
			return nil, Instance{}, &InstanceBusyError{Running: running, Ours: ours}
		}
	}
	if ok, err = lock.try(false); err != nil || !ok {
		// Only a process joining alone, or updating, holds it exclusively,
		// and neither can have got in since the check above: we hold the
		// gate.
		lock.close()
		return nil, Instance{}, fmt.Errorf("taking the instance lock shared: %v", firstNonNil(err, errors.New("busy")))
	}
	return &Membership{dir: dir, ours: ours, lock: lock}, Instance{}, nil
}

// takeGate takes instance.gate, waiting up to gateTimeout; longer while
// instance.json says a driveagent is updating itself, calling wait.
func takeGate(ctx context.Context, dir string, wait func(Instance)) (*flock.Flock, error) {
	gate := flock.New(filepath.Join(dir, instanceGate))
	limit := gateTimeout
	for {
		gctx, cancel := context.WithTimeout(ctx, limit)
		ok, err := gate.TryLockContext(gctx, 20*time.Millisecond)
		cancel()
		if err == nil && ok {
			return gate, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var running Instance
		if readSidecar(filepath.Join(dir, instanceInfo), &running) {
			if up, left := running.updating(time.Now()); up {
				wait(running)
				limit = left
				continue
			}
		}
		return nil, fmt.Errorf("taking the instance lock (%s): %v", filepath.Join(dir, instanceGate), firstNonNil(err, gctx.Err()))
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// Release leaves the instance.
func (m *Membership) Release() {
	if m.gate != nil {
		m.gate.Unlock()
		m.gate = nil
	}
	m.lock.close()
}

// TryAlone checks whether this is the only driveagent running, before it
// updates itself (docs/specs/agent-auto-update.md, "Checking it's alone").
// If so, it returns true holding instance.gate and instance.lock
// exclusively, with instance.json saying it's updating, so no one can join
// until EndUpdate or HandOver. If not, it returns false still joined
// (shared). It never converts the lock in place: under the gate, it
// unlocks, tries exclusive, and if that fails re-takes shared at once.
func (m *Membership) TryAlone(ctx context.Context) (bool, error) {
	if m.gate != nil {
		return false, errors.New("already updating")
	}
	gate, err := takeGate(ctx, m.dir, func(Instance) {})
	if err != nil {
		return false, err
	}
	if !m.alone {
		if err := m.lock.unlock(); err != nil {
			gate.Unlock()
			return false, err
		}
		ok, err := m.lock.try(true)
		if err != nil || !ok {
			// Others are running: back to shared, still under the gate.
			// Nobody can hold the lock exclusively meanwhile, so this
			// can't fail but for an I/O error, which leaves this process
			// outside the instance: fatal.
			back, rerr := m.lock.try(false)
			gate.Unlock()
			if rerr != nil || !back {
				return false, fmt.Errorf("re-taking the instance lock: %v", firstNonNil(rerr, errors.New("busy")))
			}
			return false, err
		}
	}
	now := time.Now().UTC()
	updating := m.ours
	updating.PID, updating.UpdatingSince = os.Getpid(), &now
	if err := writeSidecar(filepath.Join(m.dir, instanceInfo), updating); err != nil {
		m.gate = gate
		m.EndUpdate()
		return false, fmt.Errorf("writing %s: %w", instanceInfo, err)
	}
	m.gate = gate
	return true, nil
}

// EndUpdate goes back to how things were before TryAlone, after an update
// that didn't happen: instance.json without "updating", the lock shared
// (unless joined alone), the gate released.
func (m *Membership) EndUpdate() error {
	if m.gate == nil {
		return nil
	}
	defer func() {
		m.gate.Unlock()
		m.gate = nil
	}()
	if err := writeSidecar(filepath.Join(m.dir, instanceInfo), m.ours); err != nil {
		return err
	}
	if m.alone {
		return nil
	}
	if err := m.lock.unlock(); err != nil {
		return err
	}
	if ok, err := m.lock.try(false); err != nil || !ok {
		return fmt.Errorf("re-taking the instance lock: %v", firstNonNil(err, errors.New("busy")))
	}
	return nil
}

// HandOver readies the exclusive instance.lock for the new binary, after a
// successful TryAlone (docs/specs/agent-auto-update.md, "Handing the lock
// to the new binary"): its fd stays open across exec, and the gate is
// released. A joiner then finds the lock held and instance.json saying
// "updating", and waits. It returns the fd, for EnvInstanceFD.
func (m *Membership) HandOver() (uintptr, error) {
	if m.gate == nil {
		return 0, errors.New("not updating")
	}
	fd := m.lock.f.Fd()
	if _, err := unix.FcntlInt(fd, unix.F_SETFD, 0); err != nil {
		return 0, fmt.Errorf("keeping the instance lock across exec: %w", err)
	}
	m.gate.Unlock()
	m.gate = nil
	return fd, nil
}

// TakeBack undoes HandOver when the exec fails: the fd is closed on exec
// again, and the update ends as in EndUpdate.
func (m *Membership) TakeBack(ctx context.Context) error {
	unix.CloseOnExec(int(m.lock.f.Fd()))
	gate, err := takeGate(ctx, m.dir, func(Instance) {})
	if err != nil {
		return err
	}
	m.gate = gate
	return m.EndUpdate()
}

// ErrNotInstanceLock is an fd, handed over by EnvInstanceFD, that isn't
// the lock dir's instance.lock.
var ErrNotInstanceLock = errors.New("the handed-over fd isn't the instance lock")

// Adopt takes over the instance lock a driveagent handed to this one
// across exec, held exclusively (HandOver): it records this process's
// version in instance.json, then holds the lock shared, as Join would.
// The fd is closed on any error.
func Adopt(ctx context.Context, dir string, fd uintptr, stateDir, version string) (*Membership, error) {
	f := os.NewFile(fd, filepath.Join(dir, instanceLock))
	if f == nil {
		return nil, ErrNotInstanceLock
	}
	fi, err := f.Stat()
	want, werr := os.Stat(filepath.Join(dir, instanceLock))
	if err != nil || werr != nil || !os.SameFile(fi, want) {
		f.Close()
		return nil, ErrNotInstanceLock
	}
	unix.CloseOnExec(int(fd))
	lock := &fileLock{f: f}
	// Exclusive, as handed over: holding it is what keeps everyone out.
	if ok, err := lock.try(true); err != nil || !ok {
		f.Close()
		return nil, fmt.Errorf("the handed-over instance lock isn't held: %v", firstNonNil(err, errors.New("busy")))
	}
	gate, err := takeGate(ctx, dir, func(Instance) {})
	if err != nil {
		f.Close()
		return nil, err
	}
	defer gate.Unlock()
	m := &Membership{dir: dir, ours: Instance{StateDir: canonical(stateDir), Version: version, PID: os.Getpid(), Started: time.Now().UTC()}, lock: lock}
	if err := writeSidecar(filepath.Join(dir, instanceInfo), m.ours); err != nil {
		f.Close()
		return nil, fmt.Errorf("writing %s: %w", instanceInfo, err)
	}
	if err := lock.unlock(); err != nil {
		f.Close()
		return nil, err
	}
	if ok, err := lock.try(false); err != nil || !ok {
		f.Close()
		return nil, fmt.Errorf("taking the instance lock shared: %v", firstNonNil(err, errors.New("busy")))
	}
	return m, nil
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
