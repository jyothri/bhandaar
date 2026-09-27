package runlock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

// DiskHolder describes the scan holding a physical-drive lock.
type DiskHolder struct {
	Key     string    `json:"key"`
	DriveID string    `json:"drive_id"`
	Path    string    `json:"path"`
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
}

// DiskBusyError is a physical-drive lock held by another scan. Holder is
// its sidecar, zero apart from Key if that couldn't be read.
type DiskBusyError struct {
	Holder DiskHolder
}

func (e *DiskBusyError) Is(target error) bool { return target == ErrBusy }

func (e *DiskBusyError) Error() string {
	h := e.Holder
	if h.DriveID == "" {
		return fmt.Sprintf("another scan is reading this disk (%s)", DescribeKey(h.Key))
	}
	return fmt.Sprintf("another scan is reading this disk (%s): drive %s, %s (pid %d, since %s)",
		DescribeKey(h.Key), h.DriveID, h.Path, h.PID, since(h.Started))
}

// DescribeKey says what a disk key (identity.DiskKeys) identifies:
// "serial:NA77YET6" is "serial NA77YET6".
func DescribeKey(key string) string {
	kind, v, _ := strings.Cut(key, ":")
	switch kind {
	case "serial":
		return "serial " + v
	case "dev":
		return "device " + v
	case "fs":
		return "filesystem " + v
	case "path":
		return "path " + v
	}
	return key
}

func diskLockPath(dir, key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(dir, "disk-"+hex.EncodeToString(sum[:])[:16]+".lock")
}

func diskInfoPath(dir, key string) string {
	return strings.TrimSuffix(diskLockPath(dir, key), ".lock") + ".json"
}

// LockDisks takes the physical-drive lock of every key, in sorted order
// (so two scans spanning the same disks can't deadlock), and returns their
// release. A key held by another scan fails with a *DiskBusyError, unless
// wait: then waiting (if not nil) is called once with that error, and
// LockDisks waits for the lock until ctx is done. holder describes this
// scan; its Key is filled in per lock.
func LockDisks(ctx context.Context, dir string, keys []string, holder DiskHolder, wait bool, waiting func(*DiskBusyError)) (release func(), err error) {
	keys = slices.Compact(slices.Sorted(slices.Values(keys)))
	var held []*flock.Flock
	release = func() {
		for _, l := range slices.Backward(held) {
			l.Unlock()
		}
	}
	for _, key := range keys {
		l := flock.New(diskLockPath(dir, key))
		ok, err := l.TryLock()
		if err != nil {
			release()
			return nil, fmt.Errorf("locking the disk (%s): %w", DescribeKey(key), err)
		}
		if !ok {
			busy := &DiskBusyError{Holder: DiskHolder{Key: key}}
			if readSidecar(diskInfoPath(dir, key), &busy.Holder) {
				busy.Holder.Key = key
			}
			if !wait {
				release()
				return nil, busy
			}
			if waiting != nil {
				waiting(busy)
				waiting = nil
			}
			if ok, err = l.TryLockContext(ctx, 200*time.Millisecond); err != nil || !ok {
				release()
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("locking the disk (%s): %w", DescribeKey(key), err)
			}
		}
		held = append(held, l)
		h := holder
		h.Key = key
		if err := writeSidecar(diskInfoPath(dir, key), h); err != nil {
			release()
			return nil, fmt.Errorf("recording the disk lock's holder: %w", err)
		}
	}
	return release, nil
}
