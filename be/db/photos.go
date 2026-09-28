package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"
)

// Google Photos, through the Picker API: picking sessions and the items
// each scan picked. See docs/specs/photos-picker.md, "Backend".

// A picking session's states.
const (
	PickWaiting   = "waiting"   // created, not picked yet
	PickScanning  = "scanning"  // picked; the scan is running
	PickDone      = "done"      // the scan finished, or failed
	PickExpired   = "expired"   // not picked in time
	PickCancelled = "cancelled" // cancelled from the UI
)

// ErrPickActive is returned when a user already has a session waiting or
// scanning.
var ErrPickActive = errors.New("a Photos pick is already in progress")

// PickerSession is a Google Photos picking session.
type PickerSession struct {
	SessionKey string `db:"session_key"` // ours, what the UI holds
	PickerId   string `db:"picker_id"`   // Google's; never sent to the UI
	UserID     int64  `db:"user_id"`
	ClientKey  string `db:"client_key"`
	PickerUri  string `db:"picker_uri"`
	State      string `db:"state"`
	// When the user has to have picked by.
	PickBy time.Time     `db:"pick_by"`
	ScanId sql.NullInt64 `db:"scan_id"`
}

// PickedItem is a media item picked in a Google Photos scan.
type PickedItem struct {
	MediaItemId  string     `db:"media_item_id" json:"media_item_id"`
	MediaType    string     `db:"media_type" json:"media_type"` // PHOTO or VIDEO
	MimeType     string     `db:"mime_type" json:"mime_type"`
	Filename     string     `db:"filename" json:"filename"`
	CreateTime   *time.Time `db:"create_time" json:"create_time"`
	Width        *int       `db:"width" json:"width"`
	Height       *int       `db:"height" json:"height"`
	CameraMake   string     `db:"camera_make" json:"camera_make"`
	CameraModel  string     `db:"camera_model" json:"camera_model"`
	FocalLength  *float64   `db:"focal_length" json:"focal_length"`
	FNumber      *float64   `db:"f_number" json:"f_number"`
	Iso          *int       `db:"iso" json:"iso"`
	ExposureTime string     `db:"exposure_time" json:"exposure_time"`
	Fps          *float64   `db:"fps" json:"fps"`
	Size         *int64     `db:"size" json:"size"` // bytes of Google's copy; nil when unavailable
	SizeSource   string     `db:"size_source" json:"size_source"`
	Md5Hash      string     `db:"md5hash" json:"md5"` // only when downloaded
}

// How a picked item's size was found.
const (
	SizeFromHead     = "head"
	SizeFromDownload = "download"
	SizeUnavailable  = "unavailable"
)

// migratePhotosPicker creates the Picker API's tables.
func migratePhotosPicker() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS photos_picker_sessions (
			id          serial PRIMARY KEY,
			session_key TEXT NOT NULL UNIQUE,
			picker_id   TEXT NOT NULL,
			user_id     BIGINT NOT NULL REFERENCES agent_users (id),
			client_key  VARCHAR(100) NOT NULL,
			picker_uri  TEXT NOT NULL,
			state       VARCHAR(20) NOT NULL,
			pick_by     TIMESTAMPTZ NOT NULL,
			scan_id     INT REFERENCES scans (id) ON DELETE SET NULL,
			created_on  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		// One pick at a time per user.
		`CREATE UNIQUE INDEX IF NOT EXISTS photos_picker_sessions_active
			ON photos_picker_sessions (user_id) WHERE state IN ('waiting', 'scanning')`,
		`CREATE TABLE IF NOT EXISTS photos_picked_items (
			id            serial PRIMARY KEY,
			scan_id       INT NOT NULL REFERENCES scans (id),
			media_item_id TEXT NOT NULL,
			media_type    VARCHAR(10) NOT NULL,
			mime_type     TEXT NOT NULL DEFAULT '',
			filename      TEXT NOT NULL,
			create_time   TIMESTAMPTZ,
			width         INT,
			height        INT,
			camera_make   TEXT NOT NULL DEFAULT '',
			camera_model  TEXT NOT NULL DEFAULT '',
			focal_length  DOUBLE PRECISION,
			f_number      DOUBLE PRECISION,
			iso           INT,
			exposure_time TEXT NOT NULL DEFAULT '',
			fps           DOUBLE PRECISION,
			size          BIGINT,
			size_source   VARCHAR(12) NOT NULL,
			md5hash       TEXT NOT NULL DEFAULT '',
			UNIQUE (scan_id, media_item_id)
		)`,
		`CREATE INDEX IF NOT EXISTS photos_picked_items_scan ON photos_picked_items (scan_id)`,
	}
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("failed to create the Photos Picker tables: %w", err)
		}
	}
	return nil
}

