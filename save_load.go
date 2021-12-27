package main

import (
	"encoding/gob"
	"log"
	"os"
)

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
	parseInfo = make(map[string][]fileData)

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
