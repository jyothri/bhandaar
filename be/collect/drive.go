package collect

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// List of fields to be retreived on file resource from the drive API.
var fields []string = []string{"size", "id", "name", "mimeType", "parents", "modifiedTime", "md5Checksum"}
var paginationFields []string = []string{"nextPageToken", "incompleteSearch"}

const pageSize = 1000

var cloudConfig *oauth2.Config

func init() {
	cloudConfig = &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{drive.DriveReadonlyScope},
	}
}

func getDriveService(refreshToken string) *drive.Service {
	tokenSrc := oauth2.Token{
		RefreshToken: refreshToken,
	}
	ctx := context.Background()
	driveService, err := drive.NewService(ctx, option.WithTokenSource(cloudConfig.TokenSource(ctx, &tokenSrc)))
	checkError(err)
	return driveService
}

func CloudDrive(driveScan GDriveScan) int {
	scanData := make(chan db.FileData, 10)
	scanId := db.LogStartScan("google_drive")
	driveService := getDriveService(driveScan.RefreshToken)
	go db.SaveScanMetadata("", "", driveScan.QueryString, scanId)
	go startCloudDrive(driveService, scanId, driveScan.QueryString, scanData)
	go db.SaveStatToDb(scanId, scanData)
	return scanId
}

func startCloudDrive(driveService *drive.Service, scanId int, queryString string, scanData chan<- db.FileData) {
	lock.Lock()
	defer lock.Unlock()
	filesListCall := driveService.Files.List().PageSize(pageSize).Q(queryString).Fields(googleapi.Field(strings.Join(append(addPrefix(fields, "files/"), paginationFields...), ",")))
	hasNextPage := true
	for hasNextPage {
		fileList, err := filesListCall.Do()
		checkError(err)
		if fileList.IncompleteSearch {
			checkError(errors.New("incomplete search from drive API"))
		}
		parseFileList(fileList, scanData)
		if fileList.NextPageToken == "" {
			hasNextPage = false
		}
		filesListCall = filesListCall.PageToken(fileList.NextPageToken)
	}
	close(scanData)
}

func parseFileList(fileList *drive.FileList, scanData chan<- db.FileData) {
	for _, file := range fileList.Files {
		fd := db.FileData{
			FileName:  file.Name,
			FilePath:  file.Id,
			IsDir:     file.MimeType == "application/vnd.google-apps.folder",
			ModTime:   parseTime(file.ModifiedTime),
			FileCount: 1,
		}
		if !fd.IsDir {
			fd.Size = uint(file.Size)
			fd.FileCount = 1
			fd.Md5Hash = file.Md5Checksum
			scanData <- fd
		}
	}
}

func addPrefix(in []string, prefix string) []string {
	out := make([]string, len(in))
	for idx, str := range in {
		out[idx] = prefix + str
	}
	return out
}

func parseTime(inputTime string) time.Time {
	parsedTime, err := time.Parse(time.RFC3339, inputTime)
	checkError(err)
	return parsedTime
}

type GDriveScan struct {
	QueryString  string
	RefreshToken string
}
