package identity

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// linuxEnv is what Linux detection reads, behind functions so tests can use
// fixtures.
type linuxEnv struct {
	// dev returns the major:minor device number of the filesystem holding
	// path (stat's st_dev, following symlinks).
	dev func(path string) (major, minor uint32, err error)
	// readFile reads /run/udev/data/... and /proc/self/mountinfo.
	readFile func(path string) ([]byte, error)
	udevDir  string
	// mountinfo is the path of /proc/self/mountinfo.
	mountinfo string
}

// detectLinux finds root's identity: stat gives the device number; the
// partition's udev record (/run/udev/data/b<major>:<minor>) gives
// ID_FS_UUID, ID_FS_TYPE and ID_SERIAL_SHORT. mountinfo, matched on the same
// device number, is a fallback for the filesystem type only, when udev has
// no record (in a container, say). Matching on the device number, not the
// longest path prefix, handles symlinked roots and bind mounts, and can't
// confuse /media/x with /media/xy.
func detectLinux(root string, env linuxEnv) (Identity, error) {
	major, minor, err := env.dev(root)
	if err != nil {
		return Identity{}, fmt.Errorf("stat %s: %w", root, err)
	}
	devNum := fmt.Sprintf("%d:%d", major, minor)

	id := Identity{Source: "linux"}
	rec, err := env.readFile(env.udevDir + "/b" + devNum)
	switch {
	case err == nil:
		props := parseUdev(rec)
		id.FSUUID = NormalizeFSUUID(props["ID_FS_UUID"])
		id.FSType = normalizeFSType(props["ID_FS_TYPE"])
		id.HWSerial = NormalizeSerial(props["ID_SERIAL_SHORT"])
	case errors.Is(err, fs.ErrNotExist):
		// No udev record: a virtual or network filesystem, or no udev at all.
	default:
		return Identity{}, err
	}
	if id.FSType == "" {
		if mi, err := env.readFile(env.mountinfo); err == nil {
			id.FSType = normalizeFSType(mountinfoFSType(mi, devNum))
		}
	}
	return id, nil
}

// parseUdev reads the E:KEY=VALUE properties of a udev database record.
func parseUdev(b []byte) map[string]string {
	props := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line, ok := strings.CutPrefix(sc.Text(), "E:")
		if !ok {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			props[k] = v
		}
	}
	return props
}

// mountinfoFSType returns the filesystem type of the mount whose device is
// devNum ("major:minor"). The format is: id parent major:minor root
// mountpoint options [optional fields...] - fstype source superopts.
func mountinfoFSType(b []byte, devNum string) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[2] != devNum {
			continue
		}
		for i, f := range fields {
			if f == "-" && i+1 < len(fields) {
				return fields[i+1]
			}
		}
	}
	return ""
}
