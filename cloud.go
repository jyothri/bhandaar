package main

import (
	"context"
	"errors"
	"flag"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// List of fields to be retreived on file resource from the drive API.
var fields []string = []string{"size", "id", "name", "mimeType", "parents", "modifiedTime", "md5Checksum"}
var paginationFields []string = []string{"nextPageToken", "incompleteSearch"}

// Filter files list by this criteria.
const queryString = "name contains 'tesla'"

var driveService *drive.Service

func init() {
	oauth_client_id := flag.String("oauth_client_id", "dummy", "oauth client id")
	oauth_client_secret := flag.String("oauth_client_secret", "dummy", "oauth client secret")
	refresh_token := flag.String("refresh_token", "dummy", "refresh token for the user")
	flag.Parse()

	config := &oauth2.Config{
		ClientID:     *oauth_client_id,
		ClientSecret: *oauth_client_secret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{drive.DriveReadonlyScope},
	}
	tokenSrc := oauth2.Token{
		RefreshToken: *refresh_token,
	}
	ctx := context.Background()
	var err error
	driveService, err = drive.NewService(ctx, option.WithTokenSource(config.TokenSource(ctx, &tokenSrc)))
	checkError(err)
}

func cloudDrive(lock *sync.RWMutex) {
	lock.Lock()
	defer lock.Unlock()
	parseInfo = make([]fileData, 0)
	scanId := logStartScan("google_drive")
	filesListCall := driveService.Files.List().PageSize(5).Q(queryString).Fields(googleapi.Field(strings.Join(append(addPrefix(fields, "files/"), paginationFields...), ",")))
	hasNextPage := true
	for hasNextPage {
		fileList, err := filesListCall.Do()
		checkError(err)
		if fileList.IncompleteSearch {
			checkError(errors.New("incomplete search from drive API"))
		}
		parseFileList(fileList)
		if fileList.NextPageToken == "" {
			hasNextPage = false
		}
		filesListCall = filesListCall.PageToken(fileList.NextPageToken)
	}
	saveStatsToDb(scanId, &parseInfo)
	logCompleteScan(scanId)
}

func parseFileList(fileList *drive.FileList) {
	for _, file := range fileList.Files {
		fd := fileData{
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
			parseInfo = append(parseInfo, fd)
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
