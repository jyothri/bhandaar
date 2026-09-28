// Package auth checks web logins against the users agentserver manages.
//
// Users live in agentserver's agent_users table, in the database both
// services share, and are added, re-passworded and disabled with
// agentserver's admin CLI (`agentserver user …`). be only reads them.
// Password hashes are argon2id PHC strings, written by agentserver's
// internal/auth; VerifyPassword here must accept everything that writes.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
)

var errBadHash = errors.New("malformed password hash")

// VerifyPassword reports whether password matches an argon2id PHC hash:
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>
func VerifyPassword(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil ||
		memory == 0 || time == 0 || threads == 0 {
		return false, errBadHash
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// HashPassword returns an argon2id PHC string with agentserver's default
// cost (64 MiB, 3 passes, 4 lanes). be never stores passwords; this is for
// tests and VerifyDummy.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	const memory, time, threads = 64 * 1024, 3, 4
	key := argon2.IDKey([]byte(password), salt, time, memory, threads, 32)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// VerifyDummy spends the same time as verifying a real password, for logins
// with an unknown username, so response times don't reveal which usernames
// exist.
func VerifyDummy(password string) {
	dummyOnce.Do(func() {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		dummyHash, _ = HashPassword(base64.StdEncoding.EncodeToString(b))
	})
	_, _ = VerifyPassword(dummyHash, password)
}
