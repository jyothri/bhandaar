package auth

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestMain(m *testing.M) {
	// Cheap hashes keep the tests fast; verification reads the parameters
	// from each hash, so nothing else changes.
	DefaultParams = Params{Memory: 64, Time: 1, Threads: 1}
	os.Exit(m.Run())
}

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Errorf("hash = %q, want a PHC argon2id string", h)
	}
	if ok, err := VerifyPassword(h, "correct horse battery"); !ok || err != nil {
		t.Errorf("right password: %v, %v", ok, err)
	}
	if ok, err := VerifyPassword(h, "correct horse batterY"); ok || err != nil {
		t.Errorf("wrong password: %v, %v", ok, err)
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("two hashes of one password are equal; the salt isn't random")
	}
}

func TestVerifyUsesStoredParams(t *testing.T) {
	old := DefaultParams
	DefaultParams = Params{Memory: 128, Time: 2, Threads: 2}
	h, _ := HashPassword("pw-with-other-params")
	DefaultParams = old
	if ok, err := VerifyPassword(h, "pw-with-other-params"); !ok || err != nil {
		t.Errorf("hash with other params: %v, %v", ok, err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=64,t=1,p=1$AAAA$AAAA", "$argon2id$v=18$m=64,t=1,p=1$AAAA$AAAA",
		"$argon2id$v=19$m=0,t=1,p=1$AAAA$AAAA", "$argon2id$v=19$m=64,t=1,p=1$!!$AAAA", "$argon2id$v=19$m=64,t=1,p=1$AAAA$"} {
		if ok, err := VerifyPassword(h, "x"); ok || err == nil {
			t.Errorf("VerifyPassword(%q) = %v, %v; want an error", h, ok, err)
		}
	}
}

func TestVerifyDummyNeverPanics(t *testing.T) {
	VerifyDummy("anything")
	VerifyDummy("")
}

var secret = []byte("0123456789abcdef0123456789abcdef")

func TestAccessToken(t *testing.T) {
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	p := Principal{UserID: 42, AgentID: "7b0e5f0c-0000-4000-8000-000000000001"}
	tok, err := IssueAccessToken(secret, p, now, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyAccessToken(secret, tok, now.Add(14*time.Minute))
	if err != nil || got != p {
		t.Fatalf("verify = %+v, %v", got, err)
	}
	if _, err := VerifyAccessToken(secret, tok, now.Add(15*time.Minute+time.Second)); err != ErrTokenExpired {
		t.Errorf("after expiry: %v, want ErrTokenExpired", err)
	}
	if _, err := VerifyAccessToken([]byte("another secret, 32 bytes long!!!"), tok, now); err != ErrTokenInvalid {
		t.Errorf("wrong secret: %v, want ErrTokenInvalid", err)
	}

	// Tampering with the payload breaks the signature.
	parts := strings.Split(tok, ".")
	payload := []byte(parts[1])
	payload[len(payload)/2] ^= 1
	if _, err := VerifyAccessToken(secret, parts[0]+"."+string(payload)+"."+parts[2], now); err != ErrTokenInvalid {
		t.Errorf("tampered: %v, want ErrTokenInvalid", err)
	}
}

func TestAccessTokenRejectsOtherAlgorithms(t *testing.T) {
	now := time.Now()
	claims := AccessClaims{AgentID: "a", RegisteredClaims: jwt.RegisteredClaims{
		Subject: "1", IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(time.Minute))}}
	none, err := jwt.NewWithClaims(jwt.SigningMethodNone, claims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyAccessToken(secret, none, now); err != ErrTokenInvalid {
		t.Errorf("alg none: %v", err)
	}
	hs512, _ := jwt.NewWithClaims(jwt.SigningMethodHS512, claims).SignedString(secret)
	if _, err := VerifyAccessToken(secret, hs512, now); err != ErrTokenInvalid {
		t.Errorf("HS512: %v", err)
	}
	noExp := AccessClaims{AgentID: "a", RegisteredClaims: jwt.RegisteredClaims{Subject: "1"}}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, noExp).SignedString(secret)
	if _, err := VerifyAccessToken(secret, tok, now); err != ErrTokenInvalid {
		t.Errorf("no exp: %v", err)
	}
}
