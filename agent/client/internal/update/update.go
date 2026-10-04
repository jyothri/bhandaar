package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// What a re-exec'd driveagent is handed in its environment
// (docs/specs/agent-auto-update.md, "Re-exec"); not for users.
const (
	// EnvUpdatedFrom is the version it was updated from: it never updates
	// again in the same run.
	EnvUpdatedFrom = "DRIVEAGENT_UPDATED_FROM"
	// EnvInstanceFD is the fd holding the instance lock, which it adopts.
	EnvInstanceFD = "DRIVEAGENT_INSTANCE_FD"
	// EnvNoAutoUpdate, set to anything, turns automatic updates off.
	EnvNoAutoUpdate = "DRIVEAGENT_NO_AUTO_UPDATE"
)

// Newer reports whether version b is newer than a, both MAJOR.MINOR.PATCH;
// anything else is never newer.
func Newer(a, b string) bool {
	x, okA := parse(a)
	y, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return y[i] > x[i]
		}
	}
	return false
}

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || (len(p) > 1 && p[0] == '0') {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Failure is the last automatic update that failed, kept in the lock dir,
// so a broken release or a GitHub outage isn't retried by every run
// (docs/specs/agent-auto-update.md, "Failures").
type Failure struct {
	Version  string    `json:"version"`
	FailedAt time.Time `json:"failed_at"`
	Error    string    `json:"error"`
}

// RetryAfter is how long a failed version isn't tried again
// automatically.
var RetryAfter = time.Hour

const failureFile = "update.json"

// Blocks reports whether f keeps an automatic update to version from
// being tried now.
func (f Failure) Blocks(version string, now time.Time) bool {
	return f.Version == version && now.Sub(f.FailedAt) < RetryAfter
}

// LoadFailure reads the failure record in lockDir; none is the zero
// Failure.
func LoadFailure(lockDir string) Failure {
	var f Failure
	if b, err := os.ReadFile(filepath.Join(lockDir, failureFile)); err == nil {
		json.Unmarshal(b, &f)
	}
	return f
}

// RecordFailure records that updating to version failed with err.
func RecordFailure(lockDir, version string, err error) error {
	b, _ := json.Marshal(Failure{Version: version, FailedAt: time.Now().UTC(), Error: err.Error()})
	tmp, werr := os.CreateTemp(lockDir, ".tmp-update-*")
	if werr != nil {
		return werr
	}
	_, werr = tmp.Write(b)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), filepath.Join(lockDir, failureFile))
	}
	if werr != nil {
		os.Remove(tmp.Name())
	}
	return werr
}

// ClearFailure forgets the failure record.
func ClearFailure(lockDir string) {
	os.Remove(filepath.Join(lockDir, failureFile))
}
