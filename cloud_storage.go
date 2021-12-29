package main

import (
	"context"
	"strings"
	"sync"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

func cloudStorage(lock *sync.RWMutex) {
	lock.Lock()
	defer lock.Unlock()
	parseInfo = make(map[string][]fileData)
	ctx := context.Background()

	// Create a client.
	client, err := storage.NewClient(ctx)
	checkError(err)
	defer client.Close()

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
		}
		fileName := getFileName(attrs.Name)
		parseInfo[fileName] = append(parseInfo[fileName], fd)

		parentDir := getParentDirectory(attrs.Name)
		_, present := parseInfo[parentDir]
		if !present {
			parseInfo[parentDir] = append(make([]fileData, 0), fileData{FilePath: parentDir, IsDir: true, ModTime: attrs.Updated, FileCount: 0})
		}
		parseInfo[parentDir][0].Size = parseInfo[parentDir][0].Size + fd.Size
	}
}

func getFileName(objectPath string) string {
	fileParts := strings.Split(objectPath, "/")
	if len(fileParts) < 1 {
		panic("Does not have a valid filename. ObjectPath:" + objectPath)
	}
	return fileParts[len(fileParts)-1]
}

func getParentDirectory(objectPath string) string {
	fileParts := strings.Split(objectPath, "/")
	if len(fileParts) < 2 {
		panic("Does not have a parent directory. ObjectPath:" + objectPath)
	}
	return fileParts[len(fileParts)-2]
}
