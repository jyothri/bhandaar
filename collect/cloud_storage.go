package collect

import (
	"context"
	"fmt"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/jyothri/hdd/db"
	"google.golang.org/api/iterator"
)

func CloudStorage(gStorageScan GStorageScan) int {
	scanData := make(chan db.FileData, 10)
	scanId := db.LogStartScan("google_storage")
	go db.SaveScanMetadata("", "bucket="+gStorageScan.Bucket, "", scanId)
	go startCloudStorage(scanId, gStorageScan.Bucket, scanData)
	go db.SaveStatToDb(scanId, scanData)
	return scanId
}

func startCloudStorage(scanId int, bucketName string, scanData chan<- db.FileData) {
	lock.Lock()
	defer lock.Unlock()
	ctx := context.Background()

	// Create a client.
	client, err := storage.NewClient(ctx)
	checkError(err)
	defer client.Close()

	// Create a Bucket instance.
	bucket := client.Bucket(bucketName)

	query := &storage.Query{Prefix: ""}

	it := bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		checkError(err)
		fd := db.FileData{
			FilePath:  attrs.MediaLink,
			IsDir:     false,
			ModTime:   attrs.Updated,
			FileCount: 1,
			Size:      uint(attrs.Size),
			Md5Hash:   fmt.Sprintf("%x", attrs.MD5),
		}
		fileName := getFileName(attrs.Name)
		fd.FileName = fileName
		scanData <- fd
	}
	close(scanData)
}

func getFileName(objectPath string) string {
	fileParts := strings.Split(objectPath, "/")
	if len(fileParts) < 1 {
		panic("Does not have a valid filename. ObjectPath:" + objectPath)
	}
	return fileParts[len(fileParts)-1]
}

type GStorageScan struct {
	Bucket string
}
