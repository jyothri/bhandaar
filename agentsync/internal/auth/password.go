// Package auth holds agentsync's credentials: argon2id password hashes, JWT
// access tokens and rotating refresh tokens.
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

// Params are argon2id's cost parameters.
type Params struct {
	Memory  uint32 // KiB
	Time    uint32
	Threads uint8
}

// DefaultParams are used for new hashes: RFC 9106's second recommended
// option (64 MiB, 3 passes, 4 lanes). Verification uses the parameters stored
// in each hash, so they can be raised later without invalidating old hashes.
// Tests lower them to stay fast.
var DefaultParams = Params{Memory: 64 * 1024, Time: 3, Threads: 4}

const (
	saltLen = 16
	keyLen  = 32
)

// MinPasswordLen is the shortest password the admin CLI accepts.
const MinPasswordLen = 12

var errBadHash = errors.New("malformed password hash")

// HashPassword returns an argon2id PHC string:
// $argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := DefaultParams
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches hash.
func VerifyPassword(hash, password string) (bool, error) {
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil ||
		p.Memory == 0 || p.Time == 0 || p.Threads == 0 {
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
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// VerifyDummy spends the same time as verifying a real password, for logins
// with an unknown username, so response times don't reveal which usernames
// exist. It always reports false.
func VerifyDummy(password string) {
	dummyOnce.Do(func() {
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		dummyHash, _ = HashPassword(base64.StdEncoding.EncodeToString(b))
	})
	_, _ = VerifyPassword(dummyHash, password)
}
