package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/web"
)

var parentDir string

func init() {
	flag.StringVar(&constants.OauthClientId, "oauth_client_id", "dummy", "oauth client id")
	flag.StringVar(&constants.OauthClientSecret, "oauth_client_secret", "dummy", "oauth client secret")
	flag.StringVar(&constants.RefreshToken, "refresh_token", "dummy", "refresh token for the user")
	flag.BoolVar(&constants.StartWebServer, "start_web_server", false, "Set to true to start a web server.")
	flag.Parse()
}

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
			printStats()
		case 5:
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
