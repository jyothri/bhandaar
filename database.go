package main

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres"
	password = "postgres"
	dbname   = "postgres"
)

var db *sql.DB
var initialized bool

func init() {
	initialized = false
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)
	var err error
	db, err = sql.Open("postgres", psqlInfo)
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
