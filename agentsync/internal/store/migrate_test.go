package store_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jyothri/bhandaar/agentsync/internal/store"
	"github.com/jyothri/bhandaar/agentsync/internal/testdb"
)

func versions(t *testing.T, pool *pgxpool.Pool) []int {
	t.Helper()
	rows, err := pool.Query(context.Background(), "SELECT version FROM agentsync_schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var vs []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		vs = append(vs, v)
	}
	return vs
}

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var ok bool
	if err := pool.QueryRow(context.Background(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestMigrateAppliesOnceAndIsIdempotent(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ { // later runs model restarts
		if err := store.Migrate(ctx, pool, store.Migrations()); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	vs := versions(t, pool)
	if len(vs) == 0 || vs[0] != 1 {
		t.Fatalf("versions = %v", vs)
	}
	for i, v := range vs {
		if v != i+1 {
			t.Fatalf("versions = %v, want 1..n once each", vs)
		}
	}
	for _, table := range []string{"agentsync_schema_migrations"} {
		if !tableExists(t, pool, table) {
			t.Errorf("table %s missing", table)
		}
	}
}

func TestMigrateAppliesOnlyNewMigrations(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	dir := fstest.MapFS{
		"0001_schema_migrations.sql": {Data: mustRead(t, "0001_schema_migrations.sql")},
		"0002_a.sql":                 {Data: []byte("CREATE TABLE a (id INT)")},
	}
	if err := store.Migrate(ctx, pool, dir); err != nil {
		t.Fatal(err)
	}
	// Re-running 0002 would fail (the table exists), so this also checks it isn't re-run.
	dir["0003_b.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE b (id INT)")}
	if err := store.Migrate(ctx, pool, dir); err != nil {
		t.Fatal(err)
	}
	if got := versions(t, pool); len(got) != 3 {
		t.Fatalf("versions = %v", got)
	}
}

func TestMigrateFailureRollsBack(t *testing.T) {
	pool := testdb.NewEmpty(t)
	ctx := context.Background()
	dir := fstest.MapFS{
		"0001_schema_migrations.sql": {Data: mustRead(t, "0001_schema_migrations.sql")},
		"0002_bad.sql":               {Data: []byte("CREATE TABLE half_done (id INT); SELECT no_such_column FROM half_done;")},
	}
	err := store.Migrate(ctx, pool, dir)
	if err == nil || !strings.Contains(err.Error(), "migration 2 (bad)") {
		t.Fatalf("err = %v, want migration 2 to fail", err)
	}
	if tableExists(t, pool, "half_done") {
		t.Error("the failed migration's table survived; it should have rolled back")
	}
	if got := versions(t, pool); len(got) != 1 || got[0] != 1 {
		t.Errorf("versions = %v, want [1]", got)
	}

	// Fixing the migration lets the next start apply it.
	dir["0002_bad.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE half_done (id INT)")}
	if err := store.Migrate(ctx, pool, dir); err != nil {
		t.Fatal(err)
	}
	if !tableExists(t, pool, "half_done") {
		t.Error("fixed migration not applied")
	}
}

func TestMigrateRejectsBadNumbering(t *testing.T) {
	pool := testdb.NewEmpty(t)
	for name, dir := range map[string]fstest.MapFS{
		"gap":       {"0001_a.sql": {Data: []byte("SELECT 1")}, "0003_c.sql": {Data: []byte("SELECT 1")}},
		"duplicate": {"0001_a.sql": {Data: []byte("SELECT 1")}, "001_b.sql": {Data: []byte("SELECT 1")}},
		"no number": {"init.sql": {Data: []byte("SELECT 1")}},
	} {
		if err := store.Migrate(context.Background(), pool, dir); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(store.Migrations(), name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
