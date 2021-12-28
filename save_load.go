package main

import (
	"encoding/gob"
	"log"
	"os"
)

func saveStatsToFile(saveFile string) {
	gobFile, err := os.Create(saveFile)
	checkError(err)
	defer closeFile(gobFile)

	encoder := gob.NewEncoder(gobFile)

	// Encoding the data
	err = encoder.Encode(parseInfo)
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
	parseInfo = make(map[string][]fileData)

	gobFile, err := os.Open(saveFile)
	checkError(err)
	defer closeFile(gobFile)

	decoder := gob.NewDecoder(gobFile)

	// Decoding the data
	err = decoder.Decode(&parseInfo)
	checkError(err)
}

func closeFile(fileToClose *os.File) {
	err := fileToClose.Close()
	checkError(err)
}
