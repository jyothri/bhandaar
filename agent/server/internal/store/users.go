package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Errors returned by the user and agent queries.
var (
	ErrUserExists          = errors.New("user already exists")
	ErrNoSuchUser          = errors.New("no such user")
	ErrAgentOwnedByAnother = errors.New("agent belongs to another user")
)

// User is a row of agent_users.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	DisabledAt   *time.Time
}

// Disabled reports whether the user is disabled.
func (u User) Disabled() bool { return u.DisabledAt != nil }

// CreateUser adds a user.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string) (int64, error) {
	var id int64
	err := s.Pool.QueryRow(ctx,
		"INSERT INTO agent_users (username, password_hash) VALUES ($1, $2) RETURNING id",
		username, passwordHash).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, ErrUserExists
	}
	return id, err
}

// UserByName looks a user up, disabled or not.
func (s *Store) UserByName(ctx context.Context, username string) (User, error) {
	var u User
	err := s.Pool.QueryRow(ctx,
		"SELECT id, username, password_hash, created_at, disabled_at FROM agent_users WHERE username = $1",
		username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.DisabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNoSuchUser
	}
	return u, err
}

// ListUsers returns every user, by username.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.Pool.Query(ctx,
		"SELECT id, username, password_hash, created_at, disabled_at FROM agent_users ORDER BY username")
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) {
		var u User
		err := r.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.DisabledAt)
		return u, err
	})
}

// SetPassword replaces a user's password hash. It doesn't re-enable a
// disabled user, and existing refresh tokens stay valid.
func (s *Store) SetPassword(ctx context.Context, username, passwordHash string) error {
	tag, err := s.Pool.Exec(ctx,
		"UPDATE agent_users SET password_hash = $2 WHERE username = $1", username, passwordHash)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoSuchUser
	}
	return nil
}

// DisableUser disables a user and revokes all of their refresh tokens, in one
// transaction. It returns the number of tokens revoked. Access tokens already
// issued stay valid until they expire (AGENTSERVER_ACCESS_TTL).
func (s *Store) DisableUser(ctx context.Context, username string) (int64, error) {
	var revoked int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var id int64
		err := tx.QueryRow(ctx,
			"UPDATE agent_users SET disabled_at = coalesce(disabled_at, now()) WHERE username = $1 RETURNING id",
			username).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoSuchUser
		}
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE agent_refresh_tokens SET revoked_at = now(), revoked_reason = 'disabled'
			  WHERE user_id = $1 AND revoked_at IS NULL`, id)
		revoked = tag.RowsAffected()
		return err
	})
	return revoked, err
}

// Agent describes the agent a user logs in from.
type Agent struct {
	ID       string // UUID
	UserID   int64
	Hostname string
	OS       string
	Arch     string
	Version  string
}

// UpsertAgent records a login from an agent. An agent that is already bound
// to a different user gives ErrAgentOwnedByAnother and is left unchanged.
func (s *Store) UpsertAgent(ctx context.Context, a Agent) error {
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO agent_agents (id, user_id, hostname, os, arch, last_version)
		VALUES ($1, $2, nullif($3, ''), nullif($4, ''), nullif($5, ''), nullif($6, ''))
		ON CONFLICT (id) DO UPDATE SET
		  hostname     = coalesce(EXCLUDED.hostname, agent_agents.hostname),
		  os           = coalesce(EXCLUDED.os, agent_agents.os),
		  arch         = coalesce(EXCLUDED.arch, agent_agents.arch),
		  last_version = coalesce(EXCLUDED.last_version, agent_agents.last_version),
		  last_seen_at = now()
		WHERE agent_agents.user_id = EXCLUDED.user_id`,
		a.ID, a.UserID, a.Hostname, a.OS, a.Arch, a.Version)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAgentOwnedByAnother
	}
	return nil
}
