package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	const parentDir = "C:\\Users\\jyoth\\technical\\"
	dirEntry, err := os.ReadDir(parentDir)
	if err != nil {
		log.Fatal(err)
	}
	for _, file := range dirEntry {
		fmt.Println(file.Name(), file.IsDir())
	}
}
