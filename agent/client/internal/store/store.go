// Package store implements the checkpoint database: scanned file metadata
// and hashes (making scans resumable), plus persisted comparison status
// and a folder-level rollup (making reports a pure, offline read). See
// docs/specs/drive-comparison-agent.md for the full design.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// File scan statuses.
const (
	StatusHashed = "hashed"
	StatusError  = "error"
)

// Comparison statuses, written by compare and read by report.
const (
	ComparisonCommon    = "common"
	ComparisonDiverged  = "diverged"
	ComparisonMissing   = "missing"
	ComparisonRelocated = "relocated"
)

// Folder statuses, layered on top of the comparison statuses above.
const (
	FolderUnscanned = "unscanned"
	FolderPartial   = "partial"
)

// FileRecord is one row of the files table: everything known about a single
// file on a single drive, both from scanning and from comparison.
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

	ComparisonStatus        string // "" = never compared
	ComparedAgainstDriveID  string
	CounterpartRelativePath string // only set when ComparisonStatus == relocated
	ComparedAt              time.Time
}

// DirChild is one immediate child of a directory, as recorded in
// dir_listings.
type DirChild struct {
	Name  string
	IsDir bool
}

// FolderStatus is the persisted, incrementally-maintained rollup for one
// folder (at any depth).
type FolderStatus struct {
	DriveID    string
	RelPath    string
	ParentPath string // "" for drive_root's own row (has no parent)
	IsRoot     bool
	Status     string
	Counts     map[string]int
	UpdatedAt  time.Time
}

// Store wraps the checkpoint SQLite database.
type Store struct {
	db  *sql.DB
	log io.Writer
}

// Options configures OpenWith.
type Options struct {
	// Log receives one-time upgrade progress; nil means os.Stderr.
	Log io.Writer
}

// Open creates (if needed) and opens the checkpoint database at
// <stateDir>/state.db, enabling WAL mode for crash-safety.
func Open(stateDir string) (*Store, error) {
	return OpenWith(stateDir, Options{})
}

