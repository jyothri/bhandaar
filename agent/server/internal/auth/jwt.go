package auth

import (
	"errors"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Access-token errors.
var (
	ErrTokenExpired = errors.New("access token expired")
	ErrTokenInvalid = errors.New("access token invalid")
)

// AccessClaims are an access token's claims: sub (user id), aid (agent id),
// iat, exp and jti.
type AccessClaims struct {
	AgentID string `json:"aid"`
	jwt.RegisteredClaims
}

// Principal is who an access token was issued to.
type Principal struct {
	UserID  int64
	AgentID string
}

// IssueAccessToken signs an HS256 access token.
func IssueAccessToken(secret []byte, p Principal, now time.Time, ttl time.Duration) (string, error) {
	claims := AccessClaims{
		AgentID: p.AgentID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   strconv.FormatInt(p.UserID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        uuid.NewString(),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// VerifyAccessToken checks an access token's signature and expiry.
func VerifyAccessToken(secret []byte, token string, now time.Time) (Principal, error) {
	var claims AccessClaims
	_, err := jwt.ParseWithClaims(token, &claims,
		func(*jwt.Token) (any, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return Principal{}, ErrTokenExpired
	}
	if err != nil {
		return Principal{}, ErrTokenInvalid
	}
	uid, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || claims.AgentID == "" {
		return Principal{}, ErrTokenInvalid
	}
	return Principal{UserID: uid, AgentID: claims.AgentID}, nil
}