// endPickerSessions ends the sessions a restart cut off: their pollers and
// scans ran in this process. Waiting ones expire; scanning ones are done,
// and markInterruptedScans has failed their scans.
func endPickerSessions() error {
	res, err := db.Exec(`UPDATE photos_picker_sessions
		SET state = CASE state WHEN 'waiting' THEN 'expired' ELSE 'done' END
		WHERE state IN ('waiting', 'scanning')`)
	if err != nil {
		return fmt.Errorf("failed to end Photos picking sessions: %w", err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		slog.Warn("Ended Photos picking sessions cut off by a restart", "count", n)
	}
	return nil
}

// SavePickerSession records a new session, in state waiting. It returns
// ErrPickActive when the user already has one waiting or scanning.
func SavePickerSession(s PickerSession) error {
	_, err := db.Exec(`INSERT INTO photos_picker_sessions
			(session_key, picker_id, user_id, client_key, picker_uri, state, pick_by)
		VALUES ($1, $2, $3, $4, $5, 'waiting', $6)`,
		s.SessionKey, s.PickerId, s.UserID, s.ClientKey, s.PickerUri, s.PickBy)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" && pqErr.Constraint == "photos_picker_sessions_active" {
		return ErrPickActive
	}
	if err != nil {
		return fmt.Errorf("failed to save Photos picking session: %w", err)
	}
	return nil
}

// HasActivePick reports whether userID has a session waiting or scanning.
func HasActivePick(userID int64) (bool, error) {
	var active bool
	err := db.Get(&active, `SELECT EXISTS (SELECT 1 FROM photos_picker_sessions
		WHERE user_id = $1 AND state IN ('waiting', 'scanning'))`, userID)
	if err != nil {
		return false, fmt.Errorf("failed to check for an active Photos pick: %w", err)
	}
	return active, nil
}

// GetPickerSession returns userID's session sessionKey, or ErrNotFound.
func GetPickerSession(userID int64, sessionKey string) (PickerSession, error) {
	var s PickerSession
	err := db.Get(&s, `SELECT session_key, picker_id, user_id, client_key, picker_uri, state, pick_by, scan_id
		FROM photos_picker_sessions WHERE user_id = $1 AND session_key = $2`, userID, sessionKey)
	if errors.Is(err, sql.ErrNoRows) {
		return PickerSession{}, ErrNotFound
	}
	if err != nil {
		return PickerSession{}, fmt.Errorf("failed to get Photos picking session: %w", err)
	}
	return s, nil
}

// MovePickerSession moves session sessionKey from state from to state to,
// and reports whether it was in state from. scanId, when not 0, is
// recorded too.
func MovePickerSession(sessionKey string, from string, to string, scanId int) (bool, error) {
	res, err := db.Exec(`UPDATE photos_picker_sessions
		SET state = $3, scan_id = COALESCE(NULLIF($4, 0), scan_id)
		WHERE session_key = $1 AND state = $2`, sessionKey, from, to, scanId)
	if err != nil {
		return false, fmt.Errorf("failed to move Photos picking session to %s: %w", to, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// SavePickedItems saves a scan's picked items, in batches of 100, one
// transaction each.
func SavePickedItems(scanId int, items []PickedItem) error {
	const batch = 100
	for start := 0; start < len(items); start += batch {
		tx, err := db.Beginx()
		if err != nil {
			return fmt.Errorf("failed to begin saving picked items: %w", err)
		}
		for _, it := range items[start:min(start+batch, len(items))] {
			if _, err := tx.Exec(`INSERT INTO photos_picked_items
					(scan_id, media_item_id, media_type, mime_type, filename, create_time, width, height,
						camera_make, camera_model, focal_length, f_number, iso, exposure_time, fps,
						size, size_source, md5hash)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)
				ON CONFLICT (scan_id, media_item_id) DO NOTHING`,
				scanId, it.MediaItemId, it.MediaType, it.MimeType, it.Filename, it.CreateTime, it.Width, it.Height,
				it.CameraMake, it.CameraModel, it.FocalLength, it.FNumber, it.Iso, it.ExposureTime, it.Fps,
				it.Size, it.SizeSource, it.Md5Hash); err != nil {
				tx.Rollback()
				return fmt.Errorf("failed to save picked item %s: %w", it.MediaItemId, err)
			}
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to save picked items: %w", err)
		}
	}
	return nil
}

// PickedItemPage is a page of a scan's picked items.
type PickedItemPage struct {
	Items    []PickedItem `json:"items"`
	Page     int          `json:"page"`
	PageSize int          `json:"page_size"`
	Total    int          `json:"total"`
}

// PickedItems returns a page of a scan's picked items, in the order they
// were picked. The caller checks the scan's owner.
func PickedItems(scanId int, pageNo int) (PickedItemPage, error) {
	page := PickedItemPage{Items: []PickedItem{}, Page: max(pageNo, 1), PageSize: resultsPageSize}
	if err := db.Get(&page.Total, `SELECT count(*) FROM photos_picked_items WHERE scan_id = $1`, scanId); err != nil {
		return PickedItemPage{}, fmt.Errorf("failed to count the picked items of scan %d: %w", scanId, err)
	}
	if err := db.Select(&page.Items, `SELECT media_item_id, media_type, mime_type, filename, create_time,
			width, height, camera_make, camera_model, focal_length, f_number, iso, exposure_time, fps,
			size, size_source, md5hash
		FROM photos_picked_items WHERE scan_id = $1 ORDER BY id LIMIT $2 OFFSET $3`,
		scanId, resultsPageSize, resultsPageSize*(page.Page-1)); err != nil {
		return PickedItemPage{}, fmt.Errorf("failed to list the picked items of scan %d: %w", scanId, err)
	}
	return page, nil
}
