package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrations returns the embedded migrations directory.
func Migrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err)
	}
	return sub
}

// migrationLockID is the advisory lock that keeps two processes (a server
// restart racing an admin command) from migrating at once.
const migrationLockID = 0x61676e7473796e63 // "agntsync"

type migration struct {
	version int
	name    string
	sql     string
}

// Migrate applies the embedded migrations.
func (s *Store) Migrate(ctx context.Context) error {
	return Migrate(ctx, s.Pool, Migrations())
}

// Migrate applies every migration in dir that isn't recorded in
// agentsync_schema_migrations yet, in version order, each in its own
// transaction together with its record. Files are named NNNN_name.sql;
// migration 1 creates the tracking table itself.
func Migrate(ctx context.Context, pool *pgxpool.Pool, dir fs.FS) error {
	migs, err := loadMigrations(dir)
	if err != nil {
		return err
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", int64(migrationLockID)); err != nil {
		return fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		// A fresh context: the lock must be released even if ctx is done.
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", int64(migrationLockID))
	}()

	applied, err := appliedVersions(ctx, conn.Conn())
	if err != nil {
		return err
	}
	for _, m := range migs {
		if applied[m.version] {
			continue
		}
		err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return err
			}
			_, err := tx.Exec(ctx,
				"INSERT INTO agentsync_schema_migrations (version, applied_at) VALUES ($1, now())", m.version)
			return err
		})
		if err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
		slog.Info("applied migration", "version", m.version, "name", m.name)
	}
	return nil
}

func loadMigrations(dir fs.FS) ([]migration, error) {
	names, err := fs.Glob(dir, "*.sql")
	if err != nil {
		return nil, err
	}
	seen := map[int]string{}
	var migs []migration
	for _, name := range names {
		num, rest, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || v <= 0 {
			return nil, fmt.Errorf("migration file %q: want NNNN_name.sql", name)
		}
		if prev, dup := seen[v]; dup {
			return nil, fmt.Errorf("migrations %q and %q share version %d", prev, name, v)
		}
		seen[v] = name
		b, err := fs.ReadFile(dir, name)
		if err != nil {
			return nil, err
		}
		migs = append(migs, migration{version: v, name: strings.TrimSuffix(rest, path.Ext(rest)), sql: string(b)})
	}
	sort.Slice(migs, func(i, j int) bool { return migs[i].version < migs[j].version })
	for i, m := range migs {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1, 2, 3, …: found %d where %d was expected", m.version, i+1)
		}
	}
	return migs, nil
}

func appliedVersions(ctx context.Context, conn *pgx.Conn) (map[int]bool, error) {
	var exists bool
	if err := conn.QueryRow(ctx,
		"SELECT to_regclass('agentsync_schema_migrations') IS NOT NULL").Scan(&exists); err != nil {
		return nil, err
	}
	applied := map[int]bool{}
	if !exists {
		return applied, nil
	}
	rows, err := conn.Query(ctx, "SELECT version FROM agentsync_schema_migrations")
	if err != nil {
		return nil, err
	}
	versions, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		return nil, err
	}
	for _, v := range versions {
		applied[v] = true
	}
	return applied, nil
}
