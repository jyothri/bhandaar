package main

import (
	"encoding/gob"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

func localDrive() {
	// const parentDir = "C:\\Users\\jyoth\\technical\\"
	const parentDir = "/Users/jyothri/test"
	const saveFile = "./FolderStats.gob"
	parseInfo = make(map[string][]fileData)

	collectStats(parentDir)

	fmt.Printf("Saving stats to file %v \n", saveFile)
	saveStatsToFile(saveFile)

	fmt.Printf("Loading stats from file %v \n", saveFile)
	parseInfo = make(map[string][]fileData)
	loadStatsFromFile(saveFile)

	fmt.Println("Printing stats")
	printStats()
}

// Gathers the info for the directory.
// Returns a tuple of (size of the directory, no. of files contained)
func collectStats(parentDir string) (int64, int64) {
	var directorySize int64
	var fileCount int64 = 0
	err := filepath.Walk(parentDir, func(path string, info fs.FileInfo, err error) error {
		checkError(err)
		// filepath.Walk also traverses the parent directory.
		// As we call the same function recursively, we would
		// end up invoking with the same arg again which results
		// in an infinite loop. This check prevents traversing
		// the same directory again.
		if parentDir == path {
			return nil
		}

		// Skip hidden files and directories
		if runtime.GOOS != "windows" && info.Name()[0:1] == "." {
			// unix/linux file or directory that starts with . is hidden
			return nil
		}

		fd := fileData{
			FilePath:  path,
			IsDir:     info.IsDir(),
			ModTime:   info.ModTime(),
			FileCount: 1,
		}
		if info.IsDir() {
			ds, fc := collectStats(path)
			directorySize += ds
			fileCount += fc
			fd.Size = uint(ds)
			fd.FileCount = uint(fc)
		} else {
			directorySize += info.Size()
			fileCount++
			fd.Size = uint(info.Size())
			fd.FileCount = 1
		}
		parseInfo[info.Name()] = append(parseInfo[info.Name()], fd)
		// filepath.Walk works recursively. However our call to
		// collectStats also performs the traversal recursively.
		// Returns `filepath.SkipDir` limits to only the files and folders
		// in current directory to prevent multiple traversals.
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	checkError(err)
	return directorySize, fileCount
}

func saveStatsToFile(saveFile string) {
	gobFile, err := os.Create(saveFile)
	checkError(err)
	defer gobFile.Close()

	encoder := gob.NewEncoder(gobFile)

	// Encoding the data
	err = encoder.Encode(parseInfo)
	checkError(err)
	err = gobFile.Close()
	checkError(err)
}

func loadStatsFromFile(saveFile string) {
	_, err := os.Stat(saveFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatal("File does not exist.")
		}
		panic(err)
	}

	gobFile, err := os.Open(saveFile)
	checkError(err)
	defer gobFile.Close()

	decoder := gob.NewDecoder(gobFile)

	// Decoding the data
	err = decoder.Decode(&parseInfo)
	checkError(err)
	err = gobFile.Close()
	checkError(err)
}
