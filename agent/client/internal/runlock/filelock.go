package runlock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// fileLock is a flock(2) on one open file, which it keeps open whether
// locked or not: the instance lock needs its fd, to hand it to the new
// binary across exec (docs/specs/agent-auto-update.md, "Handing the lock
// to the new binary"), which gofrs/flock doesn't expose. Older driveagents
// take the same lock through gofrs/flock, which is flock(2) too.
//
// It never converts a lock in place: flock(2) conversion drops the old
// lock before trying the new one, and a failed conversion would leave
// nothing held. Callers unlock, then lock, under instance.gate.
type fileLock struct {
	f *os.File
}

func openFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// try takes the lock (exclusive, or shared) without waiting; false if
// someone else holds it.
func (l *fileLock) try(exclusive bool) (bool, error) {
	how := unix.LOCK_SH
	if exclusive {
		how = unix.LOCK_EX
	}
	for {
		err := unix.Flock(int(l.f.Fd()), how|unix.LOCK_NB)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, unix.EINTR):
			continue
		case errors.Is(err, unix.EWOULDBLOCK):
			return false, nil
		default:
			return false, err
		}
	}
}

func (l *fileLock) unlock() error {
	return unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
}

// close releases the lock, if held, with the file.
func (l *fileLock) close() error {
	return l.f.Close()
}
