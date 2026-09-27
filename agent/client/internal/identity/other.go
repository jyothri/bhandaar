//go:build !linux && !darwin

package identity

import (
	"errors"
	"runtime"
)

// Detect isn't supported on this OS; scans go ahead without an identity.
func Detect(root string) (Identity, error) {
	return Identity{}, errors.New("drive identity isn't supported on " + runtime.GOOS)
}

// DiskKeys can't find the disk on this OS: the key is the path.
func DiskKeys(root string) []string {
	return fallbackKeys(root, Identity{})
}
