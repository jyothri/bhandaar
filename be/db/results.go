package db

import (
	"fmt"
	"time"
)

// A scan's results, for its results view. See
// docs/archive/request-drive-scans.md, "Results view".

// resultsPageSize is how many rows a page of results holds.
const resultsPageSize = 10

// ScanSummary is a scan's details and totals.
type ScanSummary struct {
	ScanId   int    `db:"id" json:"scan_id"`
	ScanType string `db:"scan_type" json:"scan_type"`
	Name     string `db:"name" json:"name"`
	// The linked account a Google scan ran as; empty for other scans.
	ClientKey    string    `db:"client_key" json:"client_key"`
	SearchPath   string    `db:"search_path" json:"search_path"`
	SearchFilter string    `db:"search_filter" json:"search_filter"`
	Status       string    `db:"status" json:"status"`
	StartTime    time.Time `db:"scan_start_time" json:"scan_start_time"`
	// Seconds as a decimal string, or "-1" while the scan has no end time,
	// as in Request History.
	DurationInSec string `db:"scan_duration_in_sec" json:"scan_duration_in_sec"`
	// Files, or for Gmail messages, and their total size. Folder rows
	// aren't counted.
	ItemCount   int   `db:"item_count" json:"item_count"`
	TotalBytes  int64 `db:"total_bytes" json:"total_bytes"`
	FolderCount int   `db:"folder_count" json:"folder_count"`
	// Google Photos items with no size (see docs/archive/photos-picker.md);
	// 0 for other scans.
	UnsizedCount int `db:"unsized_count" json:"unsized_count"`
}

// GetScanSummary returns a scan's summary. The caller checks its owner.
func GetScanSummary(scanId int) (ScanSummary, error) {
	summary := ScanSummary{}
	err := db.Get(&summary, `SELECT s.id, s.scan_type,
			COALESCE(sm.name, '') AS name, COALESCE(sm.client_key, '') AS client_key,
			COALESCE(sm.search_path, '') AS search_path,
			COALESCE(sm.search_filter, '') AS search_filter,
			COALESCE(s.status, 'Completed') AS status, s.scan_start_time,
			COALESCE(EXTRACT(EPOCH FROM (s.scan_end_time - s.scan_start_time)), -1) AS scan_duration_in_sec,
			CASE WHEN s.scan_type = 'gmail'
				THEN (SELECT count(*) FROM messagemetadata m WHERE m.scan_id = s.id)
				WHEN s.scan_type = 'google_photos'
				THEN (SELECT count(*) FROM photos_picked_items p WHERE p.scan_id = s.id)
				ELSE (SELECT count(*) FROM scandata d WHERE d.scan_id = s.id AND NOT COALESCE(d.is_dir, false))
			END AS item_count,
			CASE WHEN s.scan_type = 'gmail'
				THEN (SELECT COALESCE(sum(size_estimate), 0) FROM messagemetadata m WHERE m.scan_id = s.id)
				WHEN s.scan_type = 'google_photos'
				THEN (SELECT COALESCE(sum(size), 0) FROM photos_picked_items p WHERE p.scan_id = s.id)
				ELSE (SELECT COALESCE(sum(size), 0) FROM scandata d WHERE d.scan_id = s.id AND NOT COALESCE(d.is_dir, false))
			END AS total_bytes,
			(SELECT count(*) FROM scandata d WHERE d.scan_id = s.id AND COALESCE(d.is_dir, false)) AS folder_count,
			(SELECT count(*) FROM photos_picked_items p WHERE p.scan_id = s.id AND p.size IS NULL) AS unsized_count
		FROM scans s LEFT JOIN scanmetadata sm ON sm.scan_id = s.id
		WHERE s.id = $1
		LIMIT 1`, scanId)
	if err != nil {
		return ScanSummary{}, fmt.Errorf("failed to get summary of scan %d: %w", scanId, err)
	}
	return summary, nil
}

