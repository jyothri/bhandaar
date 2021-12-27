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
	// const parentDir = "C:\\Users\\jyoth\\technical\\"
	const parentDir = "/Users/jyothri/test"
	const saveFile = "./FolderStats.gob"
	cloudDrive()
	printStats()
	localDrive(parentDir)
	printStats()

	fmt.Printf("Saving stats to file %v \n", saveFile)
	saveStatsToFile(saveFile)

	fmt.Printf("Loading stats from file %v \n", saveFile)
	loadStatsFromFile(saveFile)
}

func printStats() {
	fmt.Println("#################   STATS   #################")
	for fileName, filesData := range parseInfo {
		for _, fd := range filesData {
			fmt.Printf("fileName: %30.30v Path: %-45.45v Size: %10v ModifiedTime: %30.30v Directory?: %6v Contained files: %v \n", fileName, fd.FilePath, fd.Size, fd.ModTime, fd.IsDir, fd.FileCount)
		}
	}
}

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}
