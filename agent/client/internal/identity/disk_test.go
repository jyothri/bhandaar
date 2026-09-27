package identity

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fakeSysfs builds a /sys with the block devices below in a temp dir:
//
//	sdb  8:16  disk, udev serial NA77YET6; partitions sdb1 8:17, sdb2 8:18
//	sdc  8:32  disk, no udev record; partitions sdc1 8:33 (its udev record has
//	           serial NA95K2KP), sdc2 8:34 (no udev record)
//	sdd  8:48  disk, generic serial; partition sdd1 8:49
//	sde  8:64, sdf 8:80  disks, serials E1 and F1
//	dm-0 253:0  LUKS over sdd1
//	dm-1 253:1  LVM over sde and sdf
//	dm-2 253:2  over dm-0 (LVM on LUKS)
//	loop0 7:0   whole disk, nothing in udev
func fakeSysfs(t *testing.T) linuxEnv {
	t.Helper()
	sys := t.TempDir()
	block := filepath.Join(sys, "devices/virtual-and-pci/block")
	write := func(path, content string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, name string) {
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, name); err != nil {
			t.Fatal(err)
		}
	}
	dev := func(rel, num string, partition bool) {
		dir := filepath.Join(block, rel)
		write(filepath.Join(dir, "dev"), num+"\n")
		if partition {
			write(filepath.Join(dir, "partition"), "1\n")
		}
		link(dir, filepath.Join(sys, "dev/block", num))
	}
	dev("sdb", "8:16", false)
	dev("sdb/sdb1", "8:17", true)
	dev("sdb/sdb2", "8:18", true)
	dev("sdc", "8:32", false)
	dev("sdc/sdc1", "8:33", true)
	dev("sdc/sdc2", "8:34", true)
	dev("sdd", "8:48", false)
	dev("sdd/sdd1", "8:49", true)
	dev("sde", "8:64", false)
	dev("sdf", "8:80", false)
	dev("dm-0", "253:0", false)
	link(filepath.Join(block, "sdd/sdd1"), filepath.Join(block, "dm-0/slaves/sdd1"))
	dev("dm-1", "253:1", false)
	link(filepath.Join(block, "sde"), filepath.Join(block, "dm-1/slaves/sde"))
	link(filepath.Join(block, "sdf"), filepath.Join(block, "dm-1/slaves/sdf"))
	dev("dm-2", "253:2", false)
	link(filepath.Join(block, "dm-0"), filepath.Join(block, "dm-2/slaves/dm-0"))
	dev("loop0", "7:0", false)

	udev := map[string]string{
		"b8:16": "E:ID_SERIAL_SHORT=NA77YET6\n",
		"b8:17": "E:ID_SERIAL_SHORT=NA77YET6\nE:ID_FS_UUID=E856-BF7E\n",
		"b8:33": "E:ID_SERIAL_SHORT=NA95K2KP\nE:ID_FS_UUID=8650-E771\n",
		"b8:48": "E:ID_SERIAL_SHORT=0123456789ABCDEF\n",
		"b8:64": "E:ID_SERIAL_SHORT=E1\n",
		"b8:80": "E:ID_SERIAL_SHORT=F1\n",
		"b0:25": "E:ID_FS_UUID=abcd-ef01\n", // not a block device, but has a filesystem ID
	}
	env := linuxEnv{sysDir: sys, udevDir: "/run/udev/data", evalSymlinks: filepath.EvalSymlinks, readDir: readDirNames}
	env.readFile = func(path string) ([]byte, error) {
		if rec, ok := strings.CutPrefix(path, "/run/udev/data/"); ok {
			if s, ok := udev[rec]; ok {
				return []byte(s), nil
			}
			return nil, fs.ErrNotExist
		}
		return os.ReadFile(path)
	}
	return env
}

