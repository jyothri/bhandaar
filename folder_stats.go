package main

import (
	"fmt"
	"io/fs"
	"log"
	"path/filepath"
	"time"
)

type fileData struct {
	filePath string
	isDir    bool
	size     uint
	modTime  time.Time
}

func main() {
	// const parentDir = "C:\\Users\\jyoth\\technical\\"
	const parentDir = "/Users/jyothri/test/"
	parseInfo, err := collectStats(parentDir)
	if err != nil {
		log.Fatal(err)
		return
	}
	printStats(parseInfo)
}

func collectStats(parentDir string) (map[string][]fileData, error) {
	var parseInfo = make(map[string][]fileData)

	err := filepath.WalkDir(parentDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		var fd = fileData{
			filePath: path,
			isDir:    d.IsDir(),
			size:     uint(fi.Size()),
			modTime:  fi.ModTime(),
		}
		parseInfo[fi.Name()] = append(parseInfo[fi.Name()], fd)
		return nil
	})

	return parseInfo, err
}

func printStats(parseInfo map[string][]fileData) {
	for fileName, filesData := range parseInfo {
		var filePaths = make([]string, 0)
		for _, fileData := range filesData {
			if fileData.isDir {
				continue
			}
			filePaths = append(filePaths, fileData.filePath)
		}
		if len(filePaths) == 0 {
			continue
		}
		fmt.Printf("filename: %v occurences: %v\n ", fileName, len(filePaths))
	}
}
