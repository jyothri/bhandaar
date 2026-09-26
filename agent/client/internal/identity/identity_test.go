package identity

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fixtureEnv serves the M0 udev records and mountinfo from testdata/linux;
// devs maps a path to the device number stat would give it.
func fixtureEnv(t *testing.T, devs map[string]string, withUdev bool) linuxEnv {
	return linuxEnv{
		dev: func(path string) (uint32, uint32, error) {
			d, ok := devs[path]
			if !ok {
				return 0, 0, fs.ErrNotExist
			}
			var major, minor uint32
			fmt.Sscanf(d, "%d:%d", &major, &minor)
			return major, minor, nil
		},
		readFile: func(path string) ([]byte, error) {
			switch {
			case path == "/proc/self/mountinfo":
				return os.ReadFile("testdata/linux/mountinfo")
			case strings.HasPrefix(path, "/run/udev/data/") && withUdev:
				return os.ReadFile(filepath.Join("testdata/linux/udev", strings.TrimPrefix(path, "/run/udev/data/")))
			}
			return nil, fs.ErrNotExist
		},
		udevDir:   "/run/udev/data",
		mountinfo: "/proc/self/mountinfo",
	}
}

// The dev box's drives (M0, masked).
var devBox = map[string]string{
	"/media/jyothri/Seagate1": "8:33", // NTFS, mounted with the ntfs3 kernel driver
	"/mnt/seagate2":           "8:17", // NTFS, mounted with ntfs-3g (fuseblk)
	"/mnt/wd1tb":              "8:5",  // ext4, internal SATA
	"/run":                    "0:25", // tmpfs: no udev record
}

func TestLinuxDevBoxDrives(t *testing.T) {
	env := fixtureEnv(t, devBox, true)
	cases := map[string]Identity{
		"/media/jyothri/Seagate1": {FSUUID: "865000000000E771", FSType: "ntfs", Source: "linux", HWSerial: "NA0000T6"},
		"/mnt/seagate2":           {FSUUID: "E85600000000BF7E", FSType: "ntfs", Source: "linux", HWSerial: "NA0000KP"},
		"/mnt/wd1tb":              {FSUUID: "E12A000000000000000000000000D136", FSType: "ext4", Source: "linux", HWSerial: "WD-W0000000EJTX"},
	}
	for root, want := range cases {
		got, err := detectLinux(root, env)
		if err != nil || got != want {
			t.Errorf("%s: %+v, %v\nwant %+v", root, got, err, want)
		}
	}
}

// The two Seagates are both NTFS, mounted by different drivers; udev's
// type (not mountinfo's driver) makes them the same kind.
func TestNTFS3AndFuseblkAreBothNTFS(t *testing.T) {
	env := fixtureEnv(t, devBox, true)
	a, _ := detectLinux("/media/jyothri/Seagate1", env)
	b, _ := detectLinux("/mnt/seagate2", env)
	if a.FSType != "ntfs" || b.FSType != "ntfs" {
		t.Errorf("types %q and %q, want ntfs for both", a.FSType, b.FSType)
	}
}

// Matching is by device number, not path: a symlinked root, a bind mount,
// and /media/x vs /media/xy all resolve to their own device.
func TestLinuxMatchesByDevice(t *testing.T) {
	env := fixtureEnv(t, map[string]string{
		"/home/me/backup-link": "8:17", // a symlink to /mnt/seagate2 (stat follows it)
		"/srv/bind/wd":         "8:5",  // a bind mount of /mnt/wd1tb
		"/media/x":             "8:17",
		"/media/xy":            "8:33",
	}, true)
	for root, want := range map[string]string{
		"/home/me/backup-link": "E85600000000BF7E",
		"/srv/bind/wd":         "E12A000000000000000000000000D136",
		"/media/x":             "E85600000000BF7E",
		"/media/xy":            "865000000000E771",
	} {
		if got, err := detectLinux(root, env); err != nil || got.FSUUID != want {
			t.Errorf("%s: %+v, %v; want %s", root, got, err, want)
		}
	}
}

