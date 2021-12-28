package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// List of fields to be retreived on file resource from the drive API.
var fields []string = []string{"size", "id", "name", "mimeType", "parents", "modifiedTime"}
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

func cloudDrive(parent chan<- string) {
	defer close(parent)
	parseInfo = make(map[string][]fileData)
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
		parent <- fmt.Sprintf("Data collected so far: %d", len(parseInfo))
		filesListCall = filesListCall.PageToken(fileList.NextPageToken)
	}
}

func parseFileList(fileList *drive.FileList) {
	for _, file := range fileList.Files {
		fd := fileData{
			FilePath:  file.Id,
			IsDir:     file.MimeType == "application/vnd.google-apps.folder",
			ModTime:   parseTime(file.ModifiedTime),
			FileCount: 1,
		}
		if !fd.IsDir {
			fd.Size = uint(file.Size)
			fd.FileCount = 1
			for _, parent := range file.Parents {
				parentFile := getFileInfo(parent)
				_, present := parseInfo[parentFile.Name]
				if !present {
					parseInfo[parentFile.Name] = append(make([]fileData, 0), fileData{FilePath: parent, IsDir: true, ModTime: parseTime(parentFile.ModifiedTime), FileCount: 0})
				}
				prevValues := parseInfo[parentFile.Name]
				updatedValues := make([]fileData, 0)
				for _, fd2 := range prevValues {
					if fd2.FilePath != parent {
						continue
					}
					fd2.FileCount += 1
					fd2.Size += fd.Size
					updatedValues = append(updatedValues, fd2)
				}
				parseInfo[parentFile.Name] = updatedValues
			}
			parseInfo[file.Name] = append(parseInfo[file.Name], fd)
		} else {
			// It is possible that directory is already captured as part of the file traversal.
			// Insert only if the directory info is not already saved.
			if !existsKeyAndId(parseInfo, file.Name, file.Id) {
				parseInfo[file.Name] = append(parseInfo[file.Name], fd)
			}
		}

	}
}

func getFileInfo(fileId string) *drive.File {
	file, err := driveService.Files.Get(fileId).Fields(googleapi.Field(strings.Join(fields, ","))).Do()
	checkError(err)
	return file
}

func addPrefix(in []string, prefix string) []string {
	out := make([]string, len(in))
	for idx, str := range in {
		out[idx] = prefix + str
	}
	return out
}

func existsKeyAndId(theMap map[string][]fileData, mapKey string, id string) bool {
	values, present := theMap[mapKey]
	if !present {
		return false
	} else {
		for _, fd2 := range values {
			if !fd2.IsDir {
				continue
			}
			if fd2.FilePath == id {
				return true
			}
		}
	}
	return false
}

func parseTime(inputTime string) time.Time {
	parsedTime, err := time.Parse(time.RFC3339, inputTime)
	checkError(err)
	return parsedTime
}
