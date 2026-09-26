package store

import (
	"context"
	"errors"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
)

// Login lockout: this many failures for one (username, client IP) within the
// window lock that pair out until the oldest of them leaves the window.
const (
	LockoutFailures = 10
	LockoutWindow   = 15 * time.Minute
)

// LoginLockedFor reports how long (username, ip) is still locked out, or 0 if
// it may try to log in.
func (s *Store) LoginLockedFor(ctx context.Context, username string, ip netip.Addr) (time.Duration, error) {
	// The LockoutFailures-th most recent failure in the window; the pair is
	// unlocked once it leaves the window.
	var secs float64
	err := s.Pool.QueryRow(ctx, `
		SELECT extract(epoch FROM failed_at + make_interval(secs => $3) - now())::float8
		  FROM agent_login_failures
		 WHERE username = $1 AND client_ip = $2::text::inet AND failed_at > now() - make_interval(secs => $3)
		 ORDER BY failed_at DESC
		OFFSET $4 LIMIT 1`,
		username, ip.String(), LockoutWindow.Seconds(), LockoutFailures-1).Scan(&secs)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return max(time.Duration(secs*float64(time.Second)), time.Second), nil
}

// RecordLoginFailure records a failed login.
func (s *Store) RecordLoginFailure(ctx context.Context, username string, ip netip.Addr) error {
	_, err := s.Pool.Exec(ctx,
		"INSERT INTO agent_login_failures (username, client_ip, failed_at) VALUES ($1, $2::text::inet, now())",
		username, ip.String())
	return err
}

// ClearLoginFailures forgets (username, ip)'s failures after a successful login.
func (s *Store) ClearLoginFailures(ctx context.Context, username string, ip netip.Addr) error {
	_, err := s.Pool.Exec(ctx,
		"DELETE FROM agent_login_failures WHERE username = $1 AND client_ip = $2::text::inet", username, ip.String())
	return err
}
