package db

import (
	"time"
)

type FileData struct {
	FilePath  string
	FileName  string
	IsDir     bool
	Size      uint
	ModTime   time.Time
	FileCount uint
	Md5Hash   string
}