// ScanDataRow is a file or folder a scan found.
type ScanDataRow struct {
	Id      int        `db:"id" json:"scan_data_id"`
	Name    string     `db:"name" json:"name"`
	Path    string     `db:"path" json:"path"`
	Size    int64      `db:"size" json:"size"`
	ModTime *time.Time `db:"file_mod_time" json:"modified"`
	Md5Hash string     `db:"md5hash" json:"md5"`
	IsDir   bool       `db:"is_dir" json:"is_dir"`
	// For a folder, the files under it; 1 for a file.
	FileCount int `db:"file_count" json:"file_count"`
	// A cloud file's ID; null for local files.
	FileId *string `db:"file_id" json:"file_id"`
}

// GetScanDataFromDb returns a page of the files and folders a scan found,
// in tree order, so a folder comes just before what's in it, and the total
// number of rows. Paths are compared a segment at a time: compared whole,
// the locale's collation ignores "/", and "A/Bc" would land between "A/B"
// and "A/B/x".
func GetScanDataFromDb(scanId int, pageNo int) ([]ScanDataRow, int, error) {
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM scandata WHERE scan_id = $1`, scanId); err != nil {
		return nil, 0, fmt.Errorf("failed to get scan data count for scan %d: %w", scanId, err)
	}
	rows := []ScanDataRow{}
	err := db.Select(&rows, `SELECT id, COALESCE(name, '') AS name, COALESCE(path, '') AS path,
			COALESCE(size, 0) AS size, file_mod_time, COALESCE(md5hash, '') AS md5hash,
			COALESCE(is_dir, false) AS is_dir, COALESCE(file_count, 1) AS file_count, file_id
		FROM scandata WHERE scan_id = $1
		ORDER BY string_to_array(path, '/'), id LIMIT $2 OFFSET $3`, scanId, resultsPageSize, resultsPageSize*(pageNo-1))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scan data for scan %d, page %d: %w", scanId, pageNo, err)
	}
	return rows, count, nil
}

// MessageRow is a message a Gmail scan found.
type MessageRow struct {
	Id           int        `db:"id" json:"message_metadata_id"`
	MessageId    string     `db:"message_id" json:"message_id"`
	ThreadId     string     `db:"thread_id" json:"thread_id"`
	Labels       string     `db:"labels" json:"labels"`
	From         string     `db:"mail_from" json:"from"`
	To           string     `db:"mail_to" json:"to"`
	Subject      string     `db:"subject" json:"subject"`
	Date         *time.Time `db:"date" json:"date"`
	SizeEstimate int64      `db:"size_estimate" json:"size_estimate"`
}

// GetMessageMetadataFromDb returns a page of the messages a Gmail scan
// found, newest first, and the total number of them.
func GetMessageMetadataFromDb(scanId int, pageNo int) ([]MessageRow, int, error) {
	var count int
	if err := db.Get(&count, `SELECT count(*) FROM messagemetadata WHERE scan_id = $1`, scanId); err != nil {
		return nil, 0, fmt.Errorf("failed to get message count for scan %d: %w", scanId, err)
	}
	rows := []MessageRow{}
	err := db.Select(&rows, `SELECT id, COALESCE(message_id, '') AS message_id,
			COALESCE(thread_id, '') AS thread_id, COALESCE(labels, '') AS labels,
			COALESCE(mail_from, '') AS mail_from, COALESCE(mail_to, '') AS mail_to,
			COALESCE(subject, '') AS subject, date, COALESCE(size_estimate, 0) AS size_estimate
		FROM messagemetadata WHERE scan_id = $1
		ORDER BY date DESC NULLS LAST, id LIMIT $2 OFFSET $3`, scanId, resultsPageSize, resultsPageSize*(pageNo-1))
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get message metadata for scan %d, page %d: %w", scanId, pageNo, err)
	}
	return rows, count, nil
}
