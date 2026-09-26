package version

import (
	"regexp"
	"testing"
)

func TestVersionIsSemver(t *testing.T) {
	// scripts/version-check.sh reads this constant with sed and needs plain MAJOR.MINOR.PATCH.
	if !regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`).MatchString(Version) {
		t.Errorf("Version = %q, want MAJOR.MINOR.PATCH", Version)
	}
}

func TestString(t *testing.T) {
	if got, want := String(), "driveagent "+Version+" (unknown), protocols [1]"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
