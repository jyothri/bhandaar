package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// Lock is a drive's upload lock: an exclusive flock on
// <state-dir>/upload-<sha256(drive_id)[:16]>.lock, held while uploading
// the drive. It keeps a scan and a sync from interleaving writes to the
// drive's marker; reconciling happens only under it.
type Lock struct {
	f *flock.Flock
}

// LockPath is the drive's lock file.
func LockPath(stateDir, driveID string) string {
	sum := sha256.Sum256([]byte(driveID))
	return filepath.Join(stateDir, "upload-"+hex.EncodeToString(sum[:])[:16]+".lock")
}

// TryLock takes the drive's lock if it's free; ok is false if another
// process holds it. (sync skips such a drive.)
func TryLock(stateDir, driveID string) (l *Lock, ok bool, err error) {
	f := flock.New(LockPath(stateDir, driveID))
	if ok, err = f.TryLock(); err != nil {
		return nil, false, fmt.Errorf("locking drive %q for upload: %w", driveID, err)
	}
	if !ok {
		return nil, false, nil
	}
	return &Lock{f: f}, true, nil
}

// WaitLock takes the drive's lock, waiting while another process holds
// it; waiting (if not nil) is called once, when it has to wait. (scan
// waits for a sync of its drive.)
func WaitLock(ctx context.Context, stateDir, driveID string, waiting func()) (*Lock, error) {
	l, ok, err := TryLock(stateDir, driveID)
	if err != nil || ok {
		return l, err
	}
	if waiting != nil {
		waiting()
	}
	f := flock.New(LockPath(stateDir, driveID))
	if ok, err = f.TryLockContext(ctx, 200*time.Millisecond); err != nil {
		return nil, fmt.Errorf("locking drive %q for upload: %w", driveID, err)
	}
	if !ok {
		return nil, ctx.Err()
	}
	return &Lock{f: f}, nil
}

// Unlock releases the lock.
func (l *Lock) Unlock() error { return l.f.Unlock() }
