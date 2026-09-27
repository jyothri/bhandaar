//go:build linux

package creds

import "os"

// machineID is /etc/machine-id (or D-Bus's copy of it).
func machineID() string {
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if b, err := os.ReadFile(p); err == nil {
			if id := normalizeMachineID(string(b)); id != "" {
				return id
			}
		}
	}
	return ""
}
