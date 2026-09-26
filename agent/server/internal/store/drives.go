package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jyothri/bhandaar/agent/wire"
)

// ErrDriveNotOpen means the agent has no such drive (it must PUT it first).
var ErrDriveNotOpen = errors.New("drive not open")

// NormalizeFSUUID uppercases and drops dashes (as the agent does).
func NormalizeFSUUID(s string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), "-", ""))
}

// sameSourceTypes are filesystems whose ID differs between Linux and macOS
// (macOS synthesizes a VolumeUUID), so they only match within one source.
var sameSourceTypes = map[string]bool{"vfat": true, "fat": true, "fat16": true, "fat32": true, "msdos": true, "exfat": true, "ntfs": true}

type drivePK struct {
	pk               int64
	streamID         string
	physicalDriveID  *int64
	fsUUID, fsType   string
	source, hwSerial string
}

// OpenDrive creates or re-opens one of an agent's drives, in one
// transaction with the drive row locked (PUT /agent/v1/drives/{drive_id}):
//
//   - no row: create it, with no ranges;
//   - same stream: update the roots, and return the acked ranges;
//   - a different stream: delete the drive's data and ranges, and start
//     over at acked_version 0 (reset: true);
//   - in every case, store the reported identity, and match the physical
//     drive when the identity is new or changed.
func (s *Store) OpenDrive(ctx context.Context, userID int64, agentID, driveID string, req wire.DriveOpenRequest) (wire.DriveOpenResponse, error) {
	resp := wire.DriveOpenResponse{DriveID: driveID, StreamID: req.StreamID}
	var id wire.Identity
	if req.Identity != nil {
		id = *req.Identity
	}
	id.FSUUID = NormalizeFSUUID(id.FSUUID)
	id.FSType = strings.ToLower(strings.TrimSpace(id.FSType))
	id.HWSerial = strings.ToUpper(strings.TrimSpace(id.HWSerial))

	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		d, found, err := lockDrive(ctx, tx, agentID, driveID)
		if err != nil {
			return err
		}
		if !found {
			// Two PUTs of a new drive at once: one inserts, the other waits
			// on the unique key and then finds the row.
			if _, err := tx.Exec(ctx, `
				INSERT INTO agent_drives (agent_id, drive_id, stream_id, drive_root, backup_root)
				VALUES ($1, $2, $3, $4, $5) ON CONFLICT (agent_id, drive_id) DO NOTHING`,
				agentID, driveID, req.StreamID, req.DriveRoot, req.BackupRoot); err != nil {
				return err
			}
			if d, found, err = lockDrive(ctx, tx, agentID, driveID); err != nil {
				return err
			}
			if !found {
				return errors.New("drive row vanished")
			}
			found = false // still a new drive: match it, and it isn't a reset
		}

		if !strings.EqualFold(d.streamID, req.StreamID) {
			for _, table := range []string{"agent_files", "agent_dir_listings", "agent_scan_runs", "agent_tombstones", "agent_sync_ranges"} {
				if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE drive_pk = $1`, d.pk); err != nil {
					return err
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE agent_drives SET stream_id = $2, acked_version = 0 WHERE id = $1`, d.pk, req.StreamID); err != nil {
				return err
			}
			resp.Reset = found
		}

		physical := d.physicalDriveID
		if !found || d.fsUUID != id.FSUUID || d.fsType != id.FSType || d.source != id.FSUUIDSource || d.hwSerial != id.HWSerial {
			if physical, err = matchPhysicalDrive(ctx, tx, userID, d.physicalDriveID, id); err != nil {
				return err
			}
		} else if physical != nil {
			if _, err := tx.Exec(ctx, `UPDATE agent_physical_drives SET last_seen_at = now() WHERE id = $1`, *physical); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE agent_drives SET drive_root = $2, backup_root = $3, fs_uuid = nullif($4, ''), fs_type = nullif($5, ''),
			       fs_uuid_source = nullif($6, ''), hw_serial = nullif($7, ''), physical_drive_id = $8
			 WHERE id = $1`,
			d.pk, req.DriveRoot, req.BackupRoot, id.FSUUID, id.FSType, id.FSUUIDSource, id.HWSerial, physical); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE agent_agents SET last_seen_at = now() WHERE id = $1`, agentID); err != nil {
			return err
		}

		if resp.AckedRanges, err = loadRanges(ctx, tx, d.pk); err != nil {
			return err
		}
		resp.PhysicalDrive, err = physicalInfo(ctx, tx, physical, agentID)
		return err
	})
	return resp, err
}

