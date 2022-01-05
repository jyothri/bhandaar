package main

import (
	"fmt"
	"os"
	"sync"
)

var parseInfo []fileData
var parentDir string
var saveFile string

func main() {
	parentDir = "/Users/jyothri/test"
	// parentDir = "C:\\Users\\jyoth\\technical\\"
	saveFile = "./FolderStats.gob"
	var choice int
	var lock sync.RWMutex
	for {
		printOptions()
		fmt.Scan(&choice)
		fmt.Println()
		switch choice {
		case 0:
			os.Exit(0)
		case 1:
			optionLocalDrive(&lock)
		case 2:
			go cloudDrive(&lock)
		case 3:
			go cloudStorage(&lock)
		case 4:
			optionSaveStats(&lock)
		case 5:
			optionLoadStats(&lock)
		case 6:
			printStats()
		case 7:
			fmt.Printf("Provide saveFile to use. <enter> to use default %v \n", saveFile)
			var newFile string
			fmt.Scan(newFile)
			if newFile != "" {
				saveFile = newFile
			}
		case 8:
			go StartWebServer()
		default:
			fmt.Println("Invalid selection")
		}
	}
}

func optionSaveStats(lock *sync.RWMutex) {
	lock.RLock()
	defer lock.RUnlock()
	fmt.Printf("Saving stats to file %v \n", saveFile)
	saveStatsToFile(saveFile)
}

func optionLoadStats(lock *sync.RWMutex) {
	lock.Lock()
	defer lock.Unlock()
	fmt.Printf("Loading stats from file %v \n", saveFile)
	loadStatsFromFile(saveFile)
}

func optionLocalDrive(lock *sync.RWMutex) {
	fmt.Printf("Current dir: %v Change directory to scan (y/n)? \n", parentDir)
	var newParentDir string
	var changeDir string
	fmt.Scan(&changeDir)
	if changeDir == "y" {
		fmt.Println("Enter new directory to scan")
		fmt.Scan(&newParentDir)
		parentDir = newParentDir
	}
	go localDrive(parentDir, lock)
}

func printOptions() {
	fmt.Println("#################   Choice   #################")
	fmt.Println(" Please make a choice")
	fmt.Println("0 Exit")
	fmt.Println("1 Scan Local Drive")
	fmt.Println("2 Scan Google Drive")
	fmt.Println("3 Scan Cloud Storage")
	fmt.Println("4 Save stats to file")
	fmt.Println("5 Load stats from file")
	fmt.Println("6 Print sample of in-memory stats")
	fmt.Println("7 Change saveFile")
	fmt.Println("8 Start web server")
	fmt.Print(" > ")
}

func printStats() {
	fmt.Println("#################   STATS   #################")
	count := 1
	for _, fd := range parseInfo {
		fmt.Printf("fileName: %30.30v Path: %-45.45v Size: %10v ModifiedTime: %30.30v Directory?: %6v Contained files: %v \n", fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.IsDir, fd.FileCount)
		if count > 5 {
			break
		}
		count++
	}
	fmt.Printf("Collection size:%d\n", len(parseInfo))
}

func checkError(err error) {
	if err != nil {
		panic(err)
	}
}
