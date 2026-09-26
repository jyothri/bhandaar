// Package identity reads a drive's filesystem ID and hardware serial, read
// only and without root, so copies of one physical drive scanned from
// different machines can be linked, and a scan can refuse the wrong drive
// under a familiar --drive-id. See docs/specs/remote-sync-agent.md, "Drive
// identity".
package identity

import (
	"fmt"
	"strings"
)

// Identity is what was found for (or is stored for) a drive. Any field can be
// empty.
type Identity struct {
	FSUUID   string // normalised: uppercase, no dashes
	FSType   string // lowercase, e.g. ntfs, ext4, apfs, exfat
	Source   string // "linux" or "macos": where FSUUID came from
	HWSerial string // normalised; empty if missing or generic
}

// Empty reports whether nothing was found.
func (i Identity) Empty() bool { return i.FSUUID == "" && i.HWSerial == "" }

func (i Identity) String() string {
	var parts []string
	if i.FSUUID != "" {
		parts = append(parts, fmt.Sprintf("filesystem %s (%s, %s)", i.FSUUID, orUnknown(i.FSType), i.Source))
	}
	if i.HWSerial != "" {
		parts = append(parts, "serial "+i.HWSerial)
	}
	if len(parts) == 0 {
		return "nothing"
	}
	return strings.Join(parts, ", ")
}

func orUnknown(s string) string {
	if s == "" {
		return "type unknown"
	}
	return s
}

// NormalizeFSUUID uppercases and drops dashes, so IDs compare the same
// however a tool printed them.
func NormalizeFSUUID(s string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
}

// genericSerials are placeholders some USB-SATA bridges report instead of a
// real serial; they're treated as missing.
var genericSerials = map[string]bool{
	"0123456789ABCDEF": true, "0123456789": true, "123456789": true, "1234567890": true,
	"123456789ABC": true, "123456789012": true, "NONE": true, "NULL": true, "N/A": true,
	"DEFAULT": true, "DEFAULT STRING": true, "TO BE FILLED BY O.E.M.": true, "NOT AVAILABLE": true,
	"SERIAL": true, "0X0": true,
}

// NormalizeSerial trims and uppercases a serial, and returns "" for a
// missing or generic one (all one repeated character, like 000000000000, or
// a known placeholder).
func NormalizeSerial(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || genericSerials[s] {
		return ""
	}
	if strings.Count(s, s[:1]) == len(s) {
		return ""
	}
	return s
}

// normalizeFSType maps kernel driver names to the filesystem they serve, so
// one NTFS drive has one type however it was mounted. fuseblk is left as is:
// on its own it doesn't say which filesystem the FUSE driver serves.
func normalizeFSType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	switch t {
	case "ntfs3", "ntfs-3g":
		return "ntfs"
	case "msdos", "fat", "fat32", "vfat":
		return "vfat"
	}
	return t
}