// OpenWith is Open with options.
func OpenWith(stateDir string, opts Options) (*Store, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating state dir: %w", err)
	}
	dbPath := filepath.Join(stateDir, "state.db")
	if strings.Contains(dbPath, "?") {
		return nil, fmt.Errorf("state dir %q: the path can't contain '?'", stateDir)
	}
	if opts.Log == nil {
		opts.Log = os.Stderr
	}

	// Every write transaction is BEGIN IMMEDIATE (_txlock): feed writers
	// read before they write (SyncDirListings) and reserve versions after
	// that read. Under WAL, a deferred transaction that has to upgrade to a
	// writer after another process committed fails at once with
	// SQLITE_BUSY_SNAPSHOT, which busy_timeout doesn't cover; taking the
	// write lock at BEGIN avoids it. busy_timeout is in the DSN too, so it
	// applies to every connection the pool opens.
	db, err := sql.Open("sqlite", dbPath+"?_txlock=immediate&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening checkpoint db: %w", err)
	}
	// A single connection avoids SQLITE_BUSY between writers within this
	// process. It does NOT protect against a second, separate driveagent
	// process (e.g. scanning a different drive concurrently) opening the
	// same state.db at the same time — that's what busy_timeout below is
	// for: instead of failing immediately with "database is locked", a
	// writer waits up to that long for the other process's brief
	// (sub-second) write transaction to finish.
	db.SetMaxOpenConns(1)

	// Setting busy_timeout first does NOT, by itself, cover the race of
	// two processes creating a brand-new state.db at the same instant:
	// switching a fresh file to WAL mode is a one-time operation that can
	// still hit SQLITE_BUSY immediately rather than waiting, confirmed
	// empirically (two `scan` processes racing to create state.db for the
	// first time failed ~80% of the time even with busy_timeout already
	// set). execWithRetry adds an outer retry around each setup statement
	// for exactly this narrow window; once the file exists and is already
	// in WAL mode, ordinary busy_timeout waits handle concurrent access
	// fine on their own (confirmed: 0 failures in 15 runs against an
	// already-initialized state.db).
	if err := execWithRetry(func() error {
		_, err := db.Exec(`PRAGMA busy_timeout=5000;`)
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting busy timeout: %w", err)
	}
	if err := execWithRetry(func() error {
		_, err := db.Exec(`PRAGMA journal_mode=WAL;`)
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}
	if err := execWithRetry(func() error {
		_, err := db.Exec(`PRAGMA synchronous=NORMAL;`)
		return err
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting synchronous mode: %w", err)
	}

	s := &Store{db: db, log: opts.Log}
	if err := execWithRetry(s.migrate); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// execWithRetry retries run a handful of times with a short, increasing
// backoff when it fails with SQLITE_BUSY/"database is locked" — needed
// for the brand-new-database initialization race that busy_timeout alone
// doesn't cover (see the comment in Open).
func execWithRetry(run func() error) error {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		if err = run(); err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "SQLITE_BUSY") && !strings.Contains(err.Error(), "database is locked") {
			return err
		}
		time.Sleep(time.Duration(25*(attempt+1)) * time.Millisecond)
	}
	return err
}

// baseSchema is schema version 0: the tables as they were before schema
// versioning.
var baseSchema = []string{
	`CREATE TABLE IF NOT EXISTS drives (
			drive_id               TEXT PRIMARY KEY,
			drive_root             TEXT NOT NULL,
			backup_root            TEXT NOT NULL DEFAULT '',
			last_scan_started_at   TIMESTAMP,
			last_scan_completed_at TIMESTAMP
		);`,
	`CREATE TABLE IF NOT EXISTS files (
			drive_id                   TEXT NOT NULL,
			relative_path              TEXT NOT NULL,
			size                       INTEGER NOT NULL,
			mtime_unix                 INTEGER NOT NULL,
			mode                       INTEGER NOT NULL,
			quick_sig                  TEXT,
			content_hash               TEXT,
			hash_algo                  TEXT,
			status                     TEXT NOT NULL,
			error_message              TEXT,
			scanned_at                 TIMESTAMP NOT NULL,
			comparison_status          TEXT,
			compared_against_drive_id  TEXT,
			counterpart_relative_path  TEXT,
			compared_at                TIMESTAMP,
			PRIMARY KEY (drive_id, relative_path)
		);`,
	`CREATE INDEX IF NOT EXISTS idx_files_drive_hash ON files(drive_id, content_hash);`,
	`CREATE INDEX IF NOT EXISTS idx_files_drive_comparison_status ON files(drive_id, comparison_status);`,
	`CREATE TABLE IF NOT EXISTS scan_runs (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			drive_id     TEXT NOT NULL,
			started_at   TIMESTAMP NOT NULL,
			finished_at  TIMESTAMP,
			files_seen   INTEGER,
			bytes_hashed INTEGER,
			interrupted  BOOLEAN
		);`,
	`CREATE TABLE IF NOT EXISTS dir_listings (
			drive_id      TEXT NOT NULL,
			relative_path TEXT NOT NULL,
			child_name    TEXT NOT NULL,
			is_dir        BOOLEAN NOT NULL,
			first_seen_at TIMESTAMP NOT NULL,
			last_seen_at  TIMESTAMP NOT NULL,
			PRIMARY KEY (drive_id, relative_path, child_name)
		);`,
	`CREATE TABLE IF NOT EXISTS folder_status (
			drive_id      TEXT NOT NULL,
			relative_path TEXT NOT NULL,
			parent_path   TEXT,
			status        TEXT NOT NULL,
			counts_json   TEXT NOT NULL,
			updated_at    TIMESTAMP NOT NULL,
			PRIMARY KEY (drive_id, relative_path)
		);`,
	`CREATE INDEX IF NOT EXISTS idx_folder_status_parent ON folder_status(drive_id, parent_path);`,
}

func (s *Store) migrate() error {
	stmts := baseSchema
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("running migration %q: %w", stmt, err)
		}
	}
	return s.runMigrations()
}

