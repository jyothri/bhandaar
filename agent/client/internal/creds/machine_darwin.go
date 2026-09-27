//go:build darwin

package creds

import (
	"context"
	"os/exec"
	"time"
)

// machineID is the Mac's IOPlatformUUID.
func machineID() string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return ""
	}
	return parseIOPlatformUUID(out)
}
