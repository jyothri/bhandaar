package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/web"
)

var parentDir string

func init() {
	options := &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02 15:04:05.999"))
			}
			return a
		},
		Level: slog.LevelDebug,
	}

	handler := slog.NewTextHandler(os.Stdout, options)
	logger := slog.New(handler)
	slog.SetDefault(logger)
	slog.SetLogLoggerLevel(slog.LevelDebug)
}

func main() {
	if constants.StartWebServer {
		slog.Info("Starting web server on startup.")
		go web.StartWebServer()
	}

	parentDir = "/Users/jyothri/test"
	// parentDir = "C:\\Users\\jyoth\\technical\\"
	var choice int
	for {
		printOptions()
		fmt.Scan(&choice)
		slog.Info("")
		switch choice {
		case 0:
			os.Exit(0)
		case 1:
			optionLocalDrive()
		case 2:
			driveScan := collect.GDriveScan{
				QueryString: "name contains 'tesla'",
			}
			go collect.CloudDrive(driveScan)
		case 3:
			storageScan := collect.GStorageScan{
				Bucket: "jyo-pics",
			}
			go collect.CloudStorage(storageScan)
		case 4:
			gmailScan := collect.GMailScan{
				Filter: "label:inbox label:unread from:project baseline",
			}
			go collect.Gmail(gmailScan)
		case 5:
			gphotosScan := collect.GPhotosScan{}
			go collect.Photos(gphotosScan)
		case 6:
			go web.StartWebServer()
		default:
			slog.Error("Invalid selection")
		}
	}
}

func optionLocalDrive() {
	slog.Info(fmt.Sprintf("Current dir: %v Change directory to scan (y/n)?", parentDir))
	var newParentDir string
	var changeDir string
	fmt.Scan(&changeDir)
	if changeDir == "y" {
		slog.Info("Enter new directory to scan")
		fmt.Scan(&newParentDir)
		parentDir = newParentDir
	}
	localScan := collect.LocalScan{
		Path: parentDir,
	}
	go collect.LocalDrive(localScan)
}

func printOptions() {
	slog.Info("#################   Choice   #################")
	slog.Info(" Please make a choice")
	slog.Info("0 Exit")
	slog.Info("1 Scan Local Drive")
	slog.Info("2 Scan Google Drive")
	slog.Info("3 Scan Cloud Storage")
	slog.Info("4 Scan GMail")
	slog.Info("5 Scan Google Photos")
	slog.Info("6 Start web server")
	slog.Info(" > ")
}
