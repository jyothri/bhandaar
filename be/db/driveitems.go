package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"
)

// The living record of each linked account's Drive: one row per file and
// folder, keyed by Drive file ID, updated by every Drive scan. See
// docs/archive/browse.md, "Drive: a living record per account".

// DriveItem is a Drive file or folder, as the record keeps it.
type DriveItem struct {
	FileId string
	// The first parent's ID; empty at a root.
	ParentId  string
	Name      string
	IsDir     bool
	MimeType  string
	Size      int64
	Md5       string
	Modified  time.Time
	OwnedByMe bool
	Trashed   bool
	// An image's capture time, as the camera recorded it (local time, no
	// zone), and its dimensions; zero when Drive has none.
	CaptureTime   time.Time
	Width, Height int64
}

// DriveRecord is what a Drive scan of a linked account covers, for
// updating the account's record.
type DriveRecord struct {
	ClientKey string
	// The account's My Drive folder ID.
	MyDriveId string
	// The folder scanned, or "" for the whole Drive; with Recursive, its
	// subfolders too.
	FolderId  string
	Recursive bool
	// SawAll is set when the scan's query is unfiltered, so that what it
	// didn't see in its scope is gone from Drive. OwnedOnly is set when it
	// only looked at files the account owns.
	SawAll    bool
	OwnedOnly bool
}

func migrateDriveItems() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS drive_items (
			client_key     VARCHAR(100) NOT NULL,
			file_id        TEXT NOT NULL,
			parent_id      TEXT,
			name           TEXT NOT NULL,
			is_dir         BOOLEAN NOT NULL,
			mime_type      TEXT NOT NULL DEFAULT '',
			size           BIGINT NOT NULL DEFAULT 0,
			md5            TEXT NOT NULL DEFAULT '',
			modified       TIMESTAMPTZ,
			owned_by_me    BOOLEAN NOT NULL DEFAULT false,
			trashed        BOOLEAN NOT NULL DEFAULT false,
			last_seen_scan INT NOT NULL,
			last_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (client_key, file_id)
		)`,
		`CREATE INDEX IF NOT EXISTS drive_items_parent ON drive_items (client_key, parent_id)`,
		// Images' capture time and size, for matching Google Photos items.
		`ALTER TABLE drive_items ADD COLUMN IF NOT EXISTS capture_time TIMESTAMP`,
		`ALTER TABLE drive_items ADD COLUMN IF NOT EXISTS width INT`,
		`ALTER TABLE drive_items ADD COLUMN IF NOT EXISTS height INT`,
		`CREATE INDEX IF NOT EXISTS drive_items_md5 ON drive_items (client_key, md5) WHERE md5 <> ''`,
		// Per account: its My Drive folder, and when a scan last finished.
		`CREATE TABLE IF NOT EXISTS drive_accounts (
			client_key   VARCHAR(100) PRIMARY KEY,
			my_drive_id  TEXT NOT NULL,
			updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
	}
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("failed to add drive_items to the schema: %w", err)
		}
	}
	return nil
}

// SaveDriveScanToDb saves a Drive scan's rows, as SaveStatToDb does. With
// a record, it also upserts each row that carries a DriveItem into the
// account's record, and when the scan ends applies its deletions and
// rebuilds the account's folder totals.
func SaveDriveScanToDb(scanId int, record *DriveRecord, scanData <-chan FileData) {
	if record == nil {
		SaveStatToDb(scanId, scanData)
		return
	}
	if _, err := db.Exec(`INSERT INTO drive_accounts (client_key, my_drive_id) VALUES ($1, $2)
		ON CONFLICT (client_key) DO UPDATE SET my_drive_id = EXCLUDED.my_drive_id`,
		record.ClientKey, record.MyDriveId); err != nil {
		slog.Error("Failed to record the My Drive folder", "scan_id", scanId, "error", err)
	}
	for fd := range scanData {
		if !fd.RecordOnly {
			saveStat(scanId, fd)
		}
		// The My Drive folder is a root, which gets no row.
		if fd.Drive != nil && fd.Drive.FileId != record.MyDriveId {
			if err := upsertDriveItem(record.ClientKey, scanId, *fd.Drive); err != nil {
				slog.Error("Failed to update the Drive record, skipping",
					"scan_id", scanId, "file_id", fd.Drive.FileId, "error", err)
			}
		}
	}
	scan, err := GetScanById(scanId)
	if err != nil {
		slog.Error("Failed to get scan status", "scan_id", scanId, "error", err)
		return
	}
	failed := scan.Status == "Failed"
	if !failed && record.SawAll {
		if err := deleteUnseenDriveItems(scanId, *record); err != nil {
			slog.Error("Failed to apply the scan's deletions to the Drive record", "scan_id", scanId, "error", err)
		}
	}
	if _, err := db.Exec(`UPDATE drive_accounts SET updated_at = now() WHERE client_key = $1`, record.ClientKey); err != nil {
		slog.Error("Failed to record when the Drive record was updated", "scan_id", scanId, "error", err)
	}
	afterDriveScan(record.ClientKey)
	if !failed {
		if err := MarkScanCompleted(scanId); err != nil {
			slog.Error("Failed to mark scan complete", "scan_id", scanId, "error", err)
		}
	}
}