// UpsertDrive records (or updates) the drive's label -> drive_root mapping.
// initialBackupRoot only takes effect the first time driveID is seen — on
// an existing drive, its currently-stored backup_root is left alone (use
// SetBackupRoot to change it explicitly).
func (s *Store) UpsertDrive(driveID, driveRoot, initialBackupRoot string) error {
	_, err := s.db.Exec(`
		INSERT INTO drives (drive_id, drive_root, backup_root) VALUES (?, ?, ?)
		ON CONFLICT(drive_id) DO UPDATE SET drive_root = excluded.drive_root
	`, driveID, driveRoot, initialBackupRoot)
	return err
}

// SetBackupRoot updates only the backup_root for an existing drive.
func (s *Store) SetBackupRoot(driveID, backupRoot string) error {
	_, err := s.db.Exec(`UPDATE drives SET backup_root = ? WHERE drive_id = ?`, backupRoot, driveID)
	return err
}

// DriveRoot returns the recorded drive_root for a drive label.
func (s *Store) DriveRoot(driveID string) (string, error) {
	root, found, err := s.ExistingDriveRoot(driveID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("no scan recorded for drive %q (run 'driveagent scan' first)", driveID)
	}
	return root, nil
}

// ExistingDriveRoot returns the drive_root previously recorded for driveID,
// and whether one was found at all — a brand-new drive_id has none. Used
// to detect a drive_id being repointed at a different root (see
// scan.Options.ReplaceRoot).
func (s *Store) ExistingDriveRoot(driveID string) (root string, found bool, err error) {
	row := s.db.QueryRow(`SELECT drive_root FROM drives WHERE drive_id = ?`, driveID)
	if err := row.Scan(&root); err != nil {
		if err == sql.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	return root, true, nil
}

// BackupRoot returns the recorded backup_root for a drive ("" means no
// stripping — backup_root == drive_root).
func (s *Store) BackupRoot(driveID string) (string, error) {
	row := s.db.QueryRow(`SELECT backup_root FROM drives WHERE drive_id = ?`, driveID)
	var root string
	if err := row.Scan(&root); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("no scan recorded for drive %q (run 'driveagent scan' first)", driveID)
		}
		return "", err
	}
	return root, nil
}

// ClearDrive deletes every checkpointed and derived row for driveID — used
// when repointing an existing drive_id at a different drive_root, so stale
// rows from the old root don't linger and pollute later compares/reports.
// It also forgets the drive's remote-sync state (tombstones, synced ranges,
// rejected entries, stream id and watermark), so the next upload starts a
// new stream and the server discards the old root's data. One transaction.
// The drives row itself is left for UpsertDrive to update in place.
func (s *Store) ClearDrive(driveID string) error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		tables := []string{"files", "scan_runs", "dir_listings", "folder_status", "sync_tombstones", "sync_ranges", "sync_rejected"}
		for _, t := range tables {
			if _, err := tx.Exec(`DELETE FROM `+t+` WHERE drive_id = ?`, driveID); err != nil {
				return fmt.Errorf("clearing %s: %w", t, err)
			}
		}
		_, err := tx.Exec(`UPDATE drives SET sync_stream_id = NULL, synced_version = 0, synced_at = NULL WHERE drive_id = ?`, driveID)
		return err
	})
}

