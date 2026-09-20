// Package store implements the checkpoint database used to make scans
// resumable: every file that has been hashed is recorded so an interrupted
// scan can restart without re-reading unchanged files.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// File statuses.
const (
	StatusHashed = "hashed"
	StatusError  = "error"
)

// FileRecord is one row of the files table: everything known about a single
// file on a single drive as of its last successful scan.
type FileRecord struct {
	DriveID      string
	RelPath      string
	Size         int64
	MTimeUnix    int64
	Mode         uint32
	QuickSig     string
	ContentHash  string
	HashAlgo     string
	Status       string
	ErrorMessage string
	ScannedAt    time.Time
}

// Store wraps the checkpoint SQLite database.
type Store struct {
	db *sql.DB
}

// Open creates (if needed) and opens the checkpoint database at
// <stateDir>/state.db, enabling WAL mode for crash-safety.
func Open(stateDir string) (*Store, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating state dir: %w", err)
	}
	dbPath := filepath.Join(stateDir, "state.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening checkpoint db: %w", err)
	}
	// A single writer connection avoids SQLITE_BUSY under WAL without
	// needing a busy-timeout retry loop; the scan pipeline already
	// serializes writes through one batcher.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting synchronous mode: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS drives (
			drive_id               TEXT PRIMARY KEY,
			scan_root              TEXT NOT NULL,
			last_scan_started_at   TIMESTAMP,
			last_scan_completed_at TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS files (
			drive_id      TEXT NOT NULL,
			relative_path TEXT NOT NULL,
			size          INTEGER NOT NULL,
			mtime_unix    INTEGER NOT NULL,
			mode          INTEGER NOT NULL,
			quick_sig     TEXT,
			content_hash  TEXT,
			hash_algo     TEXT,
			status        TEXT NOT NULL,
			error_message TEXT,
			scanned_at    TIMESTAMP NOT NULL,
			PRIMARY KEY (drive_id, relative_path)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_files_drive_hash ON files(drive_id, content_hash);`,
		`CREATE TABLE IF NOT EXISTS scan_runs (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			drive_id     TEXT NOT NULL,
			started_at   TIMESTAMP NOT NULL,
			finished_at  TIMESTAMP,
			files_seen   INTEGER,
			bytes_hashed INTEGER,
			interrupted  BOOLEAN
		);`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("running migration %q: %w", stmt, err)
		}
	}
	return nil
}

// UpsertDrive records (or updates) the drive's label -> scan root mapping.
func (s *Store) UpsertDrive(driveID, scanRoot string) error {
	_, err := s.db.Exec(`
		INSERT INTO drives (drive_id, scan_root) VALUES (?, ?)
		ON CONFLICT(drive_id) DO UPDATE SET scan_root = excluded.scan_root
	`, driveID, scanRoot)
	return err
}

// StartScanRun records the start of a scan attempt and returns its run ID.
func (s *Store) StartScanRun(driveID string) (int64, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`
		INSERT INTO scan_runs (drive_id, started_at, interrupted) VALUES (?, ?, 0)
	`, driveID, now)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`
		UPDATE drives SET last_scan_started_at = ? WHERE drive_id = ?
	`, now, driveID); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishScanRun records the outcome of a scan attempt.
func (s *Store) FinishScanRun(runID int64, driveID string, filesSeen, bytesHashed int64, interrupted bool) error {
	now := time.Now().UTC()
	if _, err := s.db.Exec(`
		UPDATE scan_runs SET finished_at = ?, files_seen = ?, bytes_hashed = ?, interrupted = ?
		WHERE id = ?
	`, now, filesSeen, bytesHashed, interrupted, runID); err != nil {
		return err
	}
	if interrupted {
		return nil
	}
	_, err := s.db.Exec(`
		UPDATE drives SET last_scan_completed_at = ? WHERE drive_id = ?
	`, now, driveID)
	return err
}

// Existing looks up the previously recorded row for (driveID, relPath), if
// any. Used by the scanner to decide whether a file can be skipped.
func (s *Store) Existing(driveID, relPath string) (*FileRecord, error) {
	row := s.db.QueryRow(`
		SELECT size, mtime_unix, mode, status
		FROM files WHERE drive_id = ? AND relative_path = ?
	`, driveID, relPath)
	var rec FileRecord
	rec.DriveID = driveID
	rec.RelPath = relPath
	if err := row.Scan(&rec.Size, &rec.MTimeUnix, &rec.Mode, &rec.Status); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

// UpsertFiles writes a batch of file records inside a single transaction.
func (s *Store) UpsertFiles(ctx context.Context, recs []FileRecord) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO files (drive_id, relative_path, size, mtime_unix, mode, quick_sig, content_hash, hash_algo, status, error_message, scanned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(drive_id, relative_path) DO UPDATE SET
			size = excluded.size,
			mtime_unix = excluded.mtime_unix,
			mode = excluded.mode,
			quick_sig = excluded.quick_sig,
			content_hash = excluded.content_hash,
			hash_algo = excluded.hash_algo,
			status = excluded.status,
			error_message = excluded.error_message,
			scanned_at = excluded.scanned_at
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range recs {
		if _, err := stmt.Exec(r.DriveID, r.RelPath, r.Size, r.MTimeUnix, r.Mode, r.QuickSig, r.ContentHash, r.HashAlgo, r.Status, r.ErrorMessage, r.ScannedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListFiles returns every recorded file for a drive with the given status
// (pass "" for all statuses).
func (s *Store) ListFiles(driveID, status string) ([]FileRecord, error) {
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = s.db.Query(`
			SELECT relative_path, size, mtime_unix, mode, quick_sig, content_hash, hash_algo, status, error_message, scanned_at
			FROM files WHERE drive_id = ?
		`, driveID)
	} else {
		rows, err = s.db.Query(`
			SELECT relative_path, size, mtime_unix, mode, quick_sig, content_hash, hash_algo, status, error_message, scanned_at
			FROM files WHERE drive_id = ? AND status = ?
		`, driveID, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FileRecord
	for rows.Next() {
		var r FileRecord
		r.DriveID = driveID
		var contentHash, hashAlgo, errMsg sql.NullString
		if err := rows.Scan(&r.RelPath, &r.Size, &r.MTimeUnix, &r.Mode, &r.QuickSig, &contentHash, &hashAlgo, &r.Status, &errMsg, &r.ScannedAt); err != nil {
			return nil, err
		}
		r.ContentHash = contentHash.String
		r.HashAlgo = hashAlgo.String
		r.ErrorMessage = errMsg.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// DriveScanRoot returns the recorded scan root for a drive label.
func (s *Store) DriveScanRoot(driveID string) (string, error) {
	row := s.db.QueryRow(`SELECT scan_root FROM drives WHERE drive_id = ?`, driveID)
	var root string
	if err := row.Scan(&root); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no scan recorded for drive %q (run 'driveagent scan' first)", driveID)
		}
		return "", err
	}
	return root, nil
}