func TestDiskKeysLinux(t *testing.T) {
	env := fakeSysfs(t)
	roots := map[string]string{
		"/media/seagate2":   "8:17",
		"/media/seagate2-b": "8:18", // another partition of the same disk
		"/media/seagate1":   "8:33",
		"/media/DBR_BOOT":   "8:34",
		"/whole":            "8:16", // a filesystem on the whole disk
		"/luks":             "253:0",
		"/lvm":              "253:1",
		"/lvm-on-luks":      "253:2",
		"/loop":             "7:0",
		"/tmpfs-with-uuid":  "0:25",
		"/tmpfs":            "0:26",
	}
	env.dev = func(path string) (uint32, uint32, error) {
		d, ok := roots[path]
		if !ok {
			return 0, 0, fs.ErrNotExist
		}
		var major, minor uint32
		fmt.Sscanf(d, "%d:%d", &major, &minor)
		return major, minor, nil
	}
	cases := map[string][]string{
		"/media/seagate2":   {"serial:NA77YET6"},
		"/media/seagate2-b": {"serial:NA77YET6"},
		// No udev record for the disk: the partition's serial.
		"/media/seagate1": {"serial:NA95K2KP"},
		// Neither the disk nor this partition has a serial: the device.
		"/media/DBR_BOOT":  {"dev:8:32"},
		"/whole":           {"serial:NA77YET6"},
		"/luks":            {"dev:8:48"}, // generic serial: the device
		"/lvm":             {"serial:E1", "serial:F1"},
		"/lvm-on-luks":     {"dev:8:48"},
		"/loop":            {"dev:7:0"},
		"/tmpfs-with-uuid": {"fs:linux:ABCDEF01"},
		"/tmpfs":           {"path:/tmpfs"},
		"/unplugged":       {"path:/unplugged"},
	}
	for root, want := range cases {
		if got := diskKeysLinux(root, env); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", root, got, want)
		}
	}
}

// Without sysfs (or udev) there's no disk: the filesystem ID or the path.
func TestDiskKeysLinuxWithoutSysfs(t *testing.T) {
	env := fixtureEnv(t, devBox, true)
	if got, want := diskKeysLinux("/mnt/seagate2", env), []string{"fs:linux:E85600000000BF7E"}; !reflect.DeepEqual(got, want) {
		t.Errorf("%v, want %v", got, want)
	}
}

func TestDiskKeysMacOS(t *testing.T) {
	cases := []struct {
		name, diskutil, realIoreg string
		ioregErr                  error
		want                      []string
	}{
		{"ntfs on USB", "diskutil-ntfs-real.plist", "ioreg-l-real.plist", nil, []string{"serial:NA77MASK"}},
		{"ntfs without a volume UUID", "diskutil-ntfs-nouuid-real.plist", "ioreg-l-real-2.plist", nil, []string{"serial:NA95MASK"}},
		{"apfs on USB", "diskutil-apfs.plist", "", nil, []string{"serial:NA8F2K1X"}},
		{"exfat, generic serial", "diskutil-exfat.plist", "", nil, []string{"dev:disk6"}},
		{"ioreg fails", "diskutil-apfs.plist", "", fmt.Errorf("exit status 1"), []string{"dev:disk4"}},
		{"network share", "diskutil-network.plist", "", nil, []string{"path:/Volumes/x"}},
		{"diskutil fails", "", "", nil, []string{"path:/Volumes/x"}},
	}
	for _, c := range cases {
		if got := diskKeysMacOS("/Volumes/x", fakeRun(c.diskutil, c.realIoreg, c.ioregErr)); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestDiskKeysOnThisMachine(t *testing.T) {
	keys := DiskKeys(t.TempDir())
	if len(keys) == 0 {
		t.Fatal("no keys")
	}
	t.Logf("temp dir: %v", keys)
}

func TestIsDiskKey(t *testing.T) {
	for key, want := range map[string]bool{"serial:A": true, "dev:8:16": true, "fs:linux:A": false, "path:/x": false} {
		if IsDiskKey(key) != want {
			t.Errorf("IsDiskKey(%q) != %v", key, want)
		}
	}
}
