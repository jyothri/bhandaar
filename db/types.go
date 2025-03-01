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
	Date         time.Time
	SizeEstimate int64
}

type PhotosMediaItem struct {
	MediaItemId            string
	ProductUrl             string
	MimeType               string
	Filename               string
	Size                   int64
	FileModTime            time.Time
	Md5hash                string
	ContributorDisplayName string
	AlbumIds               []string
	CameraMake             string
	CameraModel            string
	FocalLength            float32
	FNumber                float32
	Iso                    int
	ExposureTime           string
	Fps                    float32
}
