package main

import (
	"time"
)

type fileData struct {
	FilePath  string
	IsDir     bool
	Size      uint
	ModTime   time.Time
	FileCount uint
	Md5Hash   string
}
