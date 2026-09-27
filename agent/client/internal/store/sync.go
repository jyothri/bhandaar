package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jyothri/bhandaar/agent/wire"
)

// The synced marker (docs/specs/remote-sync-agent.md, "Synced marker"): per
// drive, a copy of the server's acked ranges as of the last
// acknowledgement. The range starting at 0 is the watermark
// (drives.synced_version); the rest are in sync_ranges. Only the uploader
// (internal/syncer) writes it, under the drive's upload lock.

// ErrStreamChanged means a marker write was refused because the drive's
// stream is no longer the one the caller read: ClearDrive (--replace-root)
// or another uploader changed it meanwhile.
var ErrStreamChanged = errors.New("the drive's sync stream changed meanwhile")

// Marker is a drive's synced marker.
type Marker struct {
	StreamID  string // "" until the drive is first uploaded
	Watermark int64
	Ranges    []wire.Range // above the watermark, sorted
	SyncedAt  time.Time    // zero if never
}

// AckedRanges is the marker in the server's form: the watermark as (0, W]
// (if W > 0), then the other ranges.
func (m Marker) AckedRanges() []wire.Range {
	out := []wire.Range{}
	if m.Watermark > 0 {
		out = append(out, wire.Range{0, m.Watermark})
	}
	return append(out, m.Ranges...)
}

// SyncDrive is what the uploader needs to know about one drive.
type SyncDrive struct {
	DriveID, DriveRoot, BackupRoot string
	Identity                       DriveIdentity
	Marker                         Marker
}

// DriveIDs lists every drive in state.db, sorted.
func (s *Store) DriveIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT drive_id FROM drives ORDER BY drive_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SyncDrive reads a drive's roots, identity and marker.
func (s *Store) SyncDrive(driveID string) (SyncDrive, error) {
	d := SyncDrive{DriveID: driveID}
	var stream sql.NullString
	var syncedAt sql.NullTime
	err := s.db.QueryRow(`SELECT drive_root, backup_root, sync_stream_id, synced_version, synced_at FROM drives WHERE drive_id = ?`, driveID).
		Scan(&d.DriveRoot, &d.BackupRoot, &stream, &d.Marker.Watermark, &syncedAt)
	if err == sql.ErrNoRows {
		return d, fmt.Errorf("no drive %q recorded", driveID)
	}
	if err != nil {
		return d, err
	}
	d.Marker.StreamID, d.Marker.SyncedAt = stream.String, syncedAt.Time
	if d.Identity, err = s.GetDriveIdentity(driveID); err != nil {
		return d, err
	}
	rows, err := s.db.Query(`SELECT from_version, to_version FROM sync_ranges WHERE drive_id = ? ORDER BY from_version`, driveID)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	for rows.Next() {
		var r wire.Range
		if err := rows.Scan(&r[0], &r[1]); err != nil {
			return d, err
		}
		d.Marker.Ranges = append(d.Marker.Ranges, r)
	}
	return d, rows.Err()
}

// Clock returns the feed's version clock: every committed feed entry has a
// version at or below it.
func (s *Store) Clock() (int64, error) {
	var v int64
	err := s.db.QueryRow(`SELECT v FROM sync_clock WHERE id = 1`).Scan(&v)
	return v, err
}

func nullString(v string) sql.NullString { return sql.NullString{String: v, Valid: v != ""} }

// SetStream replaces the drive's stream id old ("" for none) with a new
// one, and clears its marker and sync_rejected: the server will start the
// drive over. ErrStreamChanged if the stream isn't old any more.
func (s *Store) SetStream(driveID, old, streamID string) error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE drives SET sync_stream_id = ?, synced_version = 0, synced_at = NULL
			WHERE drive_id = ? AND sync_stream_id IS ?`, streamID, driveID, nullString(old))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrStreamChanged
		}
		return clearSyncRows(tx, driveID)
	})
}

// ClearMarker empties the drive's marker and sync_rejected, keeping its
// stream: the server answered reset (it started the stream over).
func (s *Store) ClearMarker(driveID, streamID string) error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE drives SET synced_version = 0, synced_at = NULL WHERE drive_id = ? AND sync_stream_id = ?`,
			driveID, streamID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrStreamChanged
		}
		return clearSyncRows(tx, driveID)
	})
}

