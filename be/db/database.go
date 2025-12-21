package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const (
	host     = "hdd_db"
	port     = 5432
	user     = "hddb"
	password = "hddb"
	dbname   = "hdd_db"
)

var db *sqlx.DB

// SetupDatabase initializes the database connection and runs migrations
func SetupDatabase() error {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	var err error
	db, err = sqlx.Open("postgres", psqlInfo)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	err = db.Ping()
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Successfully connected to database")

	if err := migrateDB(); err != nil {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	return nil
}

// Close closes the database connection
func Close() error {
	if db != nil {
		return db.Close()
	}
	return nil
}

func LogStartScan(scanType string) (int, error) {
	insert_row := `insert into scans
									(scan_type, created_on, scan_start_time)
								values
									($1, current_timestamp, current_timestamp) RETURNING id`
	lastInsertId := 0
	err := db.QueryRow(insert_row, scanType).Scan(&lastInsertId)
	if err != nil {
		return 0, fmt.Errorf("failed to insert scan for type %s: %w", scanType, err)
	}
	return lastInsertId, nil
}

func SaveScanMetadata(name string, searchPath string, searchFilter string, scanId int) error {
	insert_row := `insert into scanmetadata
			(name, search_path, search_filter, scan_id)
		values
			($1, $2, $3, $4) RETURNING id`
	_, err := db.Exec(insert_row, name, searchPath, searchFilter, scanId)
	if err != nil {
		return fmt.Errorf("failed to save scan metadata for scan %d (name=%s, path=%s): %w",
			scanId, name, searchPath, err)
	}
	return nil
}

func SaveMessageMetadataToDb(scanId int, username string, messageMetaData <-chan MessageMetadata) {
	for {
		mmd, more := <-messageMetaData
		if !more {
			// Channel closed - mark scan as complete if not already failed
			scan, err := GetScanById(scanId)
			if err != nil {
				slog.Error("Failed to get scan status",
					"scan_id", scanId,
					"error", err)
				return
			}

			if scan.Status != "Failed" {
				if err := MarkScanCompleted(scanId); err != nil {
					slog.Error("Failed to mark scan complete",
						"scan_id", scanId,
						"error", err)
				}
			}
			break
		}

		// Check for duplicates
		count_row := `select count(*) from messagemetadata where username= $1 AND message_id = $2 AND thread_id = $3`
		var count int
		err := db.Get(&count, count_row, username, mmd.MessageId, mmd.ThreadId)
		if err != nil {
			slog.Error("Failed to check for duplicate message, skipping",
				"scan_id", scanId,
				"message_id", mmd.MessageId,
				"username", username,
				"error", err)
			continue
		}
		if count > 0 {
			continue
		}

		insert_row := `insert into messagemetadata
			(message_id, thread_id, date, mail_from, mail_to, subject, size_estimate, labels, scan_id, username)
		values
			($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id`

		_, err = db.Exec(insert_row, mmd.MessageId, mmd.ThreadId, mmd.Date.UTC(), substr(mmd.From, 500),
			substr(mmd.To, 500), substr(mmd.Subject, 2000), mmd.SizeEstimate,
			substr(strings.Join(mmd.LabelIds, ","), 500), scanId, username)

		if err != nil {
			slog.Error("Failed to save message metadata, skipping",
				"scan_id", scanId,
				"message_id", mmd.MessageId,
				"username", username,
				"subject", substr(mmd.Subject, 50),
				"size_bytes", mmd.SizeEstimate,
				"error", err)
			continue
		}
	}
}

func SavePhotosMediaItemToDb(scanId int, photosMediaItem <-chan PhotosMediaItem) {
	for {
		pmi, more := <-photosMediaItem
		if !more {
			// Channel closed - mark scan as complete if not already failed
			scan, err := GetScanById(scanId)
			if err != nil {
				slog.Error("Failed to get scan status",
					"scan_id", scanId,
					"error", err)
				return
			}

			if scan.Status != "Failed" {
				if err := MarkScanCompleted(scanId); err != nil {
					slog.Error("Failed to mark scan complete",
						"scan_id", scanId,
						"error", err)
				}
			}
			break
		}

		// Use transaction for parent + children (atomicity required)
		tx, err := db.Beginx()
		if err != nil {
			slog.Error("Failed to begin transaction for photos media item, skipping",
				"scan_id", scanId,
				"media_item_id", pmi.MediaItemId,
				"error", err)
			continue
		}

		insert_row := `insert into photosmediaitem
			(media_item_id, product_url, mime_type, filename, size, scan_id, file_mod_time,
				contributor_display_name, md5hash)
		values
			($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`
		lastInsertId := 0
		err = tx.QueryRow(insert_row, pmi.MediaItemId, pmi.ProductUrl, pmi.MimeType, pmi.Filename,
			pmi.Size, scanId, pmi.FileModTime, pmi.ContributorDisplayName, pmi.Md5hash).Scan(&lastInsertId)

		if err != nil {
			tx.Rollback()
			slog.Error("Failed to insert photos media item, skipping",
				"scan_id", scanId,
				"media_item_id", pmi.MediaItemId,
				"filename", pmi.Filename,
				"error", err)
			continue
		}

		switch pmi.MimeType[:5] {
		case "image":
			insert_photo_row := `insert into photometadata
			(photos_media_item_id, camera_make, camera_model, focal_length, f_number, iso, exposure_time)
		values
			($1, $2, $3, $4, $5, $6, $7) RETURNING id`
			_, err = tx.Exec(insert_photo_row, lastInsertId, pmi.CameraMake, pmi.CameraModel, pmi.FocalLength,
				pmi.FNumber, pmi.Iso, pmi.ExposureTime)
			if err != nil {
				tx.Rollback()
				slog.Error("Failed to insert photo metadata, skipping",
					"scan_id", scanId,
					"media_item_id", pmi.MediaItemId,
					"camera", fmt.Sprintf("%s %s", pmi.CameraMake, pmi.CameraModel),
					"error", err)
				continue
			}
		case "video":
			insert_video_row := `insert into videometadata
			(photos_media_item_id, camera_make, camera_model, fps)
		values
			($1, $2, $3, $4) RETURNING id`
			_, err = tx.Exec(insert_video_row, lastInsertId, pmi.CameraMake, pmi.CameraModel, pmi.Fps)
			if err != nil {
				tx.Rollback()
				slog.Error("Failed to insert video metadata, skipping",
					"scan_id", scanId,
					"media_item_id", pmi.MediaItemId,
					"fps", pmi.Fps,
					"error", err)
				continue
			}
		default:
			slog.Warn("Unsupported mime type",
				"mime_type", pmi.MimeType,
				"media_item_id", pmi.MediaItemId)
		}

		if err := tx.Commit(); err != nil {
			slog.Error("Failed to commit transaction for photos media item, skipping",
				"scan_id", scanId,
				"media_item_id", pmi.MediaItemId,
				"error", err)
			continue
		}
	}
}

func SaveStatToDb(scanId int, scanData <-chan FileData) {
	for {
		fd, more := <-scanData
		if !more {
			// Channel closed - mark scan as complete if not already failed
			scan, err := GetScanById(scanId)
			if err != nil {
				slog.Error("Failed to get scan status",
					"scan_id", scanId,
					"error", err)
				return
			}

			if scan.Status != "Failed" {
				if err := MarkScanCompleted(scanId); err != nil {
					slog.Error("Failed to mark scan complete",
						"scan_id", scanId,
						"error", err)
				}
			}
			break
		}

		insert_row := `insert into scandata
			(name, path, size, file_mod_time, md5hash, scan_id, is_dir, file_count)
		values
			($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
		var err error
		if fd.IsDir {
			_, err = db.Exec(insert_row, fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.Md5Hash, scanId, fd.IsDir, fd.FileCount)
		} else {
			_, err = db.Exec(insert_row, fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.Md5Hash, scanId, fd.IsDir, nil)
		}

		if err != nil {
			slog.Error("Failed to save file scan data, skipping",
				"scan_id", scanId,
				"path", fd.FilePath,
				"is_dir", fd.IsDir,
				"size_bytes", fd.Size,
				"error", err)
			continue
		}
	}
}

func SaveOAuthToken(accessToken string, refreshToken string, displayName string, clientKey string, scope string, expiresIn int16, tokenType string) error {
	insert_row := `insert into privatetokens
			(access_token, refresh_token, display_name, client_key, scope, expires_in, token_type, created_on)
		values
			($1, $2, $3, $4, $5, $6, $7, current_timestamp) RETURNING id`
	_, err := db.Exec(insert_row, accessToken, refreshToken, displayName, clientKey, scope, expiresIn, tokenType)
	if err != nil {
		return fmt.Errorf("failed to save OAuth token for client %s: %w", clientKey, err)
	}
	return nil
}

func GetOAuthToken(clientKey string) (PrivateToken, error) {
	read_row :=
		`select id, access_token, refresh_token, display_name, client_key, created_on, scope, expires_in, token_type
		FROM privatetokens
		WHERE client_key = $1`
	tokenData := PrivateToken{}
	err := db.Get(&tokenData, read_row, clientKey)
	if err != nil {
		return PrivateToken{}, fmt.Errorf("failed to get OAuth token for client %s: %w", clientKey, err)
	}
	return tokenData, nil
}

func GetRequestAccountsFromDb() ([]Account, error) {
	read_row :=
		`select distinct display_name, client_key from privatetokens p
		`
	accounts := []Account{}
	err := db.Select(&accounts, read_row)
	if err != nil {
		return nil, fmt.Errorf("failed to get request accounts: %w", err)
	}
	return accounts, nil
}

func GetAccountsFromDb() ([]string, error) {
	read_row := `select distinct name  from scanmetadata
			where name is not null
			order by 1 `
	accounts := []string{}
	err := db.Select(&accounts, read_row)
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts: %w", err)
	}
	return accounts, nil
}

func GetScanRequestsFromDb(accountKey string) ([]ScanRequests, error) {
	if len(strings.TrimSpace(accountKey)) == 0 {
		return []ScanRequests{}, nil
	}
	read_row := `select distinct COALESCE(sm.name, '') as name, sm.search_filter, s.id,
			s.scan_type,
			scan_start_time AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles' as scan_start_time,
			COALESCE(EXTRACT(EPOCH FROM (scan_end_time - scan_start_time)), -1) as scan_duration_in_sec
			from scans s
			join scanmetadata sm on sm.scan_id = s.id
			where sm.name = $1
			group by sm.name, sm.search_filter, s.id, s.scan_start_time, s.scan_type
			order by s.id desc`
	scanRequests := []ScanRequests{}
	err := db.Select(&scanRequests, read_row, accountKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get scan requests for account %s: %w", accountKey, err)
	}
	return scanRequests, nil
}

func GetScansFromDb(pageNo int) ([]Scan, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)
	count_rows := `select count(*) from scans`
	read_row :=
		`select S.id, scan_type,
		 created_on AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles' as created_on,
		 scan_start_time AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles' as scan_start_time,
		 scan_end_time, CONCAT(search_path, search_filter) as metadata,
		 date_trunc('millisecond', COALESCE(scan_end_time,current_timestamp)-scan_start_time) as duration
	   from scans S LEFT JOIN scanmetadata SM
		 ON S.id = SM.scan_id
		 order by id limit $1 OFFSET $2
		`
	scans := []Scan{}
	var count int
	err := db.Select(&scans, read_row, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scans for page %d: %w", pageNo, err)
	}
	err = db.Get(&count, count_rows)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scan count: %w", err)
	}
	return scans, count, nil
}

func GetMessageMetadataFromDb(scanId int, pageNo int) ([]MessageMetadataRead, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)
	count_rows := `select count(*) from messagemetadata where scan_id = $1`
	read_row := `select id, message_id, thread_id, date, mail_from, mail_to,
							 subject, size_estimate, labels, scan_id
	             from messagemetadata
							 where scan_id = $1 order by id limit $2 offset $3`
	messageMetadata := []MessageMetadataRead{}
	var count int
	err := db.Get(&count, count_rows, scanId)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get message count for scan %d: %w", scanId, err)
	}
	err = db.Select(&messageMetadata, read_row, scanId, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get message metadata for scan %d, page %d: %w", scanId, pageNo, err)
	}
	return messageMetadata, count, nil
}

func GetPhotosMediaItemFromDb(scanId int, pageNo int) ([]PhotosMediaItemRead, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)
	count_rows := `select count(*) from photosmediaitem where scan_id = $1`
	read_row := `select id, media_item_id, product_url, mime_type, filename,
								size, file_mod_time, md5hash, scan_id, contributor_display_name
								from photosmediaitem
							 where scan_id = $1 order by id limit $2 offset $3`
	photosMediaItemRead := []PhotosMediaItemRead{}
	var count int
	err := db.Get(&count, count_rows, scanId)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get photo count for scan %d: %w", scanId, err)
	}
	err = db.Select(&photosMediaItemRead, read_row, scanId, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get photos for scan %d, page %d: %w", scanId, pageNo, err)
	}
	return photosMediaItemRead, count, nil
}

func GetScanDataFromDb(scanId int, pageNo int) ([]ScanData, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)
	count_rows := `select count(*) from scandata where scan_id = $1`
	read_row := `select * from scandata where scan_id = $1 order by id limit $2 offset $3`
	scandata := []ScanData{}
	var count int
	err := db.Get(&count, count_rows, scanId)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scan data count for scan %d: %w", scanId, err)
	}
	err = db.Select(&scandata, read_row, scanId, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scan data for scan %d, page %d: %w", scanId, pageNo, err)
	}
	return scandata, count, nil
}

func DeleteScan(scanId int) error {
	// Begin transaction
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Defer rollback - safe to call even after commit
	defer tx.Rollback()

	// Define tables to delete from in order
	// Order matters: child tables before parent tables
	deletions := []struct {
		table string
		query string
	}{
		{"scandata", `DELETE FROM scandata WHERE scan_id = $1`},
		{"messagemetadata", `DELETE FROM messagemetadata WHERE scan_id = $1`},
		{"scanmetadata", `DELETE FROM scanmetadata WHERE scan_id = $1`},
		{"photometadata", `DELETE FROM photometadata 
			WHERE photos_media_item_id IN (
				SELECT id FROM photosmediaitem WHERE scan_id = $1
			)`},
		{"videometadata", `DELETE FROM videometadata 
			WHERE photos_media_item_id IN (
				SELECT id FROM photosmediaitem WHERE scan_id = $1
			)`},
		{"photosmediaitem", `DELETE FROM photosmediaitem WHERE scan_id = $1`},
		{"scans", `DELETE FROM scans WHERE id = $1`},
	}

	// Execute all deletions within transaction
	for _, deletion := range deletions {
		result, err := tx.Exec(deletion.query, scanId)
		if err != nil {
			// Transaction automatically rolled back by defer
			return fmt.Errorf("failed to delete from %s: %w", deletion.table, err)
		}

		// Log number of rows deleted for debugging
		rowsAffected, _ := result.RowsAffected()
		slog.Debug("Deleted rows",
			"table", deletion.table,
			"rows", rowsAffected,
			"scan_id", scanId)
	}

	// Commit transaction - all deletes succeed together
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	slog.Info("Successfully deleted scan", "scan_id", scanId)
	return nil
}

// MarkScanCompleted marks a scan as completed
func MarkScanCompleted(scanId int) error {
	update_row := `update scans
								 set scan_end_time = current_timestamp, status = 'Completed'
								 where id = $1`
	res, err := db.Exec(update_row, scanId)
	if err != nil {
		return fmt.Errorf("failed to mark scan %d as completed: %w", scanId, err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for scan %d: %w", scanId, err)
	}
	if count != 1 {
		slog.Warn("Unexpected rows affected when marking scan complete",
			"scan_id", scanId,
			"expected", 1,
			"actual", count)
	}
	slog.Info("Scan marked as completed", "scan_id", scanId)
	return nil
}

// MarkScanFailed marks a scan as failed with an error message
func MarkScanFailed(scanId int, errMsg string) error {
	update_row := `update scans
								 set scan_end_time = current_timestamp, status = 'Failed', error_msg = $2
								 where id = $1`
	res, err := db.Exec(update_row, scanId, errMsg)
	if err != nil {
		return fmt.Errorf("failed to mark scan %d as failed: %w", scanId, err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for scan %d: %w", scanId, err)
	}
	if count != 1 {
		slog.Warn("Unexpected rows affected when marking scan failed",
			"scan_id", scanId,
			"expected", 1,
			"actual", count)
	}
	slog.Error("Scan marked as failed", "scan_id", scanId, "error", errMsg)
	return nil
}

// GetScanById retrieves a scan by ID
func GetScanById(scanId int) (*Scan, error) {
	read_row := `select id, scan_type, COALESCE(status, 'Completed') as status,
		error_msg, completed_at FROM scans WHERE id = $1`

	var scan Scan
	err := db.Get(&scan, read_row, scanId)
	if err != nil {
		return nil, fmt.Errorf("failed to get scan %d: %w", scanId, err)
	}

	return &scan, nil
}

func migrateDB() error {
	var count int
	has_table_query := `select count(*)
		from information_schema.tables
		where table_name = $1`
	err := db.Get(&count, has_table_query, "version")
	if err != nil {
		return fmt.Errorf("failed to check for version table: %w", err)
	}
	if count == 0 {
		return migrateDBv0()
	}

	// Add migration for status column if needed
	return migrateAddStatusColumn()
}

func migrateDBv0() error {
	insert_version_table := `delete from version;
		INSERT INTO version (id) VALUES (4)`

	// Execute all table creation statements
	statements := []struct {
		name string
		sql  string
	}{
		{"scans", create_scans_table},
		{"scandata", create_scandata_table},
		{"scanmetadata", create_scanmetadata_table},
		{"messagemetadata", create_messagemetadata_table},
		{"photosmediaitem", create_photosmediaitem_table},
		{"photometadata", create_photometadata_table},
		{"videometadata", create_videometadata_table},
		{"privatetokens", create_privatetokens_table},
		{"version", create_version_table},
	}

	for _, stmt := range statements {
		_, err := db.Exec(stmt.sql)
		if err != nil {
			return fmt.Errorf("failed to create table %s: %w", stmt.name, err)
		}
		slog.Info("Created table", "table", stmt.name)
	}

	_, err := db.Exec(insert_version_table)
	if err != nil {
		return fmt.Errorf("failed to insert version: %w", err)
	}

	// Add status columns to scans table
	return migrateAddStatusColumn()
}

// migrateAddStatusColumn adds status, error_msg, and completed_at columns to scans table
func migrateAddStatusColumn() error {
	// Check if status column exists
	check_column := `SELECT column_name FROM information_schema.columns
		WHERE table_name='scans' AND column_name='status'`
	var columnName string
	err := db.Get(&columnName, check_column)

	// If column doesn't exist (error means no rows), add it
	if err != nil {
		alter_table := `ALTER TABLE scans
			ADD COLUMN status VARCHAR(50) DEFAULT 'Completed',
			ADD COLUMN error_msg TEXT,
			ADD COLUMN completed_at TIMESTAMP`

		_, err = db.Exec(alter_table)
		if err != nil {
			return fmt.Errorf("failed to add status columns to scans table: %w", err)
		}
		slog.Info("Added status, error_msg, and completed_at columns to scans table")
	}

	return nil
}

const create_scans_table string = `CREATE TABLE IF NOT EXISTS scans (
		  id serial PRIMARY KEY,
		  scan_type VARCHAR (50) NOT NULL,
		  created_on TIMESTAMP NOT NULL,
		  scan_start_time TIMESTAMP NOT NULL,
		  scan_end_time TIMESTAMP
		)`

const create_scandata_table string = `CREATE TABLE IF NOT EXISTS scandata (
		  id serial PRIMARY KEY,
		  name VARCHAR(200),
		  path VARCHAR(2000),
		  size BIGINT,
		  file_mod_time TIMESTAMP,
		  md5hash VARCHAR(60),
		  is_dir boolean,
		  file_count INT,
		  scan_id INT NOT NULL,
		  FOREIGN KEY (scan_id)
			  REFERENCES Scans (id)
		)`

const create_version_table string = `CREATE TABLE IF NOT EXISTS version (
		  id INT PRIMARY KEY
		)`

const create_scanmetadata_table string = `CREATE TABLE IF NOT EXISTS scanmetadata (
	id serial PRIMARY KEY,
	name VARCHAR(200),
	search_path VARCHAR(2000),
	search_filter VARCHAR(2000),
	scan_id INT NOT NULL,
	FOREIGN KEY (scan_id)
		REFERENCES Scans (id)
)`

const create_messagemetadata_table string = `CREATE TABLE IF NOT EXISTS messagemetadata (
	id serial PRIMARY KEY,
	message_id VARCHAR(200),
	thread_id VARCHAR(200),
	username  VARCHAR(200),
	date TIMESTAMP,
	mail_from VARCHAR(500),
	mail_to VARCHAR(500),
	subject VARCHAR(2000),
	size_estimate BIGINT,
	labels VARCHAR(500),
	scan_id INT NOT NULL,
	FOREIGN KEY (scan_id)
		REFERENCES Scans (id)
)`

const create_photosmediaitem_table string = `CREATE TABLE IF NOT EXISTS photosmediaitem (
	id serial PRIMARY KEY NOT NULL,
	media_item_id TEXT NOT NULL,
	product_url  TEXT NOT NULL,
	mime_type  TEXT,
	filename TEXT NOT NULL,
	size BIGINT,
	file_mod_time TIMESTAMP,
	md5hash TEXT,
	scan_id INT NOT NULL,
	contributor_display_name TEXT,
	FOREIGN KEY (scan_id)
		REFERENCES Scans (id)
)`

const create_photometadata_table string = `CREATE TABLE IF NOT EXISTS photometadata (
	id serial PRIMARY KEY NOT NULL,
	photos_media_item_id INT NOT NULL,
	camera_make VARCHAR(500),
	camera_model VARCHAR(500),
  focal_length numeric,
  f_number numeric,
  iso INT,
  exposure_time VARCHAR(500),
	FOREIGN KEY (photos_media_item_id)
		REFERENCES photosmediaitem (id)
)`

const create_videometadata_table string = `CREATE TABLE IF NOT EXISTS videometadata (
	id serial PRIMARY KEY NOT NULL,
	photos_media_item_id INT NOT NULL,
	camera_make VARCHAR(500),
	camera_model VARCHAR(500),
  fps numeric,
	FOREIGN KEY (photos_media_item_id)
		REFERENCES photosmediaitem (id)
)`

const create_privatetokens_table string = `CREATE TABLE IF NOT EXISTS privatetokens (
	id serial PRIMARY KEY NOT NULL,
	access_token VARCHAR(800),
	refresh_token VARCHAR(800),
	display_name VARCHAR(100),
	client_key VARCHAR(100) NOT NULL UNIQUE,
	created_on TIMESTAMP NOT NULL,
	scope VARCHAR(500), 
	expires_in INT, 
	token_type VARCHAR(100)
)`

type PrivateToken struct {
	Id           int       `db:"id" json:"scan_id"`
	AccessToken  string    `db:"access_token"`
	RefreshToken string    `db:"refresh_token"`
	Client_key   string    `db:"client_key"`
	CreatedOn    time.Time `db:"created_on"`
	DisplayName  string    `db:"display_name"`
	Scope        string    `db:"scope"`
	ExpiresIn    int       `db:"expires_in"`
	TokenType    string    `db:"token_type"`
}

type Scan struct {
	Id            int          `db:"id" json:"scan_id"`
	ScanType      string       `db:"scan_type"`
	CreatedOn     time.Time    `db:"created_on"`
	ScanStartTime time.Time    `db:"scan_start_time"`
	ScanEndTime   sql.NullTime `db:"scan_end_time"`
	Metadata      string       `db:"metadata"`
	Duration      string       `db:"duration"`
	Status        string       `db:"status"`
	ErrorMsg      sql.NullString `db:"error_msg"`
	CompletedAt   sql.NullTime `db:"completed_at"`
}

type ScanRequests struct {
	Id                int       `db:"id" json:"scan_id"`
	Name              string    `db:"name" json:"name"`
	ScanType          string    `db:"scan_type" json:"scan_type"`
	SearchFilter      string    `db:"search_filter" json:"search_filter"`
	ScanStartTime     time.Time `db:"scan_start_time" json:"scan_start_time"`
	ScanDurationInSec string    `db:"scan_duration_in_sec" json:"scan_duration_in_sec"`
}

type ScanData struct {
	Id           int            `db:"id" json:"scan_data_id"`
	Name         sql.NullString `db:"name"`
	Path         sql.NullString `db:"path"`
	Size         sql.NullInt64  `db:"size"`
	ModifiedTime sql.NullTime   `db:"file_mod_time"`
	Md5Hash      sql.NullString `db:"md5hash"`
	IsDir        sql.NullBool   `db:"is_dir"`
	FileCount    sql.NullInt32  `db:"file_count"`
	ScanId       int            `db:"scan_id"`
}

type MessageMetadataRead struct {
	Id           int            `db:"id" json:"message_metadata_id"`
	ScanId       int            `db:"scan_id"`
	MessageId    sql.NullString `db:"message_id"`
	ThreadId     sql.NullString `db:"thread_id"`
	LabelIds     sql.NullString `db:"labels"`
	From         sql.NullString `db:"mail_from"`
	To           sql.NullString `db:"mail_to"`
	Subject      sql.NullString
	Date         sql.NullString
	SizeEstimate sql.NullInt64 `db:"size_estimate"`
}

type PhotosMediaItemRead struct {
	Id                     int            `db:"id" json:"photos_media_item_id"`
	ScanId                 int            `db:"scan_id"`
	MediaItemId            string         `db:"media_item_id" json:"media_item_id"`
	ProductUrl             string         `db:"product_url"`
	MimeType               sql.NullString `db:"mime_type"`
	Filename               string
	Size                   sql.NullInt64
	ModifiedTime           sql.NullTime `db:"file_mod_time"`
	Md5hash                sql.NullString
	ContributorDisplayName sql.NullString `db:"contributor_display_name"`
}

type Account struct {
	ClientKey   string `db:"client_key" json:"clientKey"`
	DisplayName string `db:"display_name" json:"displayName"`
}

func substr(s string, end int) string {
	if len(s) < end {
		return s
	}
	counter := 0
	for i := range s {
		if counter == end {
			return s[0:i]
		}
		counter++
	}
	return s
}
