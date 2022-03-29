package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const (
	host     = "postgres"
	port     = 5432
	user     = "postgres"
	password = "postgres"
	dbname   = "postgres"
)

var db *sqlx.DB

func init() {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)
	var err error
	db, err = sqlx.Open("postgres", psqlInfo)
	checkError(err)
	err = db.Ping()
	checkError(err)
	fmt.Println("Successfully connected to DB!")
	migrateDB()
}

func LogStartScan(scanType string) int {
	insert_row := `insert into scans 
									(scan_type, created_on, scan_start_time) 
								values 
									($1, current_timestamp, current_timestamp) RETURNING id`
	lastInsertId := 0
	err := db.QueryRow(insert_row, scanType).Scan(&lastInsertId)
	checkError(err)
	return lastInsertId
}

func SaveScanMetadata(searchPath string, searchFilter string, scanId int) {
	insert_row := `insert into scanmetadata 
			(name, search_path, search_filter, scan_id) 
		values 
			($1, $2, $3, $4) RETURNING id`
	var err error
	_, err = db.Exec(insert_row, nil, searchPath, searchFilter, scanId)
	checkError(err)
}

func SaveMessageMetadataToDb(scanId int, messageMetaData <-chan MessageMetadata) {
	for {
		mmd, more := <-messageMetaData
		if !more {
			logCompleteScan(scanId)
			break
		}
		insert_row := `insert into messagemetadata 
			(message_id, thread_id, date, mail_from, mail_to, subject, size_estimate, labels, scan_id) 
		values 
			($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`
		var err error
		_, err = db.Exec(insert_row, mmd.MessageId, mmd.ThreadId, mmd.Date, substr(mmd.From, 500),
			substr(mmd.To, 500), substr(mmd.Subject, 2000), mmd.SizeEstimate,
			substr(strings.Join(mmd.LabelIds, ","), 500), scanId)
		checkError(err, fmt.Sprintf("While inserting to messagemetadata messageId:%v", mmd.MessageId))
	}
}

