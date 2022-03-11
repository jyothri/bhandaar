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

type MessageMetadata struct {
	MessageId    string
	ThreadId     string
	LabelIds     []string
	From         string
	To           string
	Subject      string
	Date         string
	SizeEstimate int64
}