// ResetStreams forgets every drive's stream, marker and sync_rejected, for
// "login --new-agent": the new agent has no drives on the server, so each
// drive gets a new stream when it's next opened and is uploaded again in
// full. The scan data itself is kept.
func (s *Store) ResetStreams() error {
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE drives SET sync_stream_id = NULL, synced_version = 0, synced_at = NULL`); err != nil {
			return err
		}
		for _, t := range []string{"sync_ranges", "sync_rejected"} {
			if _, err := tx.Exec(`DELETE FROM ` + t); err != nil {
				return err
			}
		}
		return nil
	})
}

func clearSyncRows(tx *sql.Tx, driveID string) error {
	for _, t := range []string{"sync_ranges", "sync_rejected"} {
		if _, err := tx.Exec(`DELETE FROM `+t+` WHERE drive_id = ?`, driveID); err != nil {
			return err
		}
	}
	return nil
}

// FeedKey identifies a feed entry's key at a version. Path is the
// relative path (the parent, for a dir_child); for a scan_run it is the
// run id.
type FeedKey struct {
	Kind, Path, Child string
	V                 int64
}

// RejectedEntry is an entry the server acknowledged but didn't store.
type RejectedEntry struct {
	FeedKey
	Reason string
	At     time.Time
}

// Ack is what an acknowledged batch changes besides the ranges.
type Ack struct {
	// Rejected are recorded in sync_rejected.
	Rejected []RejectedEntry
	// Stored are the batch's other entries: an older sync_rejected row for
	// the same key is deleted, since the key is on the server now.
	Stored []FeedKey
}

// ReplaceMarker makes the drive's marker a copy of ranges, the server's
// acked ranges (sorted, non-overlapping), records ack (nil for none), and
// prunes the tombstones the new marker covers. ErrStreamChanged if the
// drive's stream isn't streamID any more.
func (s *Store) ReplaceMarker(driveID, streamID string, ranges []wire.Range, ack *Ack) error {
	var watermark int64
	if len(ranges) > 0 && ranges[0][0] == 0 {
		watermark, ranges = ranges[0][1], ranges[1:]
	}
	now := time.Now().UTC()
	return s.inTx(context.Background(), func(tx *sql.Tx) error {
		res, err := tx.Exec(`UPDATE drives SET synced_version = ?, synced_at = ? WHERE drive_id = ? AND sync_stream_id = ?`,
			watermark, now, driveID, streamID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrStreamChanged
		}
		if _, err := tx.Exec(`DELETE FROM sync_ranges WHERE drive_id = ?`, driveID); err != nil {
			return err
		}
		for _, r := range ranges {
			if _, err := tx.Exec(`INSERT INTO sync_ranges (drive_id, from_version, to_version) VALUES (?, ?, ?)`, driveID, r[0], r[1]); err != nil {
				return err
			}
		}
		if ack != nil {
			if err := recordAck(tx, driveID, ack); err != nil {
				return err
			}
		}
		// Synced tombstones protect nothing locally any more.
		_, err = tx.Exec(`DELETE FROM sync_tombstones WHERE drive_id = ?1 AND (row_version <= ?2
			OR EXISTS (SELECT 1 FROM sync_ranges r WHERE r.drive_id = ?1
			           AND sync_tombstones.row_version > r.from_version AND sync_tombstones.row_version <= r.to_version))`,
			driveID, watermark)
		return err
	})
}

func recordAck(tx *sql.Tx, driveID string, ack *Ack) error {
	if len(ack.Stored) > 0 {
		del, err := tx.Prepare(`DELETE FROM sync_rejected WHERE drive_id = ? AND kind = ? AND relative_path = ? AND child_name = ? AND row_version < ?`)
		if err != nil {
			return err
		}
		defer del.Close()
		for _, k := range ack.Stored {
			if _, err := del.Exec(driveID, k.Kind, k.Path, k.Child, k.V); err != nil {
				return err
			}
		}
	}
	if len(ack.Rejected) > 0 {
		ins, err := tx.Prepare(`INSERT INTO sync_rejected (drive_id, row_version, kind, relative_path, child_name, reason, rejected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(drive_id, row_version) DO UPDATE SET kind = excluded.kind, relative_path = excluded.relative_path,
				child_name = excluded.child_name, reason = excluded.reason, rejected_at = excluded.rejected_at`)
		if err != nil {
			return err
		}
		defer ins.Close()
		for _, r := range ack.Rejected {
			if _, err := ins.Exec(driveID, r.V, r.Kind, r.Path, r.Child, r.Reason, r.At.UTC()); err != nil {
				return err
			}
		}
	}
	return nil
}

// Rejected lists the drive's sync_rejected entries, oldest version first.
func (s *Store) Rejected(driveID string) ([]RejectedEntry, error) {
	rows, err := s.db.Query(`SELECT row_version, kind, relative_path, child_name, reason, rejected_at FROM sync_rejected
		WHERE drive_id = ? ORDER BY row_version`, driveID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RejectedEntry
	for rows.Next() {
		var r RejectedEntry
		if err := rows.Scan(&r.V, &r.Kind, &r.Path, &r.Child, &r.Reason, &r.At); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PendingCount counts the drive's feed entries the marker doesn't cover,
// through the sync_summary view. It reads every entry of the drive, so it
// is for explicit status commands, not for anything on a scan's path.
func (s *Store) PendingCount(driveID string) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT pending FROM sync_summary WHERE drive_id = ?`, driveID).Scan(&n)
	return n, err
}

// ScanRunKey is a scan_run's FeedKey path: its run id.
func ScanRunKey(runID int64) string { return strconv.FormatInt(runID, 10) }
