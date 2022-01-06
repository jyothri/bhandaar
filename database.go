package main

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "postgres"
	dbname   = "postgres"
)

var db *sqlx.DB
var initialized bool

func init() {
	initialized = false
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)
	var err error
	db, err = sqlx.Open("postgres", psqlInfo)
	if err != nil {
		return
	}
	err = db.Ping()
	if err != nil {
		return
	}
	fmt.Println("Successfully connected to DB!")
	initialized = true
}

func logStartScan(scanType string) int {
	if !initialized {
		return -1
	}
	insert_row := `insert into scans 
									(scan_type, created_on, scan_start_time) 
								values 
									($1, current_timestamp, current_timestamp) RETURNING id`
	lastInsertId := 0
	err := db.QueryRow(insert_row, scanType).Scan(&lastInsertId)
	checkError(err)
	return lastInsertId
}

func saveStatsToDb(scanId int, info *[]fileData) {
	if !initialized {
		return
	}
	insert_row := `insert into ScanData 
                           (name, path, size, file_mod_time, md5hash, scan_id, is_dir, file_count) 
                        values 
                           ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`
	for _, fd := range *info {
		var err error
		if fd.IsDir {
			_, err = db.Exec(insert_row, fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.Md5Hash, scanId, fd.IsDir, fd.FileCount)
		} else {
			_, err = db.Exec(insert_row, fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.Md5Hash, scanId, fd.IsDir, nil)
		}
		checkError(err)
	}
}

func getScansFromDb(pageNo int) ([]Scan, int) {
	if !initialized {
		return make([]Scan, 0), 0
	}
	limit := 10
	offset := limit * (pageNo - 1)
	count_rows := `select count(*) from scans`
	read_row := `select * from scans order by id limit $1 OFFSET $2`
	scans := []Scan{}
	var count int
	err := db.Select(&scans, read_row, limit, offset)
	checkError(err)
	err = db.Get(&count, count_rows)
	checkError(err)
	return scans, count
}

func getScanDataFromDb(scanId int, pageNo int) ([]ScanData, int) {
	if !initialized {
		return make([]ScanData, 0), 0
	}
	limit := 10
	offset := limit * (pageNo - 1)
	count_rows := `select count(*) from scandata where scan_id = $1`
	read_row := `select * from scandata where scan_id = $1 order by id limit $2 offset $3`
	scandata := []ScanData{}
	var count int
	err := db.Select(&scandata, read_row, scanId, limit, offset)
	checkError(err)
	err = db.Get(&count, count_rows, scanId)
	checkError(err)
	return scandata, count
}

func logCompleteScan(scanId int) {
	if !initialized {
		return
	}
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

type Scan struct {
	Id            int          `db:"id" json:"scan_id"`
	ScanType      string       `db:"scan_type"`
	CreatedOn     time.Time    `db:"created_on"`
	ScanStartTime time.Time    `db:"scan_start_time"`
	ScanEndTime   sql.NullTime `db:"scan_end_time"`
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
