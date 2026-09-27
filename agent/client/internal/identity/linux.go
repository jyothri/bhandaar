//go:build linux

package identity

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Detect reads the identity of the drive holding root.
func Detect(root string) (Identity, error) {
	return detectLinux(root, hostEnv())
}

// DiskKeys returns the keys of the physical disk(s) holding root.
func DiskKeys(root string) []string {
	return diskKeysLinux(root, hostEnv())
}

func hostEnv() linuxEnv {
	return linuxEnv{
		dev: func(path string) (uint32, uint32, error) {
			var st unix.Stat_t
			if err := unix.Stat(path, &st); err != nil {
				return 0, 0, err
			}
			return unix.Major(uint64(st.Dev)), unix.Minor(uint64(st.Dev)), nil
		},
		readFile:     os.ReadFile,
		udevDir:      "/run/udev/data",
		mountinfo:    "/proc/self/mountinfo",
		sysDir:       "/sys",
		evalSymlinks: filepath.EvalSymlinks,
		readDir:      readDirNames,
	}
}
