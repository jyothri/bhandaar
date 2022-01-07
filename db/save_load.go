package db

import (
	"encoding/gob"
	"log"
	"os"
)

func saveStatsToFile(saveFile string, info *[]FileData) {
	gobFile, err := os.Create(saveFile)
	checkError(err)
	defer closeFile(gobFile)

	encoder := gob.NewEncoder(gobFile)

	// Encoding the data
	err = encoder.Encode(*info)
	checkError(err)
}

func loadStatsFromFile(saveFile string) *[]FileData {
	_, err := os.Stat(saveFile)
	if err != nil {
		if os.IsNotExist(err) {
			log.Fatal("File does not exist.")
		}
		panic(err)
	}
	info := make([]FileData, 0)

	gobFile, err := os.Open(saveFile)
	checkError(err)
	defer closeFile(gobFile)

	decoder := gob.NewDecoder(gobFile)

	// Decoding the data
	err = decoder.Decode(&info)
	checkError(err)
	return &info
}

func closeFile(fileToClose *os.File) {
	err := fileToClose.Close()
	checkError(err)
}
