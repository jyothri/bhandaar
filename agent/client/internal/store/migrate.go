package store

import (
	"context"
	"database/sql"
	"fmt"
)

// Schema versions. Version 0 is the original CREATE ... IF NOT EXISTS list in
// migrate; each later version is a Go migration applied once, in order,
// recorded in schema_version.
var migrations = []func(s *Store, tx *sql.Tx) error{
	1: migrateChangeFeed,
}

// SchemaVersion is the schema this build writes.
func SchemaVersion() int { return len(migrations) - 1 }

func (s *Store) runMigrations() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		id      INTEGER PRIMARY KEY CHECK (id = 1),
		version INTEGER NOT NULL
	)`); err != nil {
		return err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO schema_version (id, version) VALUES (1, 0)`); err != nil {
		return err
	}
	for v := 1; v < len(migrations); v++ {
		// The version is re-read inside the (immediate) transaction, so two
		// processes opening an old state.db at once apply each migration once.
		err := s.inTx(context.Background(), func(tx *sql.Tx) error {
			var cur int
			if err := tx.QueryRow(`SELECT version FROM schema_version WHERE id = 1`).Scan(&cur); err != nil {
				return err
			}
			if cur > SchemaVersion() {
				return fmt.Errorf("state.db has schema version %d, newer than this driveagent understands (%d); upgrade driveagent", cur, SchemaVersion())
			}
			if cur >= v {
				return nil
			}
			if err := migrations[v](s, tx); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE schema_version SET version = ? WHERE id = 1`, v)
			return err
		})
		if err != nil {
			return fmt.Errorf("upgrading state.db to schema version %d: %w", v, err)
		}
	}
	return nil
}

// inTx runs f in a transaction (BEGIN IMMEDIATE, see OpenWith), committing
// if it returns nil.
func (s *Store) inTx(ctx context.Context, f func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := f(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func hasColumn(tx *sql.Tx, table, column string) (bool, error) {
	var n int
	err := tx.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n)
	return n > 0, err
}

// migrateChangeFeed is schema version 1: the versioned change feed that
// remote sync uploads (docs/specs/remote-sync-agent.md, "Change feed in
// state.db"), the synced marker's tables, and the drive identity columns.
func migrateChangeFeed(s *Store, tx *sql.Tx) error {
	columns := []struct{ table, column, def string }{
		{"drives", "sync_stream_id", "TEXT"},
		{"drives", "synced_version", "INTEGER NOT NULL DEFAULT 0"},
		{"drives", "synced_at", "TIMESTAMP"},
		{"drives", "fs_uuid", "TEXT"},
		{"drives", "fs_type", "TEXT"},
		{"drives", "fs_uuid_source", "TEXT"},
		{"drives", "hw_serial", "TEXT"},
		{"drives", "identity_seen_at", "TIMESTAMP"},
		{"files", "row_version", "INTEGER NOT NULL DEFAULT 0"},
		{"dir_listings", "row_version", "INTEGER NOT NULL DEFAULT 0"},
		{"scan_runs", "row_version", "INTEGER NOT NULL DEFAULT 0"},
	}
	for _, c := range columns {
		// ALTER TABLE ADD COLUMN isn't idempotent; guard it.
		ok, err := hasColumn(tx, c.table, c.column)
		if err != nil {
			return err
		}
		if !ok {
			if _, err := tx.Exec(`ALTER TABLE ` + c.table + ` ADD COLUMN ` + c.column + ` ` + c.def); err != nil {
				return err
			}
		}
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sync_clock (id INTEGER PRIMARY KEY CHECK (id = 1), v INTEGER NOT NULL)`,
		`INSERT OR IGNORE INTO sync_clock (id, v) VALUES (1, 0)`,
		// Synced ranges above the watermark: every entry of the drive with
		// from_version < v <= to_version is on the remote.
		`CREATE TABLE IF NOT EXISTS sync_ranges (
			drive_id      TEXT NOT NULL,
			from_version  INTEGER NOT NULL,
			to_version    INTEGER NOT NULL,
			PRIMARY KEY (drive_id, from_version)
		)`,
		// Entries the server rejected (acked but not stored).
		`CREATE TABLE IF NOT EXISTS sync_rejected (
			drive_id       TEXT NOT NULL,
			row_version    INTEGER NOT NULL,
			kind           TEXT NOT NULL,
			relative_path  TEXT NOT NULL,
			child_name     TEXT NOT NULL DEFAULT '',
			reason         TEXT NOT NULL,
			rejected_at    TIMESTAMP NOT NULL,
			PRIMARY KEY (drive_id, row_version)
		)`,
		// Deletions, so they can be uploaded.
		`CREATE TABLE IF NOT EXISTS sync_tombstones (
			drive_id       TEXT NOT NULL,
			kind           TEXT NOT NULL,
			relative_path  TEXT NOT NULL,
			child_name     TEXT NOT NULL DEFAULT '',
			row_version    INTEGER NOT NULL,
			PRIMARY KEY (drive_id, kind, relative_path, child_name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_files_feed      ON files(drive_id, row_version)`,
		`CREATE INDEX IF NOT EXISTS idx_dir_feed        ON dir_listings(drive_id, row_version)`,
		`CREATE INDEX IF NOT EXISTS idx_runs_feed       ON scan_runs(drive_id, row_version)`,
		`CREATE INDEX IF NOT EXISTS idx_tombstones_feed ON sync_tombstones(drive_id, row_version)`,
		// For looking at the marker by hand; the uploader reads the feed
		// with explicit range queries instead.
		`CREATE VIEW IF NOT EXISTS sync_feed_status AS
		SELECT f.drive_id, f.kind, f.op, f.row_version, f.relative_path, f.child_name,
		       (f.row_version <= d.synced_version
		        OR EXISTS (SELECT 1 FROM sync_ranges r
		                   WHERE r.drive_id = f.drive_id
		                     AND f.row_version > r.from_version AND f.row_version <= r.to_version)) AS synced
		FROM (
		  SELECT drive_id, 'file' AS kind, 'upsert' AS op, row_version, relative_path, '' AS child_name FROM files
		  UNION ALL SELECT drive_id, 'dir_child', 'upsert', row_version, relative_path, child_name FROM dir_listings
		  UNION ALL SELECT drive_id, 'scan_run', 'upsert', row_version, CAST(id AS TEXT), '' FROM scan_runs
		  UNION ALL SELECT drive_id, kind, 'delete', row_version, relative_path, child_name FROM sync_tombstones
		) f JOIN drives d USING (drive_id)`,
		`CREATE VIEW IF NOT EXISTS sync_summary AS
		SELECT d.drive_id, d.synced_version, d.synced_at,
		       (SELECT count(*) FROM sync_ranges r WHERE r.drive_id = d.drive_id) AS ranges,
		       (SELECT count(*) FROM sync_feed_status s WHERE s.drive_id = d.drive_id AND NOT s.synced) AS pending
		FROM drives d`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	return s.backfill(tx)
}

// backfillChunk is how many rowids each backfill UPDATE covers, and how
// often progress is printed. A variable so tests can lower it.
var backfillChunk int64 = 100_000

// backfill numbers every existing files, dir_listings and scan_runs row, in
// rowid order per table, and sets the clock past them. Rowids are distinct
// and increasing, so base+rowid is too; versions may have gaps, which the
// feed allows. Existing checkpoints become history, for the first sync.
func (s *Store) backfill(tx *sql.Tx) error {
	tables := []string{"files", "dir_listings", "scan_runs"}
	var total int64
	for _, t := range tables {
		var n int64
		if err := tx.QueryRow(`SELECT count(*) FROM ` + t).Scan(&n); err != nil {
			return err
		}
		total += n
	}
	if total == 0 {
		return nil
	}
	fmt.Fprintf(s.log, "upgrading state.db (one-time, %d rows)…\n", total)

	var base, done, reported int64
	if err := tx.QueryRow(`SELECT v FROM sync_clock WHERE id = 1`).Scan(&base); err != nil {
		return err
	}
	for _, t := range tables {
		var maxRowid sql.NullInt64
		if err := tx.QueryRow(`SELECT max(rowid) FROM ` + t).Scan(&maxRowid); err != nil {
			return err
		}
		if !maxRowid.Valid {
			continue
		}
		for lo := int64(0); lo < maxRowid.Int64; lo += backfillChunk {
			res, err := tx.Exec(`UPDATE `+t+` SET row_version = ? + rowid WHERE rowid > ? AND rowid <= ?`,
				base, lo, lo+backfillChunk)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			done += n
			if done-reported >= backfillChunk {
				fmt.Fprintf(s.log, "  %d / %d rows\n", done, total)
				reported = done
			}
		}
		base += maxRowid.Int64
	}
	if _, err := tx.Exec(`UPDATE sync_clock SET v = ? WHERE id = 1`, base); err != nil {
		return err
	}
	fmt.Fprintf(s.log, "upgraded state.db (%d rows)\n", done)
	return nil
}

// reserveVersions reserves n consecutive versions and returns the first.
// It must run inside a write transaction.
func reserveVersions(tx *sql.Tx, n int) (int64, error) {
	if n <= 0 {
		return 0, nil
	}
	var last int64
	if err := tx.QueryRow(`UPDATE sync_clock SET v = v + ? WHERE id = 1 RETURNING v`, n).Scan(&last); err != nil {
		return 0, err
	}
	return last - int64(n) + 1, nil
}