// StartScanRun records the start of a scan attempt and returns its run ID.
func (s *Store) StartScanRun(driveID string) (int64, error) {
	now := time.Now().UTC()
	var id int64
	err := s.inTx(context.Background(), func(tx *sql.Tx) error {
		v, err := reserveVersions(tx, 1)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`
			INSERT INTO scan_runs (drive_id, started_at, interrupted, row_version) VALUES (?, ?, 0, ?)
		`, driveID, now, v)
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE drives SET last_scan_started_at = ? WHERE drive_id = ?`, now, driveID)
		return err
	})
	return id, err
}

// FinishScanRun records the outcome of a scan attempt.
func (s *Store) FinishScanRun(runID int64, driveID string, filesSeen, bytesHashed int64, interrupted bool) error {
	now := time.Now().UTC()
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		v, err := reserveVersions(tx, 1)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`
			UPDATE scan_runs SET finished_at = ?, files_seen = ?, bytes_hashed = ?, interrupted = ?, row_version = ?
			WHERE id = ?
		`, now, filesSeen, bytesHashed, interrupted, v, runID); err != nil {
			return err
		}
		if interrupted {
			return nil
		}
		_, err = tx.Exec(`UPDATE drives SET last_scan_completed_at = ? WHERE drive_id = ?`, now, driveID)
		return err
	})
}

// FileComparisonStatus returns the comparison_status of one file ("" if
// the file has no row, or has never been compared). Used by folder-status
// recomputation to classify a file child without loading the whole row.
func (s *Store) FileComparisonStatus(driveID, relPath string) (string, error) {
	row := s.db.QueryRow(`SELECT comparison_status FROM files WHERE drive_id = ? AND relative_path = ?`, driveID, relPath)
	var status sql.NullString
	if err := row.Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", err
	}
	return status.String, nil
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

// UpsertFiles writes a batch of freshly-(re)hashed file records inside a
// single transaction. Every row here represents either a brand-new file or
// one scan just decided needed re-hashing (size/mtime changed) — in both
// cases any previously-computed comparison_status is now stale, so it's
// reset to NULL (uncompared) rather than carried forward silently. Each row
// gets a new feed version, which supersedes any tombstone for its path.
func (s *Store) UpsertFiles(ctx context.Context, recs []FileRecord) error {
	if len(recs) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		first, err := reserveVersions(tx, len(recs))
		if err != nil {
			return err
		}
		stmt, err := tx.Prepare(`
			INSERT INTO files (drive_id, relative_path, size, mtime_unix, mode, quick_sig, content_hash, hash_algo, status, error_message, scanned_at, row_version)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(drive_id, relative_path) DO UPDATE SET
				size = excluded.size,
				mtime_unix = excluded.mtime_unix,
				mode = excluded.mode,
				quick_sig = excluded.quick_sig,
				content_hash = excluded.content_hash,
				hash_algo = excluded.hash_algo,
				status = excluded.status,
				error_message = excluded.error_message,
				scanned_at = excluded.scanned_at,
				row_version = excluded.row_version,
				comparison_status = NULL,
				compared_against_drive_id = NULL,
				counterpart_relative_path = NULL,
				compared_at = NULL
		`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		untomb, err := tx.Prepare(`DELETE FROM sync_tombstones WHERE drive_id = ? AND kind = 'file' AND relative_path = ? AND child_name = ''`)
		if err != nil {
			return err
		}
		defer untomb.Close()

		for i, r := range recs {
			if _, err := stmt.Exec(r.DriveID, r.RelPath, r.Size, r.MTimeUnix, r.Mode, r.QuickSig, r.ContentHash, r.HashAlgo, r.Status, r.ErrorMessage, r.ScannedAt, first+int64(i)); err != nil {
				return err
			}
			if _, err := untomb.Exec(r.DriveID, r.RelPath); err != nil {
				return err
			}
		}
		return nil
	})
}

const fileCols = `relative_path, size, mtime_unix, mode, quick_sig, content_hash, hash_algo, status, error_message, scanned_at,
	comparison_status, compared_against_drive_id, counterpart_relative_path, compared_at`

// GetFile returns the full row for one file, or nil if it has no row.
func (s *Store) GetFile(driveID, relPath string) (*FileRecord, error) {
	rows, err := s.db.Query(`SELECT `+fileCols+` FROM files WHERE drive_id = ? AND relative_path = ?`, driveID, relPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	recs, err := scanFileRows(driveID, rows)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, nil
	}
	return &recs[0], nil
}

// ListFiles returns every recorded file for a drive with the given scan
// status (pass "" for all statuses).
func (s *Store) ListFiles(driveID, status string) ([]FileRecord, error) {
	const cols = fileCols
	var rows *sql.Rows
	var err error
	if status == "" {
		rows, err = s.db.Query(`SELECT `+cols+` FROM files WHERE drive_id = ?`, driveID)
	} else {
		rows, err = s.db.Query(`SELECT `+cols+` FROM files WHERE drive_id = ? AND status = ?`, driveID, status)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileRows(driveID, rows)
}

// ListFilesByComparisonStatus returns every file for a drive currently
// carrying the given comparison status (e.g. "missing", for the global
// relocated-detection candidate pool).
func (s *Store) ListFilesByComparisonStatus(driveID, comparisonStatus string) ([]FileRecord, error) {
	rows, err := s.db.Query(`SELECT `+fileCols+` FROM files WHERE drive_id = ? AND comparison_status = ?`, driveID, comparisonStatus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFileRows(driveID, rows)
}

func scanFileRows(driveID string, rows *sql.Rows) ([]FileRecord, error) {
	var out []FileRecord
	for rows.Next() {
		var r FileRecord
		r.DriveID = driveID
		var contentHash, hashAlgo, errMsg sql.NullString
		var compStatus, comparedAgainst, counterpart sql.NullString
		var comparedAt sql.NullTime
		if err := rows.Scan(&r.RelPath, &r.Size, &r.MTimeUnix, &r.Mode, &r.QuickSig, &contentHash, &hashAlgo, &r.Status, &errMsg, &r.ScannedAt,
			&compStatus, &comparedAgainst, &counterpart, &comparedAt); err != nil {
			return nil, err
		}
		r.ContentHash = contentHash.String
		r.HashAlgo = hashAlgo.String
		r.ErrorMessage = errMsg.String
		r.ComparisonStatus = compStatus.String
		r.ComparedAgainstDriveID = comparedAgainst.String
		r.CounterpartRelativePath = counterpart.String
		r.ComparedAt = comparedAt.Time
		out = append(out, r)
	}
	return out, rows.Err()
}

// ComparisonUpdate is one file's freshly-computed comparison result, to be
// written via UpdateComparisonStatuses.
type ComparisonUpdate struct {
	DriveID                 string
	RelPath                 string
	ComparisonStatus        string
	ComparedAgainstDriveID  string
	CounterpartRelativePath string // only meaningful for relocated
}

// UpdateComparisonStatuses writes a batch of comparison results inside a
// single transaction.
func (s *Store) UpdateComparisonStatuses(ctx context.Context, updates []ComparisonUpdate) error {
	if len(updates) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		UPDATE files SET
			comparison_status = ?,
			compared_against_drive_id = ?,
			counterpart_relative_path = ?,
			compared_at = ?
		WHERE drive_id = ? AND relative_path = ?
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	now := time.Now().UTC()
	for _, u := range updates {
		var counterpart sql.NullString
		if u.CounterpartRelativePath != "" {
			counterpart = sql.NullString{String: u.CounterpartRelativePath, Valid: true}
		}
		if _, err := stmt.Exec(u.ComparisonStatus, u.ComparedAgainstDriveID, counterpart, now, u.DriveID, u.RelPath); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SyncDirListings records a batch of (parent, child) observations. entries
// gives, for each parent directory, its children as freshly observed —
// either by a full recursive walk or a fresh non-recursive ReadDir, both
// of which represent complete, current ground truth for that one parent.
//
// When deleteStale is true, each parent's children are made to match
// entries[parent] exactly: previously-recorded children not present now
// are deleted (they no longer exist on disk), in addition to upserting
// the ones that are. Callers should only pass true when they're certain
// their observation of every parent in entries was complete and
// error-free — see scan.Run's sawSoftError handling for why a single
// unreadable file anywhere in the walk makes this unsafe to assume.
//
// Feed versions: a new child, or one whose is_dir changed, gets a new
// version; a child that's merely seen again only has last_seen_at touched
// (otherwise every scan would re-upload every listing). Each deleted row
// leaves a 'dir_child' tombstone.
func (s *Store) SyncDirListings(ctx context.Context, driveID string, entries map[string][]DirChild, deleteStale bool) error {
	if len(entries) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		versions := &versionAlloc{tx: tx}
		lookupStmt, err := tx.Prepare(`SELECT is_dir FROM dir_listings WHERE drive_id = ? AND relative_path = ? AND child_name = ?`)
		if err != nil {
			return err
		}
		defer lookupStmt.Close()
		insertStmt, err := tx.Prepare(`
			INSERT INTO dir_listings (drive_id, relative_path, child_name, is_dir, first_seen_at, last_seen_at, row_version)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`)
		if err != nil {
			return err
		}
		defer insertStmt.Close()
		changedStmt, err := tx.Prepare(`UPDATE dir_listings SET is_dir = ?, last_seen_at = ?, row_version = ? WHERE drive_id = ? AND relative_path = ? AND child_name = ?`)
		if err != nil {
			return err
		}
		defer changedStmt.Close()
		touchStmt, err := tx.Prepare(`UPDATE dir_listings SET last_seen_at = ? WHERE drive_id = ? AND relative_path = ? AND child_name = ?`)
		if err != nil {
			return err
		}
		defer touchStmt.Close()
		untombStmt, err := tx.Prepare(`DELETE FROM sync_tombstones WHERE drive_id = ? AND kind = 'dir_child' AND relative_path = ? AND child_name = ?`)
		if err != nil {
			return err
		}
		defer untombStmt.Close()

		var listStmt, deleteStmt, tombStmt *sql.Stmt
		if deleteStale {
			if listStmt, err = tx.Prepare(`SELECT child_name, is_dir FROM dir_listings WHERE drive_id = ? AND relative_path = ?`); err != nil {
				return err
			}
			defer listStmt.Close()
			if deleteStmt, err = tx.Prepare(`DELETE FROM dir_listings WHERE drive_id = ? AND relative_path = ? AND child_name = ?`); err != nil {
				return err
			}
			defer deleteStmt.Close()
			if tombStmt, err = prepareTombstone(tx); err != nil {
				return err
			}
			defer tombStmt.Close()
		}

		type child struct {
			name  string
			isDir bool
		}
		listChildren := func(dirPath string) ([]child, error) {
			rows, err := listStmt.Query(driveID, dirPath)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			var out []child
			for rows.Next() {
				var c child
				if err := rows.Scan(&c.name, &c.isDir); err != nil {
					return nil, err
				}
				out = append(out, c)
			}
			return out, rows.Err()
		}
		remove := func(parent, name string) error {
			v, err := versions.take()
			if err != nil {
				return err
			}
			if _, err := deleteStmt.Exec(driveID, parent, name); err != nil {
				return err
			}
			_, err = tombStmt.Exec(driveID, "dir_child", parent, name, v)
			return err
		}

		// cascadePurge removes dirPath's own listing-as-parent row(s), then
		// recurses into any child that was itself a directory. Needed because
		// a directory disappearing entirely means we'll never again observe
		// it as a key in `entries` (there's nothing left to walk into), so
		// its former children would otherwise become permanently orphaned
		// (harmless — unreachable from drive_root — but untidy).
		var cascadePurge func(dirPath string) error
		cascadePurge = func(dirPath string) error {
			children, err := listChildren(dirPath)
			if err != nil {
				return err
			}
			for _, c := range children {
				if err := remove(dirPath, c.name); err != nil {
					return err
				}
			}
			for _, c := range children {
				if c.isDir {
					if err := cascadePurge(joinPath(dirPath, c.name)); err != nil {
						return err
					}
				}
			}
			return nil
		}

		now := time.Now().UTC()
		for parent, children := range entries {
			if deleteStale {
				wanted := make(map[string]bool, len(children))
				for _, c := range children {
					wanted[c.Name] = true
				}
				existing, err := listChildren(parent)
				if err != nil {
					return err
				}
				for _, c := range existing {
					if wanted[c.name] {
						continue
					}
					if err := remove(parent, c.name); err != nil {
						return err
					}
					if c.isDir {
						if err := cascadePurge(joinPath(parent, c.name)); err != nil {
							return err
						}
					}
				}
			}
			for _, c := range children {
				var wasDir bool
				err := lookupStmt.QueryRow(driveID, parent, c.Name).Scan(&wasDir)
				switch {
				case err == sql.ErrNoRows:
					v, err := versions.take()
					if err != nil {
						return err
					}
					if _, err := insertStmt.Exec(driveID, parent, c.Name, c.IsDir, now, now, v); err != nil {
						return err
					}
					if _, err := untombStmt.Exec(driveID, parent, c.Name); err != nil {
						return err
					}
				case err != nil:
					return err
				case wasDir != c.IsDir:
					v, err := versions.take()
					if err != nil {
						return err
					}
					if _, err := changedStmt.Exec(c.IsDir, now, v, driveID, parent, c.Name); err != nil {
						return err
					}
				default:
					if _, err := touchStmt.Exec(now, driveID, parent, c.Name); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

// versionAlloc hands out feed versions inside one transaction, reserving
// them from sync_clock in blocks. Versions left over at the end are simply
// never used: the feed allows gaps.
type versionAlloc struct {
	tx        *sql.Tx
	next, end int64 // next to hand out; end is the last reserved
}

const versionBlock = 1024

func (a *versionAlloc) take() (int64, error) {
	if a.next == 0 || a.next > a.end {
		first, err := reserveVersions(a.tx, versionBlock)
		if err != nil {
			return 0, err
		}
		a.next, a.end = first, first+versionBlock-1
	}
	v := a.next
	a.next++
	return v, nil
}

// ListFileRelativePaths returns every relative_path currently recorded for
// a drive (a cheap, single-column query — used to detect files that no
// longer exist on disk after a scan).
func (s *Store) ListFileRelativePaths(driveID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT relative_path FROM files WHERE drive_id = ?`, driveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteFiles removes a batch of files rows by relative_path — used when a
// scan determines they no longer exist on disk. Each removed row leaves a
// 'file' tombstone with a new feed version, so the deletion is uploaded.
func (s *Store) DeleteFiles(ctx context.Context, driveID string, relPaths []string) error {
	if len(relPaths) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		first, err := reserveVersions(tx, len(relPaths))
		if err != nil {
			return err
		}
		del, err := tx.Prepare(`DELETE FROM files WHERE drive_id = ? AND relative_path = ?`)
		if err != nil {
			return err
		}
		defer del.Close()
		tomb, err := prepareTombstone(tx)
		if err != nil {
			return err
		}
		defer tomb.Close()

		for i, p := range relPaths {
			res, err := del.Exec(driveID, p)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				continue // nothing was there; its version stays unused
			}
			if _, err := tomb.Exec(driveID, "file", p, "", first+int64(i)); err != nil {
				return err
			}
		}
		return nil
	})
}

