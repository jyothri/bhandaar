package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/jyothri/hdd/notification"
	"github.com/lib/pq"
)

// DBConfig holds database configuration parameters
type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

var db *sqlx.DB

// getEnv retrieves an environment variable or returns a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvInt retrieves an environment variable as an integer or returns a default value
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
		slog.Warn("Invalid integer value for environment variable, using default",
			"key", key, "value", value, "default", defaultValue)
	}
	return defaultValue
}

// getDBConfig loads database configuration from environment variables
func getDBConfig() DBConfig {
	return DBConfig{
		Host:     getEnv("DB_HOST", "hdd_db"),
		Port:     getEnvInt("DB_PORT", 5432),
		User:     getEnv("DB_USER", "hddb"),
		Password: getEnv("DB_PASSWORD", ""),
		DBName:   getEnv("DB_NAME", "hdd_db"),
		SSLMode:  getEnv("DB_SSL_MODE", "disable"),
	}
}

// SetupDatabase initializes the database connection and runs migrations
func SetupDatabase() error {
	config := getDBConfig()

	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.User, config.Password, config.DBName, config.SSLMode)

	slog.Info("Connecting to database",
		"host", config.Host,
		"port", config.Port,
		"user", config.User,
		"dbname", config.DBName,
		"sslmode", config.SSLMode,
	)

	var err error
	db, err = sqlx.Open("postgres", connStr)
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

	if err := markInterruptedScans(); err != nil {
		return fmt.Errorf("failed to mark interrupted scans: %w", err)
	}

	return nil
}

// markInterruptedScans marks scans that are still open at startup as failed.
// Scans run inside this process, so a scan without an end time when the
// server starts was cut off by a crash or restart and will never finish.
// Its end time stays null, since when it stopped isn't known.
func markInterruptedScans() error {
	update_rows := `update scans
		set status = 'Failed',
			error_msg = 'Interrupted: the server stopped before the scan finished'
		where scan_end_time is null and status is distinct from 'Failed'`
	res, err := db.Exec(update_rows)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count > 0 {
		slog.Warn("Marked interrupted scans as failed", "count", count)
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

// LogStartScan records a new running scan owned by userID.
func LogStartScan(scanType string, userID int64) (int, error) {
	insert_row := `insert into scans
									(scan_type, created_on, scan_start_time, status, user_id)
								values
									($1, current_timestamp, current_timestamp, 'Running', $2) RETURNING id`
	lastInsertId := 0
	err := db.QueryRow(insert_row, scanType, userID).Scan(&lastInsertId)
	if err != nil {
		return 0, fmt.Errorf("failed to insert scan for type %s: %w", scanType, err)
	}
	return lastInsertId, nil
}

// SaveScanMetadata records what a scan covers. A Google scan's name and
// clientKey are its linked account's; others leave both empty (clientKey is
// then stored as NULL).
func SaveScanMetadata(name string, clientKey string, searchPath string, searchFilter string, scanId int) error {
	insert_row := `insert into scanmetadata
			(name, client_key, search_path, search_filter, scan_id)
		values
			($1, NULLIF($2, ''), $3, $4, $5) RETURNING id`
	_, err := db.Exec(insert_row, name, clientKey, searchPath, searchFilter, scanId)
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
	for fd := range scanData {
		if !fd.RecordOnly {
			saveStat(scanId, fd)
		}
	}
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
}

// saveStat saves one row of a scan's files and folders.
func saveStat(scanId int, fd FileData) {
	insert_row := `insert into scandata
			(name, path, size, file_mod_time, md5hash, scan_id, is_dir, file_count, file_id)
		values
			($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($9, '')) RETURNING id`
	var err error
	if fd.IsDir {
		_, err = db.Exec(insert_row, fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.Md5Hash, scanId, fd.IsDir, fd.FileCount, fd.FileId)
	} else {
		_, err = db.Exec(insert_row, fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.Md5Hash, scanId, fd.IsDir, nil, fd.FileId)
	}

	if err != nil {
		slog.Error("Failed to save file scan data, skipping",
			"scan_id", scanId,
			"path", fd.FilePath,
			"is_dir", fd.IsDir,
			"size_bytes", fd.Size,
			"error", err)
	}
}

// GetOAuthToken returns a Google account userID linked. Another user's
// account is not found.
func GetOAuthToken(userID int64, clientKey string) (PrivateToken, error) {
	read_row :=
		`select id, access_token, refresh_token, COALESCE(display_name, '') AS display_name, client_key, created_on,
			COALESCE(scope, '') AS scope, COALESCE(expires_in, 0) AS expires_in, COALESCE(token_type, '') AS token_type
		FROM privatetokens
		WHERE client_key = $1 AND user_id = $2`
	tokenData := PrivateToken{}
	err := db.Get(&tokenData, read_row, clientKey, userID)
	if err != nil {
		return PrivateToken{}, fmt.Errorf("failed to get OAuth token for client %s: %w", clientKey, err)
	}
	return tokenData, nil
}

func GetScansFromDb(userID int64, pageNo int) ([]Scan, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)
	count_rows := `select count(*) from scans where user_id = $1`
	read_row :=
		`select S.id, scan_type,
		 created_on,
		 scan_start_time,
		 scan_end_time, CONCAT(search_path, search_filter) as metadata,
		 date_trunc('millisecond', COALESCE(scan_end_time,current_timestamp)-scan_start_time) as duration
	   from scans S LEFT JOIN scanmetadata SM
		 ON S.id = SM.scan_id
		 where S.user_id = $3
		 order by id limit $1 OFFSET $2
		`
	scans := []Scan{}
	var count int
	err := db.Select(&scans, read_row, limit, offset, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scans for page %d: %w", pageNo, err)
	}
	err = db.Get(&count, count_rows, userID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scan count: %w", err)
	}
	return scans, count, nil
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
	notification.PublishScanEnd(scanId, notification.StatusCompleted, "")
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
	notification.PublishScanEnd(scanId, notification.StatusFailed, errMsg)
	return nil
}

// GetScanById retrieves a scan by ID
func GetScanById(scanId int) (*Scan, error) {
	read_row := `select id, scan_type, COALESCE(status, 'Completed') as status,
		error_msg FROM scans WHERE id = $1`

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
		if err := migrateDBv0(); err != nil {
			return err
		}
	} else if err := migrateAddStatusColumn(); err != nil {
		// Add migration for status column if needed
		return err
	}

	if err := migrateDropCompletedAt(); err != nil {
		return err
	}
	if err := migrateScanTimesToTimestamptz(); err != nil {
		return err
	}
	// A cloud file's ID; its path is then the folder path it was found at.
	if _, err := db.Exec(`ALTER TABLE scandata ADD COLUMN IF NOT EXISTS file_id VARCHAR(200)`); err != nil {
		return fmt.Errorf("failed to add scandata.file_id: %w", err)
	}
	if err := migrateUsers(); err != nil {
		return err
	}
	if err := migrateAccounts(); err != nil {
		return err
	}
	if err := migrateDriveItems(); err != nil {
		return err
	}
	return migrateBrowseTotals()
}