func TestLinuxWithoutUdev(t *testing.T) {
	// In a container there's no udev database: the type comes from
	// mountinfo, normalised; there's no ID or serial.
	env := fixtureEnv(t, devBox, false)
	for root, wantType := range map[string]string{
		"/media/jyothri/Seagate1": "ntfs",    // ntfs3 -> ntfs
		"/mnt/seagate2":           "fuseblk", // FUSE: can't tell which filesystem
		"/mnt/wd1tb":              "ext4",
		"/run":                    "tmpfs",
	} {
		got, err := detectLinux(root, env)
		want := Identity{FSType: wantType, Source: "linux"}
		if err != nil || got != want {
			t.Errorf("%s: %+v, %v; want %+v", root, got, err, want)
		}
		if !got.Empty() {
			t.Errorf("%s: not empty", root)
		}
	}
}

func TestLinuxStatError(t *testing.T) {
	if _, err := detectLinux("/unplugged", fixtureEnv(t, devBox, true)); err == nil {
		t.Error("want an error")
	}
}

func TestDetectOnThisMachine(t *testing.T) {
	// Whatever the machine, detecting the temp dir's filesystem must not fail.
	id, err := Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("temp dir: %v (%s)", id, id.FSType)
}

func TestNormalizeSerial(t *testing.T) {
	for in, want := range map[string]string{
		"NA8F2K1X": "NA8F2K1X", " na8f2k1x ": "NA8F2K1X", "WD-WCC4E1234567": "WD-WCC4E1234567",
		"": "", "000000000000": "", "0": "", "FFFFFFFF": "", "0123456789ABCDEF": "", "0123456789abcdef": "",
		"123456789": "", "To Be Filled By O.E.M.": "", "None": "",
	} {
		if got := NormalizeSerial(in); got != want {
			t.Errorf("NormalizeSerial(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeFSUUID(t *testing.T) {
	for in, want := range map[string]string{
		"e12a1234-5678-9abc-def0-123456789abc": "E12A123456789ABCDEF0123456789ABC",
		"ABCD-1234":                            "ABCD1234",
		"865089f05089e771":                     "865089F05089E771",
	} {
		if got := NormalizeFSUUID(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestParsePlist(t *testing.T) {
	b, _ := os.ReadFile("testdata/macos/ioreg-usb.plist")
	v, err := parsePlist(b)
	if err != nil {
		t.Fatal(err)
	}
	arr := v.([]any)
	if len(arr) != 3 {
		t.Fatalf("%d devices", len(arr))
	}
	kb := arr[0].(map[string]any)
	if kb["idVendor"] != int64(1452) || kb["USB Serial Number"] != "KB0000000001" {
		t.Errorf("keyboard = %v", kb)
	}
	bridge := arr[2].(map[string]any)
	child := bridge["IORegistryEntryChildren"].([]any)[0].(map[string]any)
	if !reflect.DeepEqual(child["Data"], []byte{0, 1, 2, 3}) {
		t.Errorf("data = %v", child["Data"])
	}
	media := arr[1].(map[string]any)["IORegistryEntryChildren"].([]any)[0].(map[string]any)["IORegistryEntryChildren"].([]any)[0].(map[string]any)
	if media["Whole"] != true {
		t.Errorf("bool = %v", media["Whole"])
	}
	for _, bad := range []string{"", "<plist>", "<plist><dict><key>a</key><integer>x</integer></dict></plist>", "<plist><bogus/></plist>"} {
		if _, err := parsePlist([]byte(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

// fakeRun answers diskutil from a fixture and ioreg from ioreg-usb.plist.
func fakeRun(diskutil string, ioregErr error) runFunc {
	return func(name string, args ...string) ([]byte, error) {
		switch name {
		case "diskutil":
			if diskutil == "" {
				return nil, errors.New("diskutil: exit status 1")
			}
			return os.ReadFile("testdata/macos/" + diskutil)
		case "ioreg":
			if ioregErr != nil {
				return nil, ioregErr
			}
			return os.ReadFile("testdata/macos/ioreg-usb.plist")
		}
		return nil, errors.New("unexpected command " + name)
	}
}

func TestMacOS(t *testing.T) {
	cases := []struct {
		name, diskutil string
		ioregErr       error
		want           Identity
	}{
		// APFS: the volume's parent is a synthesized container, so the
		// physical store's disk (disk4) is what's matched in ioreg.
		{"apfs on USB", "diskutil-apfs.plist", nil,
			Identity{FSUUID: "1A2B3C4D00004000800000000000ABCD", FSType: "apfs", Source: "macos", HWSerial: "NA8F2K1X"}},
		// The bridge reports a placeholder serial: treated as none.
		{"exfat, generic serial", "diskutil-exfat.plist", nil,
			Identity{FSUUID: "4F1E00000000300090000000000077AA", FSType: "exfat", Source: "macos"}},
		{"network share", "diskutil-network.plist", nil, Identity{FSType: "smbfs", Source: "macos"}},
		{"ioreg fails", "diskutil-apfs.plist", errors.New("ioreg: exit status 1"),
			Identity{FSUUID: "1A2B3C4D00004000800000000000ABCD", FSType: "apfs", Source: "macos"}},
	}
	for _, c := range cases {
		got, err := detectMacOS("/Volumes/x", fakeRun(c.diskutil, c.ioregErr))
		if err != nil || got != c.want {
			t.Errorf("%s: %+v, %v\nwant %+v", c.name, got, err, c.want)
		}
	}
	if _, err := detectMacOS("/Volumes/x", fakeRun("", nil)); err == nil {
		t.Error("diskutil failing should be an error")
	}
}

func TestGuard(t *testing.T) {
	ntfsA := Identity{FSUUID: "AAAA", FSType: "ntfs", Source: "linux", HWSerial: "S1"}
	cases := []struct {
		name          string
		stored, found Identity
		accept        bool
		refuse        bool
		save          bool
		record        Identity
		warn          string
	}{
		{"new drive", Identity{}, ntfsA, false, false, true, ntfsA, ""},
		{"same drive", ntfsA, ntfsA, false, false, false, ntfsA, ""},
		{"different filesystem", ntfsA, Identity{FSUUID: "BBBB", FSType: "ntfs", Source: "linux", HWSerial: "S1"}, false, true, false, Identity{}, ""},
		{"different filesystem, accepted", ntfsA, Identity{FSUUID: "BBBB", FSType: "ntfs", Source: "linux", HWSerial: "S9"}, true, false, true,
			Identity{FSUUID: "BBBB", FSType: "ntfs", Source: "linux", HWSerial: "S9"}, "accepting a new identity"},
		{"new dock: serial changed", ntfsA, Identity{FSUUID: "AAAA", FSType: "ntfs", Source: "linux", HWSerial: "S2"}, false, false, true,
			Identity{FSUUID: "AAAA", FSType: "ntfs", Source: "linux", HWSerial: "S2"}, "now reports serial S2"},
		{"serial filled in", Identity{FSUUID: "AAAA", FSType: "ntfs", Source: "linux"}, ntfsA, false, false, true, ntfsA, ""},
		{"nothing found this time", ntfsA, Identity{Source: "linux"}, false, false, false, ntfsA, "keeping the stored"},
		{"serial missing this time", ntfsA, Identity{FSUUID: "AAAA", FSType: "ntfs", Source: "linux"}, false, false, false, ntfsA, "keeping the stored"},
		{"nothing at all", Identity{}, Identity{FSType: "tmpfs", Source: "linux"}, false, false, true, Identity{FSType: "tmpfs"}, "can't be linked"},
		{"only a serial, and it changed", Identity{HWSerial: "S1"}, Identity{HWSerial: "S2", Source: "linux"}, false, false, true,
			Identity{HWSerial: "S2"}, "now reports serial"},
		// IDs from different OSes aren't comparable: no refusal.
		{"other source", Identity{FSUUID: "MAC1", FSType: "exfat", Source: "macos"}, Identity{FSUUID: "LNX1", FSType: "exfat", Source: "linux"}, false, false, true,
			Identity{FSUUID: "LNX1", FSType: "exfat", Source: "linux"}, ""},
	}
	for _, c := range cases {
		d := Check("seagate1", "/media/jyothri/Seagate1", c.stored, c.found, c.accept)
		if (d.Refusal != "") != c.refuse {
			t.Errorf("%s: refusal %q", c.name, d.Refusal)
			continue
		}
		if c.refuse {
			if !strings.Contains(d.Refusal, "--accept-identity-change") || !strings.Contains(d.Refusal, "Wrong drive?") {
				t.Errorf("%s: refusal message %q", c.name, d.Refusal)
			}
			continue
		}
		if d.Save != c.save || (c.save && d.Record != c.record) {
			t.Errorf("%s: save=%v record=%+v; want save=%v %+v", c.name, d.Save, d.Record, c.save, c.record)
		}
		joined := strings.Join(d.Warnings, "\n")
		if (c.warn == "") != (joined == "") || !strings.Contains(joined, c.warn) {
			t.Errorf("%s: warnings %q, want %q", c.name, joined, c.warn)
		}
	}
}
