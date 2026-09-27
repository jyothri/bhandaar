package identity

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// Disk keys name the physical disk(s) holding a drive root, for the
// one-scan-per-disk lock (docs/archive/agent-hardening.md, "Goal 2: the
// physical-drive lock"). DiskKeys (per OS) returns, per disk, the first of:
//
//	serial:<HWSerial>    the disk's serial (generic ones count as missing)
//	dev:<whole disk>     the whole-disk device: 8:16 on Linux, disk4 on macOS
//
// and, when no disk can be found at all (a network or virtual filesystem,
// an unsupported OS):
//
//	fs:<source>:<FSUUID> the filesystem ID
//	path:<drive root>    absolute, symlinks resolved
//
// The first two are per disk, so every partition of a disk gets the same
// key; the fallbacks keep apart only scans of one filesystem or mount. A
// filesystem spanning several disks (LVM, RAID) gets a key per disk.
// DiskKeys never fails: whatever can't be read falls through to the next
// kind, and a root that doesn't exist gets a path key (the scan then fails
// on its own checks).

// IsDiskKey reports whether key names a disk (serial or device) rather
// than only a filesystem or a path.
func IsDiskKey(key string) bool {
	return strings.HasPrefix(key, "serial:") || strings.HasPrefix(key, "dev:")
}

// fallbackKeys is the key when no disk was found: the filesystem ID if
// there is one, else the path.
func fallbackKeys(root string, id Identity) []string {
	if id.FSUUID != "" {
		return []string{"fs:" + id.Source + ":" + id.FSUUID}
	}
	return []string{"path:" + resolvedPath(root)}
}

func resolvedPath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		return filepath.Clean(root)
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// diskKeysLinux: stat gives the filesystem's device number; sysfs
// (/sys/dev/block/<maj>:<min>) gives the whole disk(s) behind it, and each
// disk's udev record its serial.
func diskKeysLinux(root string, env linuxEnv) []string {
	major, minor, err := env.dev(root)
	if err != nil {
		return fallbackKeys(root, Identity{})
	}
	devNum := fmt.Sprintf("%d:%d", major, minor)
	part := env.udev(devNum)

	disks := env.wholeDisks(devNum, 0)
	if len(disks) == 0 {
		return fallbackKeys(root, Identity{Source: "linux", FSUUID: NormalizeFSUUID(part["ID_FS_UUID"])})
	}
	var keys []string
	for _, d := range disks {
		serial := NormalizeSerial(env.udev(d)["ID_SERIAL_SHORT"])
		if serial == "" && len(disks) == 1 {
			// A partition's udev record carries its disk's serial too.
			serial = NormalizeSerial(part["ID_SERIAL_SHORT"])
		}
		if serial != "" {
			keys = append(keys, "serial:"+serial)
		} else {
			keys = append(keys, "dev:"+d)
		}
	}
	return slices.Compact(slices.Sorted(slices.Values(keys)))
}

// udev returns the properties of the udev record of block device devNum,
// or none.
func (env linuxEnv) udev(devNum string) map[string]string {
	b, err := env.readFile(env.udevDir + "/b" + devNum)
	if err != nil {
		return map[string]string{}
	}
	return parseUdev(b)
}

// wholeDisks returns the whole disks ("8:16") behind block device devNum:
// the device itself if it's a whole disk, a partition's parent, or, for a
// device-mapper or md device, the whole disks behind each of its slaves.
// None if devNum isn't a block device (tmpfs, overlay, NFS).
func (env linuxEnv) wholeDisks(devNum string, depth int) []string {
	if env.evalSymlinks == nil || depth > 8 {
		return nil
	}
	dir, err := env.evalSymlinks(env.sysDir + "/dev/block/" + devNum)
	if err != nil {
		return nil
	}
	if slaves, _ := env.readDir(dir + "/slaves"); len(slaves) > 0 {
		var out []string
		for _, s := range slaves {
			if b, err := env.readFile(dir + "/slaves/" + s + "/dev"); err == nil {
				out = append(out, env.wholeDisks(strings.TrimSpace(string(b)), depth+1)...)
			}
		}
		return slices.Compact(slices.Sorted(slices.Values(out)))
	}
	if _, err := env.readFile(dir + "/partition"); err == nil {
		b, err := env.readFile(filepath.Dir(dir) + "/dev")
		if err != nil {
			return nil
		}
		return []string{strings.TrimSpace(string(b))}
	}
	return []string{devNum}
}

// diskKeysMacOS uses what detectMacOS reads: the USB serial, else the
// whole disk behind the volume.
func diskKeysMacOS(root string, run runFunc) []string {
	id, disk, err := detectMacOSDisk(root, run)
	switch {
	case err != nil:
		return fallbackKeys(root, Identity{})
	case id.HWSerial != "":
		return []string{"serial:" + id.HWSerial}
	case disk != "":
		return []string{"dev:" + disk}
	}
	return fallbackKeys(root, id)
}
