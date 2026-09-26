// Package version identifies this driveagent build to the server.
package version

import "fmt"

// Version is bumped by hand in every PR that changes the agent (or
// agent/wire). CI refuses an unbumped change, and on merge to main releases
// exactly this version as the tag driveagent/v<Version>
// (docs/specs/remote-sync-ci.md).
const Version = "0.2.1"

// Commit is the short commit SHA, stamped by the release build with
// -ldflags "-X github.com/jyothri/bhandaar/agent/client/internal/version.Commit=<sha>".
var Commit = "unknown"

// Protocols are the agentserver protocols this build speaks.
var Protocols = []int{1}

// String is what "driveagent version" prints.
func String() string {
	return fmt.Sprintf("driveagent %s (%s), protocols %v", Version, Commit, Protocols)
}
