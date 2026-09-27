//go:build !linux && !darwin

package creds

// machineID isn't known on this OS: the state dir isn't bound to a machine.
func machineID() string { return "" }