func SavePhotosMediaItemToDb(scanId int, photosMediaItem <-chan PhotosMediaItem) {
	for {
		pmi, more := <-photosMediaItem
		if !more {
			logCompleteScan(scanId)
			break
		}
		insert_row := `insert into photosmediaitem 
			(media_item_id, product_url, mime_type, filename, size, scan_id, file_mod_time, 
				contributor_display_name) 
		values 
			($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
		var err error
		lastInsertId := 0
		err = db.QueryRow(insert_row, pmi.MediaItemId, pmi.ProductUrl, pmi.MimeType, pmi.Filename,
			pmi.Size, scanId, pmi.FileModTime, pmi.ContributorDisplayName).Scan(&lastInsertId)
		checkError(err, fmt.Sprintf("While inserting to photosmediaitem mediaItemId:%v", pmi.MediaItemId))

		switch pmi.MimeType[:5] {
		case "image":
			//e.g. image/jpeg image/png image/gif
			insert_photo_row := `insert into photometadata 
			(photos_media_item_id, camera_make, camera_model, focal_length, f_number, iso, exposure_time) 
		values 
			($1, $2, $3, $4, $5, $6, $7) RETURNING id`
			_, err = db.Exec(insert_photo_row, lastInsertId, pmi.CameraMake, pmi.CameraModel, pmi.FocalLength,
				pmi.FNumber, pmi.Iso, pmi.ExposureTime)
			checkError(err, fmt.Sprintf("While inserting to photometadata mediaItemId:%v", pmi.MediaItemId))
		case "video":
			//e.g. video/mp4
			insert_video_row := `insert into videometadata 
			(photos_media_item_id, camera_make, camera_model, fps) 
		values 
			($1, $2, $3, $4) RETURNING id`
			_, err = db.Exec(insert_video_row, lastInsertId, pmi.CameraMake, pmi.CameraModel, pmi.Fps)
			checkError(err, fmt.Sprintf("While inserting to videometadata mediaItemId:%v", pmi.MediaItemId))
		default:
			fmt.Println("Unsupported mime type.")
		}
	}
}

func SaveStatToDb(scanId int, scanData <-chan FileData) {
	for {
		fd, more := <-scanData
		if !more {
			logCompleteScan(scanId)
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
		checkError(err)
	}
}

func GetScansFromDb(pageNo int) ([]Scan, int) {
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
	checkError(err)
	err = db.Get(&count, count_rows)
	checkError(err)
	return scans, count
}

func GetMessageMetadataFromDb(scanId int, pageNo int) ([]MessageMetadataRead, int) {
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
	checkError(err)
	err = db.Select(&messageMetadata, read_row, scanId, limit, offset)
	checkError(err)
	return messageMetadata, count
}

func GetScanDataFromDb(scanId int, pageNo int) ([]ScanData, int) {
	limit := 10
	offset := limit * (pageNo - 1)
	count_rows := `select count(*) from scandata where scan_id = $1`
	read_row := `select * from scandata where scan_id = $1 order by id limit $2 offset $3`
	scandata := []ScanData{}
	var count int
	err := db.Get(&count, count_rows, scanId)
	checkError(err)
	err = db.Select(&scandata, read_row, scanId, limit, offset)
	checkError(err)
	return scandata, count
}

func DeleteScan(scanId int) {
	delete_scandata := `delete from scandata
	where scan_id = $1`
	_, err := db.Exec(delete_scandata, scanId)
	checkError(err)

	delete_messagemetadata := `delete from messagemetadata
	where scan_id = $1`
	_, err = db.Exec(delete_messagemetadata, scanId)
	checkError(err)

	delete_scanmetadata := `delete from scanmetadata
	where scan_id = $1`
	_, err = db.Exec(delete_scanmetadata, scanId)
	checkError(err)

	delete_photometadata := `delete from photometadata
	where photos_media_item_id IN (select id from 
		photosmediaitem where scan_id = $1)`
	_, err = db.Exec(delete_photometadata, scanId)
	checkError(err)

	delete_videometadata := `delete from videometadata
	where photos_media_item_id IN (select id from 
		photosmediaitem where scan_id = $1)`
	_, err = db.Exec(delete_videometadata, scanId)
	checkError(err)

	delete_photosmediaitem := `delete from photosmediaitem
	where scan_id = $1`
	_, err = db.Exec(delete_photosmediaitem, scanId)
	checkError(err)

	delete_scans := `delete from scans
	where id = $1`
	_, err = db.Exec(delete_scans, scanId)
	checkError(err)
}

func logCompleteScan(scanId int) {
	update_row := `update scans 
								 set scan_end_time = current_timestamp 
								 where id = $1`
	res, err := db.Exec(update_row, scanId)
	checkError(err)
	count, err := res.RowsAffected()
	checkError(err)
	if count != 1 {
		fmt.Printf("Could not perform update. query=%s, expected:%d actual: %d", update_row, 1, count)
	}
}

func migrateDB() {
	var count int
	var version int
	has_table_query := `select count(*) 
		from information_schema.tables 
		where table_name = $1`
	err := db.Get(&count, has_table_query, "version")
	checkError(err)
	if count == 0 {
		migrateDBv0()
		return
	}
	select_version_table := `select COALESCE(MAX(id),0) from version`
	err = db.Get(&version, select_version_table)
	checkError(err)

	if version < 2 {
		migrateDBv1To2()
	}
	if version < 3 {
		migrateDBv2To3()
	}
	if version < 4 {
		migrateDBv3To4()
	}
}

func migrateDBv0() {
	create_scans_table := `CREATE TABLE scans (
		  id serial PRIMARY KEY,
		  scan_type VARCHAR (50) NOT NULL,
		  created_on TIMESTAMP NOT NULL,
		  scan_start_time TIMESTAMP NOT NULL,
		  scan_end_time TIMESTAMP
		)`
	create_scandata_table := `CREATE TABLE IF NOT EXISTS scandata (
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
	create_version_table := `CREATE TABLE IF NOT EXISTS version (
		  id INT PRIMARY KEY
		)`

	insert_version_table := `delete from version; 
		INSERT INTO version (id) VALUES (2)`
	db.MustExec(create_scans_table)
	db.MustExec(create_scandata_table)
	db.MustExec(create_scanmetadata_table)
	db.MustExec(create_version_table)
	db.MustExec(insert_version_table)
}

func migrateDBv1To2() {
	insert_version_table := `delete from version; 
		INSERT INTO version (id) VALUES (2)`
	db.MustExec(create_scanmetadata_table)
	db.MustExec(insert_version_table)
}

func migrateDBv2To3() {
	insert_version_table := `delete from version; 
		INSERT INTO version (id) VALUES (3)`
	db.MustExec(create_messagemetadata_table)
	db.MustExec(insert_version_table)
}

func migrateDBv3To4() {
	insert_version_table := `delete from version; 
		INSERT INTO version (id) VALUES (4)`
	db.MustExec(create_photosmediaitem_table)
	db.MustExec(create_photometadata_table)
	db.MustExec(create_videometadata_table)
	db.MustExec(insert_version_table)
}

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
	date VARCHAR(200),
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

type Scan struct {
	Id            int          `db:"id" json:"scan_id"`
	ScanType      string       `db:"scan_type"`
	CreatedOn     time.Time    `db:"created_on"`
	ScanStartTime time.Time    `db:"scan_start_time"`
	ScanEndTime   sql.NullTime `db:"scan_end_time"`
	Metadata      string       `db:"metadata"`
	Duration      string       `db:"duration"`
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

func checkError(err error, msg ...string) {
	if err != nil {
		fmt.Println(msg)
		panic(err)
	}
}
