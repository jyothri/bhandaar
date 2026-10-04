// Command verifysig checks a release's SHA256SUMS.sig against the public
// keys built into driveagent, as an update will, so the release job fails
// rather than shipping a release nobody can update to
// (docs/specs/agent-auto-update.md, "Signing in CI").
//
//	go run ./scripts/verifysig <version> <SHA256SUMS> <SHA256SUMS.sig>
package main

import (
	"fmt"
	"os"

	"github.com/jyothri/bhandaar/agent/client/internal/update"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: verifysig <version> <SHA256SUMS> <SHA256SUMS.sig>")
		os.Exit(2)
	}
	sums, err := os.ReadFile(os.Args[2])
	if err == nil {
		var sig []byte
		if sig, err = os.ReadFile(os.Args[3]); err == nil {
			err = update.VerifySums(update.Keys, os.Args[1], sums, sig)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "verifysig: driveagent %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
	fmt.Printf("SHA256SUMS of driveagent %s is signed with a built-in key\n", os.Args[1])
}
