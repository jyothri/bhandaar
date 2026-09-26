//go:build darwin

package identity

import (
	"context"
	"os/exec"
	"time"
)

// Detect reads the identity of the drive holding root. diskutil and ioreg
// are the only external commands the agent runs.
func Detect(root string) (Identity, error) {
	return detectMacOS(root, func(name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).Output()
	})
}
