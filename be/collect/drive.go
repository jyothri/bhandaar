package collect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

func getDriveService(refreshToken string) (*drive.Service, error) {
	tokenSrc := oauth2.Token{
		RefreshToken: refreshToken,
	}
	ctx := context.Background()
	driveService, err := drive.NewService(ctx, option.WithTokenSource(cloudConfig.TokenSource(ctx, &tokenSrc)))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service: %w", err)
	}
	return driveService, nil
}

func CloudDrive(driveScan GDriveScan) (int, error) {
	// Phase 1: Create scan record (synchronous)
	scanId, err := db.LogStartScan("google_drive")
	if err != nil {
		return 0, fmt.Errorf("failed to start google drive scan (query=%s): %w", driveScan.QueryString, err)
	}

	// Get Drive service
	driveService, err := getDriveService(driveScan.RefreshToken)
	if err != nil {
		return 0, fmt.Errorf("failed to get drive service for scan %d: %w", scanId, err)
	}

	// Save metadata in background
	go func() {
		if err := db.SaveScanMetadata("", "", driveScan.QueryString, scanId); err != nil {
			slog.Error("Failed to save scan metadata",
				"scan_id", scanId,
				"query", driveScan.QueryString,
				"error", err)
		}
	}()

	// Phase 2: Start collection in background (asynchronous)
	scanData := make(chan db.FileData, 10)
	go func() {
		defer close(scanData)

		err := startCloudDrive(driveService, scanId, driveScan.QueryString, scanData)
		if err != nil {
			slog.Error("Google Drive scan collection failed",
				"scan_id", scanId,
				"query", driveScan.QueryString,
				"error", err)
			db.MarkScanFailed(scanId, err.Error())
			return
		}
	}()

	// Start processing file data in background
	go db.SaveStatToDb(scanId, scanData)

	return scanId, nil
}

func startCloudDrive(driveService *drive.Service, scanId int, queryString string, scanData chan<- db.FileData) error {
	lock.Lock()
	defer lock.Unlock()
	filesListCall := driveService.Files.List().PageSize(pageSize).Q(queryString).Fields(googleapi.Field(strings.Join(append(addPrefix(fields, "files/"), paginationFields...), ",")))
	hasNextPage := true
	for hasNextPage {
		fileList, err := filesListCall.Do()
		if err != nil {
			return fmt.Errorf("failed to list drive files for query '%s': %w", queryString, err)
		}
		if fileList.IncompleteSearch {
			return errors.New("incomplete search from drive API")
		}
		parseFileList(fileList, scanData)
		if fileList.NextPageToken == "" {
			hasNextPage = false
		}
		filesListCall = filesListCall.PageToken(fileList.NextPageToken)
	}
	return nil
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
	if err != nil {
		slog.Warn("Failed to parse time, using zero time",
			"input", inputTime,
			"error", err)
		return time.Time{} // Return zero time on error
	}
	return parsedTime
}

type GDriveScan struct {
	QueryString  string
	RefreshToken string
}
