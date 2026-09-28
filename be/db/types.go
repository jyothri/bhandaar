package db

import (
	"time"
)

type FileData struct {
	FilePath string
	// The file's ID where it lives, for cloud files (a Drive file ID);
	// empty for local ones.
	FileId    string
	FileName  string
	IsDir     bool
	Size      uint
	ModTime   time.Time
	FileCount uint
	Md5Hash   string
	// For a Drive scan of a linked account, the item to update in the
	// account's record (see SaveDriveScanToDb). RecordOnly rows update
	// only the record: they get no row in the scan's results.
	Drive      *DriveItem
	RecordOnly bool
}

type MessageMetadata struct {
	MessageId    string
	ThreadId     string
	LabelIds     []string
	From         string
	To           string
	Subject      string
	Date         time.Time
	SizeEstimate int64
}
