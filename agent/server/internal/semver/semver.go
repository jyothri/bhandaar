// Package semver parses and compares MAJOR.MINOR.PATCH versions. It is kept
// small on purpose: agent versions never carry pre-release or build suffixes.
package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version is a MAJOR.MINOR.PATCH version.
type Version struct {
	Major, Minor, Patch int
}

// Parse parses "MAJOR.MINOR.PATCH", with no leading "v" and no suffix.
func Parse(s string) (Version, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid version %q: want MAJOR.MINOR.PATCH", s)
	}
	var n [3]int
	for i, p := range parts {
		// Digits only, and no leading zeros (as in semver).
		if p == "" || strings.TrimLeft(p, "0123456789") != "" || (len(p) > 1 && p[0] == '0') {
			return Version{}, fmt.Errorf("invalid version %q: want MAJOR.MINOR.PATCH", s)
		}
		v, err := strconv.Atoi(p)
		if err != nil {
			return Version{}, fmt.Errorf("invalid version %q: %v", s, err)
		}
		n[i] = v
	}
	return Version{n[0], n[1], n[2]}, nil
}

// Compare returns -1, 0 or +1 as v is below, equal to or above w.
func (v Version) Compare(w Version) int {
	for _, d := range [3]int{v.Major - w.Major, v.Minor - w.Minor, v.Patch - w.Patch} {
		if d < 0 {
			return -1
		}
		if d > 0 {
			return 1
		}
	}
	return 0
}

// Less reports whether v is below w.
func (v Version) Less(w Version) bool { return v.Compare(w) < 0 }

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}
