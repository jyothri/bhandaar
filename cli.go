package main

import (
	"fmt"
	"os"

	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/web"
)

var parentDir string

func main() {
	if constants.StartWebServer {
		fmt.Println("Starting web server on startup.")
		go web.StartWebServer()
	}

	parentDir = "/Users/jyothri/test"
	// parentDir = "C:\\Users\\jyoth\\technical\\"
	var choice int
	for {
		printOptions()
		fmt.Scan(&choice)
		fmt.Println()
		switch choice {
		case 0:
			os.Exit(0)
		case 1:
			optionLocalDrive()
		case 2:
			go collect.CloudDrive("name contains 'tesla'")
		case 3:
			go collect.CloudStorage("jyo-pics")
		case 4:
			go collect.Gmail("label:inbox label:unread from:project baseline")
		case 5:
			go web.StartWebServer()
		default:
			fmt.Println("Invalid selection")
		}
	}
}

func optionLocalDrive() {
	fmt.Printf("Current dir: %v Change directory to scan (y/n)? \n", parentDir)
	var newParentDir string
	var changeDir string
	fmt.Scan(&changeDir)
	if changeDir == "y" {
		fmt.Println("Enter new directory to scan")
		fmt.Scan(&newParentDir)
		parentDir = newParentDir
	}
	go collect.LocalDrive(parentDir)
}

func printOptions() {
	fmt.Println("#################   Choice   #################")
	fmt.Println(" Please make a choice")
	fmt.Println("0 Exit")
	fmt.Println("1 Scan Local Drive")
	fmt.Println("2 Scan Google Drive")
	fmt.Println("3 Scan Cloud Storage")
	fmt.Println("4 Scan GMail")
	fmt.Println("5 Start web server")
	fmt.Print(" > ")
}
