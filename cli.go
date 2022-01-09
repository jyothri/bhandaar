package main

import (
	"fmt"
	"os"
	"sync"

	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/web"
)

var parentDir string

func main() {
	parentDir = "/Users/jyothri/test"
	// parentDir = "C:\\Users\\jyoth\\technical\\"
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
			go collect.CloudDrive(&lock)
		case 3:
			go collect.CloudStorage(&lock)
		case 4:
			printStats()
		case 5:
			lock.Lock()
			lock.Unlock()
			fmt.Println("Provide saveFile to use?")
			var saveFile string
			fmt.Scan(&saveFile)
			collect.ParseInfo = *db.LoadStatsFromFile(saveFile)
		case 6:
			go web.StartWebServer()
		default:
			fmt.Println("Invalid selection")
		}
	}
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
	go collect.LocalDrive(parentDir, lock)
}

func printOptions() {
	fmt.Println("#################   Choice   #################")
	fmt.Println(" Please make a choice")
	fmt.Println("0 Exit")
	fmt.Println("1 Scan Local Drive")
	fmt.Println("2 Scan Google Drive")
	fmt.Println("3 Scan Cloud Storage")
	fmt.Println("4 Print sample of stats")
	fmt.Println("5 Load saveFile")
	fmt.Println("6 Start web server")
	fmt.Print(" > ")
}

func printStats() {
	fmt.Println("#################   STATS   #################")
	count := 1
	for _, fd := range collect.ParseInfo {
		fmt.Printf("fileName: %30.30v Path: %-45.45v Size: %10v ModifiedTime: %30.30v Directory?: %6v Contained files: %v \n", fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.IsDir, fd.FileCount)
		if count > 5 {
			break
		}
		count++
	}
	fmt.Printf("Collection size:%d\n", len(collect.ParseInfo))
}
