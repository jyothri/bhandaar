// Package store is agentsync's Postgres access: the connection pool, schema
// migrations and queries. agentsync only touches its own agent_* tables.
package store

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jyothri/bhandaar/agentsync/internal/config"
)

// Store wraps the connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// DSN builds a connection URL from the DB settings.
func DSN(c config.DB) string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     c.Host + ":" + strconv.Itoa(c.Port),
		Path:     "/" + c.Name,
		RawQuery: url.Values{"sslmode": {c.SSLMode}}.Encode(),
	}
	return u.String()
}

// Open connects to Postgres and checks the connection.
func Open(ctx context.Context, c config.DB) (*Store, error) {
	pool, err := pgxpool.New(ctx, DSN(c))
	if err != nil {
		return nil, fmt.Errorf("connect to postgres: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to postgres at %s:%d: %w", c.Host, c.Port, err)
	}
	return &Store{Pool: pool}, nil
}

// Close closes the pool.
func (s *Store) Close() { s.Pool.Close() }

// Ping checks the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }
