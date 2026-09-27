package creds

import (
	"regexp"
	"strings"
)

var ioPlatformUUID = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

// parseIOPlatformUUID finds IOPlatformUUID in the output of
// "ioreg -rd1 -c IOPlatformExpertDevice".
func parseIOPlatformUUID(out []byte) string {
	if m := ioPlatformUUID.FindSubmatch(out); m != nil {
		return normalizeMachineID(string(m[1]))
	}
	return ""
}

func normalizeMachineID(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
