package update

import (
	"crypto/ed25519"
	"encoding/hex"
)

// Keys are the public keys releases are signed with: Ed25519, over
// Message (docs/specs/agent-auto-update.md, "Signing in CI"). The private
// key is a secret of the driveagent-release GitHub environment. A list, so
// a release can trust a new key before releases are signed with it.
var Keys = []ed25519.PublicKey{
	mustKey("1a55a4cb433776f83672ef372249e06e7f7b6f90fd9ffb6728ae1a7aef408e48"), // 2026-10-04
}

func mustKey(h string) ed25519.PublicKey {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != ed25519.PublicKeySize {
		panic("update: bad public key " + h)
	}
	return ed25519.PublicKey(b)
}
