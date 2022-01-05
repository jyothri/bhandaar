package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

func cloudStorage(lock *sync.RWMutex) {
	lock.Lock()
	defer lock.Unlock()
	parseInfo = make([]fileData, 0)
	ctx := context.Background()

	// Create a client.
	client, err := storage.NewClient(ctx)
	checkError(err)
	defer client.Close()

	scanId := logStartScan("google_storage")
	// Create a Bucket instance.
	bucket := client.Bucket("jyo-pics")

	query := &storage.Query{Prefix: ""}

	it := bucket.Objects(ctx, query)
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		checkError(err)
		fd := fileData{
			FilePath:  attrs.MediaLink,
			IsDir:     false,
			ModTime:   attrs.Updated,
			FileCount: 1,
			Size:      uint(attrs.Size),
			Md5Hash:   fmt.Sprintf("%x", attrs.MD5),
		}
		fileName := getFileName(attrs.Name)
		fd.FileName = fileName
		parseInfo = append(parseInfo, fd)
	}
	logCompleteScan(scanId)
}

func getFileName(objectPath string) string {
	fileParts := strings.Split(objectPath, "/")
	if len(fileParts) < 1 {
		panic("Does not have a valid filename. ObjectPath:" + objectPath)
	}
	return fileParts[len(fileParts)-1]
}