// prepareTombstone records a deletion: (drive_id, kind, relative_path,
// child_name, row_version).
func prepareTombstone(tx *sql.Tx) (*sql.Stmt, error) {
	return tx.Prepare(`
		INSERT INTO sync_tombstones (drive_id, kind, relative_path, child_name, row_version) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(drive_id, kind, relative_path, child_name) DO UPDATE SET row_version = excluded.row_version
	`)
}

// ListChildren returns the known immediate children of a directory
// (relativePath == "" for drive_root itself).
func (s *Store) ListChildren(driveID, relativePath string) ([]DirChild, error) {
	rows, err := s.db.Query(`
		SELECT child_name, is_dir FROM dir_listings WHERE drive_id = ? AND relative_path = ?
	`, driveID, relativePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DirChild
	for rows.Next() {
		var c DirChild
		if err := rows.Scan(&c.Name, &c.IsDir); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetFolderStatus returns the persisted rollup for a folder, or nil if it
// has none (implicitly unscanned).
func (s *Store) GetFolderStatus(driveID, relativePath string) (*FolderStatus, error) {
	row := s.db.QueryRow(`
		SELECT parent_path, status, counts_json, updated_at FROM folder_status
		WHERE drive_id = ? AND relative_path = ?
	`, driveID, relativePath)
	var fs FolderStatus
	fs.DriveID = driveID
	fs.RelPath = relativePath
	var parentPath sql.NullString
	var countsJSON string
	if err := row.Scan(&parentPath, &fs.Status, &countsJSON, &fs.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	fs.ParentPath = parentPath.String
	fs.IsRoot = !parentPath.Valid
	if err := json.Unmarshal([]byte(countsJSON), &fs.Counts); err != nil {
		return nil, fmt.Errorf("decoding counts_json for %q: %w", relativePath, err)
	}
	return &fs, nil
}

// UpsertFolderStatus writes (or overwrites) a folder's rollup. parentPath
// should be "" only for drive_root itself (stored as NULL).
func (s *Store) UpsertFolderStatus(driveID, relativePath, parentPath, status string, counts map[string]int) error {
	countsJSON, err := json.Marshal(counts)
	if err != nil {
		return err
	}
	var parent sql.NullString
	if relativePath != "" {
		parent = sql.NullString{String: parentPath, Valid: true}
	}
	_, err = s.db.Exec(`
		INSERT INTO folder_status (drive_id, relative_path, parent_path, status, counts_json, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(drive_id, relative_path) DO UPDATE SET
			parent_path = excluded.parent_path,
			status = excluded.status,
			counts_json = excluded.counts_json,
			updated_at = excluded.updated_at
	`, driveID, relativePath, parent, status, string(countsJSON), time.Now().UTC())
	return err
}

// joinPath joins a parent relative path ("" for drive_root) with an
// immediate child name.
func joinPath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

// DriveIdentity is a drive's recorded identity (see internal/identity).
// Empty strings mean not known.
type DriveIdentity struct {
	FSUUID, FSType, FSUUIDSource, HWSerial string
	SeenAt                                 time.Time
}

// GetDriveIdentity returns the identity recorded for driveID (zero if none,
// or if the drive has no row).
func (s *Store) GetDriveIdentity(driveID string) (DriveIdentity, error) {
	var id DriveIdentity
	var uuid, typ, src, serial sql.NullString
	var seen sql.NullTime
	err := s.db.QueryRow(`SELECT fs_uuid, fs_type, fs_uuid_source, hw_serial, identity_seen_at FROM drives WHERE drive_id = ?`, driveID).
		Scan(&uuid, &typ, &src, &serial, &seen)
	if err == sql.ErrNoRows {
		return id, nil
	}
	if err != nil {
		return id, err
	}
	return DriveIdentity{FSUUID: uuid.String, FSType: typ.String, FSUUIDSource: src.String, HWSerial: serial.String, SeenAt: seen.Time}, nil
}

// SetDriveIdentity records a drive's identity. The drive must have a row
// (scan.Prepare records it first).
func (s *Store) SetDriveIdentity(driveID string, id DriveIdentity) error {
	null := func(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }
	res, err := s.db.Exec(`UPDATE drives SET fs_uuid = ?, fs_type = ?, fs_uuid_source = ?, hw_serial = ?, identity_seen_at = ? WHERE drive_id = ?`,
		null(id.FSUUID), null(id.FSType), null(id.FSUUIDSource), null(id.HWSerial), id.SeenAt.UTC(), driveID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no drive %q recorded", driveID)
	}
	return nil
}
