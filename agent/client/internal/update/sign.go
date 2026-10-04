// Package update replaces driveagent with a newer release from GitHub: the
// version the server names, signed for that version with the release key
// (docs/specs/agent-auto-update.md).
package update

import (
	"crypto/ed25519"
	"errors"
)

// Message is what a release's SHA256SUMS.sig signs: the release's tag and
// its SHA256SUMS, so a signature is good only for the version it was made
// for, and an older release can't be served under a newer tag.
func Message(version string, sums []byte) []byte {
	return append([]byte("driveagent/v"+version+"\n"), sums...)
}

// ErrSignature is a SHA256SUMS whose signature doesn't verify, for this
// version, with any of the keys.
var ErrSignature = errors.New("the SHA256SUMS signature doesn't verify")

// VerifySums checks sig over Message(version, sums) against keys.
func VerifySums(keys []ed25519.PublicKey, version string, sums, sig []byte) error {
	if len(sig) != ed25519.SignatureSize {
		return ErrSignature
	}
	msg := Message(version, sums)
	for _, k := range keys {
		if ed25519.Verify(k, msg, sig) {
			return nil
		}
	}
	return ErrSignature
}
