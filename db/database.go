package db

import (
	"database/sql"
	"fmt"
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

func SaveStatsToDb(scanId int, info *[]FileData) {
	insert_row := `insert into scandata 
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

func GetScansFromDb(pageNo int) ([]Scan, int) {
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

func LogCompleteScan(scanId int) {
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

func LoadStatsFromFile(saveFile string) *[]FileData {
	return loadStatsFromFile(saveFile)
}

func migrateDB() {
	table_row_count := `select count(*) 
											from information_schema.tables 
											where table_name = $1`
	row_count := 0
	err := db.QueryRow(table_row_count, "scans").Scan(&row_count)
	checkError(err)

	if row_count == 0 {
		fmt.Println("Creating scans table")
		create_scans_table := `CREATE TABLE scans (
														id serial PRIMARY KEY,
														scan_type VARCHAR (50) NOT NULL,
														created_on TIMESTAMP NOT NULL,
														scan_start_time TIMESTAMP NOT NULL,
														scan_end_time TIMESTAMP
													)`
		db.MustExec(create_scans_table)
	}

	err = db.QueryRow(table_row_count, "scandata").Scan(&row_count)
	checkError(err)

	if row_count == 0 {
		fmt.Println("Creating scandata table")
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
		db.MustExec(create_scandata_table)
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

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}
