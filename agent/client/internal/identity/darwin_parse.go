package identity

import (
	"fmt"
	"regexp"
)

// run runs an external command and returns its standard output.
type runFunc func(name string, args ...string) ([]byte, error)

// detectMacOS finds root's identity from diskutil and ioreg. UNVERIFIED: it
// follows Apple's documented plist formats and has been tested only against
// hand-written fixtures (testdata/macos), not a real Mac.
//
//   - diskutil info -plist <root>: VolumeUUID and FilesystemType, and the
//     whole disk behind the volume (ParentWholeDisk, or for APFS the first
//     APFSPhysicalStores entry, since an APFS volume's parent is a
//     synthesized container disk).
//   - ioreg -a -r -c IOUSBHostDevice: the USB devices, each with its
//     registry subtree; the one whose subtree has a "BSD Name" equal to
//     that whole disk carries the serial ("USB Serial Number").
//
// macOS reports a synthesized VolumeUUID for FAT, exFAT and NTFS, so those
// only match other macOS agents (Source "macos"); the server links them
// accordingly.
func detectMacOS(root string, run runFunc) (Identity, error) {
	out, err := run("diskutil", "info", "-plist", root)
	if err != nil {
		return Identity{}, fmt.Errorf("diskutil info %s: %w", root, err)
	}
	v, err := parsePlist(out)
	if err != nil {
		return Identity{}, fmt.Errorf("diskutil info %s: %w", root, err)
	}
	info, _ := v.(map[string]any)
	id := Identity{
		Source: "macos",
		FSUUID: NormalizeFSUUID(str(info["VolumeUUID"])),
		FSType: normalizeFSType(str(info["FilesystemType"])),
	}

	disk := wholeDisk(info)
	if disk == "" {
		return id, nil
	}
	out, err = run("ioreg", "-a", "-r", "-c", "IOUSBHostDevice")
	if err != nil {
		return id, nil // not USB, or ioreg failed: no serial
	}
	v, err = parsePlist(out)
	if err != nil {
		return id, nil
	}
	devices, _ := v.([]any)
	for _, dev := range devices {
		d, _ := dev.(map[string]any)
		if d != nil && hasBSDName(d, disk) {
			serial := str(d["USB Serial Number"])
			if serial == "" {
				serial = str(d["kUSBSerialNumberString"])
			}
			id.HWSerial = NormalizeSerial(serial)
			break
		}
	}
	return id, nil
}

var partitionSuffix = regexp.MustCompile(`s\d+$`)

// wholeDisk returns the physical whole disk ("disk4") behind a volume.
func wholeDisk(info map[string]any) string {
	if stores, ok := info["APFSPhysicalStores"].([]any); ok && len(stores) > 0 {
		if s, ok := stores[0].(map[string]any); ok {
			if dev := str(s["APFSPhysicalStore"]); dev != "" {
				return partitionSuffix.ReplaceAllString(dev, "")
			}
		}
	}
	if p := str(info["ParentWholeDisk"]); p != "" {
		return p
	}
	return partitionSuffix.ReplaceAllString(str(info["DeviceIdentifier"]), "")
}

// hasBSDName reports whether the registry subtree d contains an entry with
// the given "BSD Name".
func hasBSDName(d map[string]any, name string) bool {
	if str(d["BSD Name"]) == name {
		return true
	}
	children, _ := d["IORegistryEntryChildren"].([]any)
	for _, c := range children {
		if cd, ok := c.(map[string]any); ok && hasBSDName(cd, name) {
			return true
		}
	}
	return false
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