// afterDriveScan runs once a Drive scan has updated an account's record:
// it rebuilds the account's folder totals (see totals.go).
var afterDriveScan func(clientKey string)

func upsertDriveItem(clientKey string, scanId int, item DriveItem) error {
	var modified sql.NullTime
	if !item.Modified.IsZero() {
		modified = sql.NullTime{Time: item.Modified, Valid: true}
	}
	var captured sql.NullTime
	if !item.CaptureTime.IsZero() {
		captured = sql.NullTime{Time: item.CaptureTime, Valid: true}
	}
	_, err := db.Exec(`INSERT INTO drive_items AS d (client_key, file_id, parent_id, name, is_dir, mime_type,
			size, md5, modified, owned_by_me, trashed, last_seen_scan, last_seen_at, capture_time, width, height)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $9, $10, $11, $12, now(), $13, NULLIF($14, 0), NULLIF($15, 0))
		ON CONFLICT (client_key, file_id) DO UPDATE SET
			parent_id = EXCLUDED.parent_id, name = EXCLUDED.name, is_dir = EXCLUDED.is_dir,
			mime_type = EXCLUDED.mime_type, size = EXCLUDED.size, md5 = EXCLUDED.md5,
			modified = EXCLUDED.modified, owned_by_me = EXCLUDED.owned_by_me, trashed = EXCLUDED.trashed,
			last_seen_scan = EXCLUDED.last_seen_scan, last_seen_at = EXCLUDED.last_seen_at,
			capture_time = EXCLUDED.capture_time, width = EXCLUDED.width, height = EXCLUDED.height`,
		clientKey, item.FileId, item.ParentId, item.Name, item.IsDir, item.MimeType,
		item.Size, item.Md5, modified, item.OwnedByMe, item.Trashed, scanId, captured, item.Width, item.Height)
	return err
}

// driveScopeCTE is the rows in a scan's scope, as a CTE named scope: the
// whole account ($1) when $2 is empty, else what's under the folder $2,
// down to $3 levels.
const driveScopeCTE = `WITH RECURSIVE scope (file_id, depth) AS (
		SELECT file_id, 1 FROM drive_items
		WHERE client_key = $1 AND ($2 = '' OR parent_id = $2)
	UNION
		SELECT i.file_id, s.depth + 1 FROM drive_items i JOIN scope s ON i.parent_id = s.file_id
		WHERE $2 <> '' AND i.client_key = $1 AND s.depth < $3
	)`

// deleteUnseenDriveItems deletes, in record's scope, the files the scan's
// query would have matched but it didn't see, then the folders it didn't
// see that are left empty.
func deleteUnseenDriveItems(scanId int, record DriveRecord) error {
	depth := 1
	if record.Recursive {
		depth = 100 // guards against a loop of parents
	}
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()
	files, err := tx.Exec(driveScopeCTE+`
		DELETE FROM drive_items d USING (SELECT DISTINCT file_id FROM scope) s
		WHERE d.client_key = $1 AND d.file_id = s.file_id AND NOT d.is_dir
			AND d.last_seen_scan <> $4 AND NOT d.trashed AND (NOT $5 OR d.owned_by_me)`,
		record.ClientKey, record.FolderId, depth, scanId, record.OwnedOnly)
	if err != nil {
		return fmt.Errorf("failed to delete files: %w", err)
	}
	var folders int64
	for {
		// A folder is empty once what was under it is deleted, so repeat
		// until none are left, from the bottom up.
		result, err := tx.Exec(driveScopeCTE+`
			DELETE FROM drive_items d USING (SELECT DISTINCT file_id FROM scope) s
			WHERE d.client_key = $1 AND d.file_id = s.file_id AND d.is_dir AND d.last_seen_scan <> $4
				AND NOT EXISTS (SELECT 1 FROM drive_items c WHERE c.client_key = $1 AND c.parent_id = d.file_id)`,
			record.ClientKey, record.FolderId, depth, scanId)
		if err != nil {
			return fmt.Errorf("failed to delete folders: %w", err)
		}
		n, _ := result.RowsAffected()
		if n == 0 {
			break
		}
		folders += n
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}
	nFiles, _ := files.RowsAffected()
	if nFiles > 0 || folders > 0 {
		slog.Info("Deleted what the scan no longer found from the Drive record",
			"scan_id", scanId, "client_key", record.ClientKey, "files", nFiles, "folders", folders)
	}
	return nil
}
