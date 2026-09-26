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
