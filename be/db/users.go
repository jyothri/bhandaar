package db

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"
)

// Users are agentserver's: be reads agent_users (and shares agentserver's
// login lockout, agent_login_failures) but never creates or changes users.
// See be/auth.

var ErrNoSuchUser = errors.New("no such user")

// User is the part of an agent_users row be needs.
type User struct {
	ID           int64      `db:"id"`
	Username     string     `db:"username"`
	PasswordHash string     `db:"password_hash"`
	DisabledAt   *time.Time `db:"disabled_at"`
}

// Disabled reports whether agentserver's `user disable` was run for u.
func (u User) Disabled() bool { return u.DisabledAt != nil }

// UserByName looks a user up, disabled or not.
func UserByName(username string) (User, error) {
	var u User
	err := db.Get(&u, `SELECT id, username, password_hash, disabled_at FROM agent_users WHERE username = $1`, username)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSuchUser
	}
	if err != nil {
		return User{}, fmt.Errorf("failed to look up user: %w", err)
	}
	return u, nil
}

// Login lockout, the same as agentserver's (and in the same table, so failed
// web and agent logins count together): LockoutFailures failures for one
// (username, client IP) within LockoutWindow lock that pair out until the
// oldest of them leaves the window.
const (
	LockoutFailures = 10
	LockoutWindow   = 15 * time.Minute
)

// LoginLockedFor reports how long (username, ip) is still locked out, or 0 if
// it may try to log in.
func LoginLockedFor(username string, ip netip.Addr) (time.Duration, error) {
	var secs float64
	err := db.Get(&secs, `
		SELECT extract(epoch FROM failed_at + make_interval(secs => $3) - now())::float8
		  FROM agent_login_failures
		 WHERE username = $1 AND client_ip = $2::text::inet AND failed_at > now() - make_interval(secs => $3)
		 ORDER BY failed_at DESC
		OFFSET $4 LIMIT 1`,
		username, ip.String(), LockoutWindow.Seconds(), LockoutFailures-1)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("failed to check login lockout: %w", err)
	}
	return max(time.Duration(secs*float64(time.Second)), time.Second), nil
}

// RecordLoginFailure records a failed login.
func RecordLoginFailure(username string, ip netip.Addr) error {
	_, err := db.Exec(
		`INSERT INTO agent_login_failures (username, client_ip, failed_at) VALUES ($1, $2::text::inet, now())`,
		username, ip.String())
	return err
}

// ClearLoginFailures forgets (username, ip)'s failures after a successful login.
func ClearLoginFailures(username string, ip netip.Addr) error {
	_, err := db.Exec(
		`DELETE FROM agent_login_failures WHERE username = $1 AND client_ip = $2::text::inet`, username, ip.String())
	return err
}

// Sessions are rows of web_sessions, keyed on sha256 of the cookie's token,
// so the table holds nothing a browser could present. A session expires
// SessionTTL after it was last used.
const SessionTTL = 30 * 24 * time.Hour

// sessionTouchInterval limits how often using a session rewrites its row.
const sessionTouchInterval = time.Hour

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// CreateSession stores a session for userID under token. It also clears out
// expired sessions, so the table doesn't need housekeeping.
func CreateSession(userID int64, token string) error {
	if _, err := db.Exec(`DELETE FROM web_sessions WHERE expires_at < now()`); err != nil {
		return fmt.Errorf("failed to delete expired sessions: %w", err)
	}
	_, err := db.Exec(`INSERT INTO web_sessions (token_hash, user_id, expires_at)
		VALUES ($1, $2, now() + make_interval(secs => $3))`,
		tokenHash(token), userID, SessionTTL.Seconds())
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	return nil
}

// SessionUser returns the user a live session token belongs to, extending the
// session. It returns ErrNoSuchUser when the token is unknown or expired, or
// its user is disabled.
func SessionUser(token string) (User, error) {
	var row struct {
		User
		LastSeenAt time.Time `db:"last_seen_at"`
	}
	err := db.Get(&row, `
		SELECT u.id, u.username, u.password_hash, u.disabled_at, s.last_seen_at
		  FROM web_sessions s JOIN agent_users u ON u.id = s.user_id
		 WHERE s.token_hash = $1 AND s.expires_at > now()`, tokenHash(token))
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoSuchUser
	}
	if err != nil {
		return User{}, fmt.Errorf("failed to look up session: %w", err)
	}
	if row.Disabled() {
		return User{}, ErrNoSuchUser
	}
	if time.Since(row.LastSeenAt) > sessionTouchInterval {
		_, err := db.Exec(`UPDATE web_sessions
			SET last_seen_at = now(), expires_at = now() + make_interval(secs => $2)
			WHERE token_hash = $1`, tokenHash(token), SessionTTL.Seconds())
		if err != nil {
			slog.Warn("Failed to extend session", "user", row.Username, "error", err)
		}
	}
	return row.User, nil
}

