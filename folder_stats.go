package main

import (
	"fmt"
	"time"
)

type fileData struct {
	FilePath  string
	IsDir     bool
	Size      uint
	ModTime   time.Time
	FileCount uint
}

var parseInfo map[string][]fileData

func main() {
	cloudDrive()
	printStats()
	localDrive()
}

func printStats() {
	for fileName, filesData := range parseInfo {
		for _, fd := range filesData {
			fmt.Printf("fileName: %30.30v Path: %v Size: %v ModifiedTime: %v Directory?: %v Contained files: %v \n ", fileName, fd.FilePath, fd.Size, fd.ModTime, fd.IsDir, fd.FileCount)
		}
	}
}

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}
