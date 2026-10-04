package update

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"
)

// A signature made by openssl with the release key, as the release job
// makes them, over Message("0.0.0", "test\n"): Go's ed25519 must accept it.
const opensslSig = "a9c57f17671a18c846dd615b7b748e2daab8532039084d49e45b13b1b0e95c79522b84e73826e54ec684843bf4b88b8c60696e78ac25150ffc22bde075fc570f"

func TestVerifySumsAcceptsTheReleaseKeysOpenSSLSignature(t *testing.T) {
	sig, _ := hex.DecodeString(opensslSig)
	if err := VerifySums(Keys, "0.0.0", []byte("test\n"), sig); err != nil {
		t.Fatalf("the release key's signature: %v", err)
	}
	// Bound to the version: the same signature for another one fails.
	if err := VerifySums(Keys, "0.0.1", []byte("test\n"), sig); err != ErrSignature {
		t.Errorf("another version: %v, want ErrSignature", err)
	}
	if err := VerifySums(Keys, "0.0.0", []byte("test2\n"), sig); err != ErrSignature {
		t.Errorf("other sums: %v, want ErrSignature", err)
	}
	if err := VerifySums(Keys, "0.0.0", []byte("test\n"), sig[:10]); err != ErrSignature {
		t.Errorf("a short signature: %v, want ErrSignature", err)
	}
}

func TestVerifySumsTriesEveryKey(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	sig := ed25519.Sign(priv, Message("1.2.3", []byte("sums")))
	other, _, _ := ed25519.GenerateKey(nil)
	if err := VerifySums([]ed25519.PublicKey{other, pub}, "1.2.3", []byte("sums"), sig); err != nil {
		t.Errorf("with the key second: %v", err)
	}
	if err := VerifySums([]ed25519.PublicKey{other}, "1.2.3", []byte("sums"), sig); err != ErrSignature {
		t.Errorf("an unknown key: %v", err)
	}
}