// DeleteSession ends a session. An unknown token is not an error.
func DeleteSession(token string) error {
	_, err := db.Exec(`DELETE FROM web_sessions WHERE token_hash = $1`, tokenHash(token))
	return err
}

// ScanOwnedBy reports whether scanId exists and belongs to userID.
func ScanOwnedBy(scanId int, userID int64) (bool, error) {
	var owned bool
	err := db.Get(&owned, `SELECT EXISTS (SELECT 1 FROM scans WHERE id = $1 AND user_id = $2)`, scanId, userID)
	if err != nil {
		return false, fmt.Errorf("failed to check owner of scan %d: %w", scanId, err)
	}
	return owned, nil
}

// migrateUsers ties scans and linked Google accounts to agentserver's users,
// and adds the session table. agent_users must already exist: agentserver
// creates it, so it has to have started once against this database.
func migrateUsers() error {
	var exists bool
	if err := db.Get(&exists, `SELECT to_regclass('agent_users') IS NOT NULL`); err != nil {
		return fmt.Errorf("failed to check for agent_users: %w", err)
	}
	if !exists {
		return errors.New("table agent_users not found: be uses agentserver's users, " +
			"so start agentserver once against this database before be")
	}
	statements := []string{
		`ALTER TABLE scans ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES agent_users(id)`,
		`CREATE INDEX IF NOT EXISTS scans_user_id ON scans (user_id)`,
		`ALTER TABLE privatetokens ADD COLUMN IF NOT EXISTS user_id BIGINT REFERENCES agent_users(id)`,
		`CREATE INDEX IF NOT EXISTS privatetokens_user_id ON privatetokens (user_id)`,
		`CREATE TABLE IF NOT EXISTS web_sessions (
			token_hash    BYTEA PRIMARY KEY,
			user_id       BIGINT NOT NULL REFERENCES agent_users(id),
			created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
			last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
			expires_at    TIMESTAMPTZ NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS web_sessions_user_id ON web_sessions (user_id)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to add users to the schema: %w", err)
		}
	}
	return nil
}

// AssignUnownedTo gives scans and linked Google accounts that have no user
// (everything from before users) to username. Rows stay unowned, and so
// invisible, while that user doesn't exist; this runs at every start, so
// they're picked up once agentserver's `user add` creates it.
func AssignUnownedTo(username string) error {
	if username == "" {
		return nil
	}
	var unowned int
	err := db.Get(&unowned, `SELECT (SELECT count(*) FROM scans WHERE user_id IS NULL)
		+ (SELECT count(*) FROM privatetokens WHERE user_id IS NULL)`)
	if err != nil {
		return fmt.Errorf("failed to count unowned scans: %w", err)
	}
	if unowned == 0 {
		return nil
	}
	user, err := UserByName(username)
	if errors.Is(err, ErrNoSuchUser) {
		slog.Warn("Scans and accounts without a user stay hidden until their owner exists",
			"owner", username, "rows", unowned)
		return nil
	}
	if err != nil {
		return err
	}
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	scans, err := tx.Exec(`UPDATE scans SET user_id = $1 WHERE user_id IS NULL`, user.ID)
	if err != nil {
		return fmt.Errorf("failed to assign unowned scans: %w", err)
	}
	accounts, err := tx.Exec(`UPDATE privatetokens SET user_id = $1 WHERE user_id IS NULL`, user.ID)
	if err != nil {
		return fmt.Errorf("failed to assign unowned accounts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	nScans, _ := scans.RowsAffected()
	nAccounts, _ := accounts.RowsAffected()
	slog.Info("Assigned unowned scans and accounts", "owner", username, "scans", nScans, "accounts", nAccounts)
	return nil
}