// migrateDropCompletedAt drops scans.completed_at. Nothing ever set it, so
// every value was null, and scan_end_time already records when a scan
// finished or failed.
func migrateDropCompletedAt() error {
	if _, err := db.Exec(`ALTER TABLE scans DROP COLUMN IF EXISTS completed_at`); err != nil {
		return fmt.Errorf("failed to drop scans.completed_at: %w", err)
	}
	return nil
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

// migrateAddStatusColumn adds status and error_msg columns to scans table
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
			ADD COLUMN error_msg TEXT`

		_, err = db.Exec(alter_table)
		if err != nil {
			return fmt.Errorf("failed to add status columns to scans table: %w", err)
		}
		slog.Info("Added status and error_msg columns to scans table")
	}

	return nil
}

// migrateScanTimesToTimestamptz converts the scans table's time columns from
// timestamp to timestamptz. They are filled with current_timestamp, so as
// timestamp they held wall-clock time in the database session's time zone,
// and reading them as instants depended on that zone. The conversion reads
// the existing values as UTC, the default of the Postgres image this app
// runs with. As timestamptz they are instants, whatever the server's zone.
func migrateScanTimesToTimestamptz() error {
	var columns []string
	find_columns := `SELECT column_name FROM information_schema.columns
		WHERE table_name = 'scans' AND data_type = 'timestamp without time zone'
		ORDER BY ordinal_position`
	if err := db.Select(&columns, find_columns); err != nil {
		return fmt.Errorf("failed to find timestamp columns in scans table: %w", err)
	}
	if len(columns) == 0 {
		return nil
	}

	alters := make([]string, len(columns))
	for i, column := range columns {
		quoted := pq.QuoteIdentifier(column)
		alters[i] = fmt.Sprintf("ALTER COLUMN %s TYPE timestamptz USING %s AT TIME ZONE 'UTC'", quoted, quoted)
	}
	if _, err := db.Exec("ALTER TABLE scans " + strings.Join(alters, ", ")); err != nil {
		return fmt.Errorf("failed to convert scans time columns to timestamptz: %w", err)
	}
	slog.Info("Converted scans time columns to timestamptz", "columns", columns)
	return nil
}

const create_scans_table string = `CREATE TABLE IF NOT EXISTS scans (
		  id serial PRIMARY KEY,
		  scan_type VARCHAR (50) NOT NULL,
		  created_on TIMESTAMPTZ NOT NULL,
		  scan_start_time TIMESTAMPTZ NOT NULL,
		  scan_end_time TIMESTAMPTZ
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
}

type ScanRequests struct {
	Id                int       `db:"id" json:"scan_id"`
	Name              string    `db:"name" json:"name"`
	ScanType          string    `db:"scan_type" json:"scan_type"`
	SearchFilter      string    `db:"search_filter" json:"search_filter"`
	// A Drive folder scan's folder; empty otherwise.
	SearchPath        string    `db:"search_path" json:"search_path"`
	ScanStartTime     time.Time `db:"scan_start_time" json:"scan_start_time"`
	ScanDurationInSec string    `db:"scan_duration_in_sec" json:"scan_duration_in_sec"`
	Status            string    `db:"status" json:"status"`
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

// Account is a linked Google account, as GET /api/accounts lists it.
type Account struct {
	ClientKey   string   `json:"clientKey"`
	DisplayName string   `json:"displayName"`
	Services    []string `json:"services"`
	// The Google account ID, for Google's login_hint; empty for an account
	// linked before it was recorded.
	LoginHint string `json:"loginHint,omitempty"`
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
