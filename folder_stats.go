package main

import (
	"encoding/gob"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
)

type fileData struct {
	FilePath string
	IsDir    bool
	Size     uint
	ModTime  time.Time
}

func main() {
	// const parentDir = "C:\\Users\\jyoth\\technical\\"
	const parentDir = "/Users/jyothri/test/"
	const saveFile = "./FolderStats.gob"
	var parseInfo map[string][]fileData
	var err error
	parseInfo, err = collectStats(parentDir)
	if err != nil {
		log.Fatal(err)
		return
	}

	fmt.Println("Saving stats to file")
	err = saveStatsToFile(parseInfo, saveFile)
	if err != nil {
		log.Fatal(err)
		return
	}

	fmt.Println("Loading stats from file")
	parseInfo, err = loadStatsFromFile(saveFile)
	if err != nil {
		log.Fatal(err)
		return
	}
	fmt.Println("Printing stats from file")
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
			FilePath: path,
			IsDir:    d.IsDir(),
			Size:     uint(fi.Size()),
			ModTime:  fi.ModTime(),
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
			if fileData.IsDir {
				continue
			}
			filePaths = append(filePaths, fileData.FilePath)
		}
		if len(filePaths) == 0 {
			continue
		}
		fmt.Printf("filename: %v occurences: %v\n ", fileName, len(filePaths))
	}
}

func saveStatsToFile(parseInfo map[string][]fileData, saveFile string) error {
	gobFile, err := os.Create(saveFile)
	if err != nil {
		return err
	}
	defer gobFile.Close()

	encoder := gob.NewEncoder(gobFile)

	// Encoding the data
	err = encoder.Encode(parseInfo)
	if err != nil {
		return err
	}
	gobFile.Close()
	fmt.Printf("Data saved to file %v \n", saveFile)
	return nil
}

func loadStatsFromFile(saveFile string) (map[string][]fileData, error) {
	_, err := os.Stat(saveFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatal("File does not exist.")
		}
		return nil, err
	}

	gobFile, err := os.Open(saveFile)
	if err != nil {
		return nil, err
	}
	defer gobFile.Close()

	decoder := gob.NewDecoder(gobFile)

	var parseInfo map[string][]fileData
	// Encoding the data
	err = decoder.Decode(&parseInfo)
	if err != nil {
		return nil, err
	}
	gobFile.Close()
	fmt.Printf("Data loaded from file %v \n", saveFile)
	return parseInfo, nil
}
