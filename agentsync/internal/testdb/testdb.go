// Package testdb gives tests a Postgres schema of their own. Tests that need
// Postgres call New, which skips the test when AGENTSYNC_TEST_DB is unset.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jyothri/bhandaar/agentsync/internal/store"
)

// EnvVar holds the connection URL of the test database.
const EnvVar = "AGENTSYNC_TEST_DB"

// NewEmpty returns a pool whose search_path is a new, empty schema, dropped
// when the test ends.
func NewEmpty(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(EnvVar)
	if dsn == "" {
		t.Skipf("%s is not set; skipping a test that needs Postgres", EnvVar)
	}
	ctx := context.Background()

	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(b)

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatal(err)
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("drop test schema %s: %v", schema, err)
		}
		admin.Close()
	})
	return pool
}

// New returns a store on a new schema with every migration applied.
func New(t testing.TB) *store.Store {
	t.Helper()
	pool := NewEmpty(t)
	st := &store.Store{Pool: pool}
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}