func lockDrive(ctx context.Context, tx pgx.Tx, agentID, driveID string) (drivePK, bool, error) {
	var d drivePK
	var fsUUID, fsType, source, serial *string
	err := tx.QueryRow(ctx, `
		SELECT id, stream_id::text, physical_drive_id, fs_uuid, fs_type, fs_uuid_source, hw_serial
		  FROM agent_drives WHERE agent_id = $1 AND drive_id = $2 FOR UPDATE`, agentID, driveID).
		Scan(&d.pk, &d.streamID, &d.physicalDriveID, &fsUUID, &fsType, &source, &serial)
	if errors.Is(err, pgx.ErrNoRows) {
		return d, false, nil
	}
	d.fsUUID, d.fsType, d.source, d.hwSerial = deref(fsUUID), deref(fsType), deref(source), deref(serial)
	return d, err == nil, err
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// matchPhysicalDrive links a drive row to the user's physical drive with the
// same filesystem ID (server spec, "Matching physical drives"). It locks the
// user's row first, so two agents opening the same new drive at once can't
// both create a physical drive for it.
//
//   - no filesystem ID: not linked (a serial identifies the disk, not the
//     partition);
//   - no candidates: a new physical drive;
//   - candidates whose serial matches, or where either side has none: the
//     same drive (the current link if it's one of them, else the oldest);
//     a serial the candidate lacked is filled in;
//   - every candidate has a different serial: a clone, recorded as a new
//     physical drive with clone_of the oldest candidate.
//
// For FAT, exFAT and NTFS, candidates must also have the same source.
func matchPhysicalDrive(ctx context.Context, tx pgx.Tx, userID int64, current *int64, id wire.Identity) (*int64, error) {
	if id.FSUUID == "" {
		return nil, nil
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM agent_users WHERE id = $1 FOR UPDATE`, userID); err != nil {
		return nil, err
	}
	query := `SELECT id, coalesce(hw_serial, '') FROM agent_physical_drives WHERE user_id = $1 AND fs_uuid = $2`
	args := []any{userID, id.FSUUID}
	if sameSourceTypes[id.FSType] {
		query += ` AND fs_uuid_source IS NOT DISTINCT FROM nullif($3, '')`
		args = append(args, id.FSUUIDSource)
	}
	rows, err := tx.Query(ctx, query+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		id     int64
		serial string
	}
	cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (candidate, error) {
		var c candidate
		return c, r.Scan(&c.id, &c.serial)
	})
	if err != nil {
		return nil, err
	}

	create := func(cloneOf *int64) (*int64, error) {
		var newID int64
		err := tx.QueryRow(ctx, `
			INSERT INTO agent_physical_drives (user_id, fs_uuid, fs_type, fs_uuid_source, hw_serial, clone_of)
			VALUES ($1, $2, nullif($3, ''), nullif($4, ''), nullif($5, ''), $6) RETURNING id`,
			userID, id.FSUUID, id.FSType, id.FSUUIDSource, id.HWSerial, cloneOf).Scan(&newID)
		return &newID, err
	}
	if len(cands) == 0 {
		return create(nil)
	}

	var same []candidate
	for _, c := range cands {
		if c.serial == "" || id.HWSerial == "" || c.serial == id.HWSerial {
			same = append(same, c)
		}
	}
	if len(same) == 0 {
		oldest := cands[0].id
		return create(&oldest)
	}
	chosen := same[0] // the oldest
	if current != nil {
		for _, c := range same {
			if c.id == *current {
				chosen = c
			}
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE agent_physical_drives SET last_seen_at = now(),
		       hw_serial = coalesce(hw_serial, nullif($2, '')),
		       fs_type = coalesce(fs_type, nullif($3, ''))
		 WHERE id = $1`, chosen.id, id.HWSerial, id.FSType); err != nil {
		return nil, err
	}
	return &chosen.id, nil
}

func loadRanges(ctx context.Context, tx pgx.Tx, pk int64) ([]wire.Range, error) {
	rows, err := tx.Query(ctx, `SELECT from_version, to_version FROM agent_sync_ranges WHERE drive_pk = $1 ORDER BY from_version`, pk)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (wire.Range, error) {
		var rg wire.Range
		return rg, r.Scan(&rg[0], &rg[1])
	})
	if out == nil {
		out = []wire.Range{}
	}
	return out, err
}

func physicalInfo(ctx context.Context, q pgx.Tx, physical *int64, agentID string) (*wire.PhysicalDrive, error) {
	if physical == nil {
		return nil, nil
	}
	p := &wire.PhysicalDrive{ID: *physical, Linked: []wire.LinkedDrive{}}
	if err := q.QueryRow(ctx, `SELECT clone_of FROM agent_physical_drives WHERE id = $1`, *physical).Scan(&p.CloneOf); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `
		SELECT coalesce(a.hostname, ''), d.drive_id, d.last_synced_at
		  FROM agent_drives d JOIN agent_agents a ON a.id = d.agent_id
		 WHERE d.physical_drive_id = $1 AND d.agent_id <> $2
		 ORDER BY a.hostname, d.drive_id`, *physical, agentID)
	if err != nil {
		return nil, err
	}
	p.Linked, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (wire.LinkedDrive, error) {
		var l wire.LinkedDrive
		var at *time.Time
		err := r.Scan(&l.Hostname, &l.DriveID, &at)
		l.LastSyncedAt = at
		return l, err
	})
	if p.Linked == nil {
		p.Linked = []wire.LinkedDrive{}
	}
	return p, err
}

// ListDrives returns an agent's drives (GET /agent/v1/drives).
func (s *Store) ListDrives(ctx context.Context, agentID string) ([]wire.Drive, error) {
	out := []wire.Drive{}
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, drive_id, stream_id::text, drive_root, backup_root, last_synced_at, physical_drive_id
			  FROM agent_drives WHERE agent_id = $1 ORDER BY drive_id`, agentID)
		if err != nil {
			return err
		}
		type row struct {
			pk       int64
			d        wire.Drive
			physical *int64
		}
		list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
			var x row
			err := r.Scan(&x.pk, &x.d.DriveID, &x.d.StreamID, &x.d.DriveRoot, &x.d.BackupRoot, &x.d.LastSyncedAt, &x.physical)
			return x, err
		})
		if err != nil {
			return err
		}
		for _, x := range list {
			if x.d.AckedRanges, err = loadRanges(ctx, tx, x.pk); err != nil {
				return err
			}
			if x.d.PhysicalDrive, err = physicalInfo(ctx, tx, x.physical, agentID); err != nil {
				return err
			}
			out = append(out, x.d)
		}
		return nil
	})
	return out, err
}
