package collect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

// List of fields to be retreived on file resource from the drive API.
var fields []string = []string{"size", "id", "name", "mimeType", "parents", "modifiedTime", "md5Checksum", "trashed", "ownedByMe"}
var paginationFields []string = []string{"nextPageToken", "incompleteSearch"}

const pageSize = 1000

const folderMimeType = "application/vnd.google-apps.folder"

// folderIdPattern is what a Drive folder ID looks like. IDs go inside quotes
// in a Drive query, so nothing else may.
var folderIdPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,}$`)

// ValidFolderId reports whether id looks like a Drive folder ID.
func ValidFolderId(id string) bool {
	return folderIdPattern.MatchString(id)
}

// Built on first use, after main has parsed the OAuth flags. The collector
// reads only metadata (docs/specs/request-drive-scans.md, "Scopes").
var cloudConfig = sync.OnceValue(func() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{drive.DriveMetadataReadonlyScope},
	}
})

func getDriveService(refreshToken string) (*drive.Service, error) {
	tokenSrc := oauth2.Token{
		RefreshToken: refreshToken,
	}
	ctx := context.Background()
	driveService, err := drive.NewService(ctx, option.WithTokenSource(cloudConfig().TokenSource(ctx, &tokenSrc)))
	if err != nil {
		return nil, fmt.Errorf("failed to create drive service: %w", err)
	}
	return driveService, nil
}

// RequestError is a problem with a scan request itself, such as a folder
// that doesn't exist, to answer with 400 and Message.
type RequestError struct {
	Message string
}

func (e *RequestError) Error() string {
	return e.Message
}

func CloudDrive(driveScan GDriveScan, userID int64) (int, error) {
	// The account and a folder are checked before the scan is recorded, so
	// a bad folder is answered at once, without a failed scan.
	account, err := resolveAccount(userID, driveScan.ClientKey, driveScan.RefreshToken)
	if err != nil {
		return 0, err
	}
	driveService, err := getDriveService(account.RefreshToken)
	if err != nil {
		return 0, err
	}
	rootId, err := myDriveId(driveService)
	if err != nil {
		return 0, err
	}
	searchPath, folderPath := "", ""
	// Folders the scan knows before it starts: the one scanned and those
	// above it.
	var known []*drive.File
	if driveScan.FolderId != "" {
		folder, err := checkFolder(driveService, driveScan.FolderId)
		if err != nil {
			return 0, err
		}
		var above []*drive.File
		if folderPath, above, err = pathOfFolder(driveService, folder, rootId); err != nil {
			return 0, err
		}
		known = append(above, folder)
		searchPath = fmt.Sprintf("%s (%s)", folderPath, folder.Id)
		if driveScan.Recursive {
			searchPath += " and subfolders"
		}
	}

	// Phase 1: Create scan record (synchronous)
	scanId, err := db.LogStartScan("google_drive", userID)
	if err != nil {
		return 0, fmt.Errorf("failed to start google drive scan (query=%s): %w", driveScan.QueryString, err)
	}

	// Save metadata in background
	go func() {
		if err := db.SaveScanMetadata(account.Name, account.ClientKey, searchPath, driveScan.QueryString, scanId); err != nil {
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

		err := startCloudDrive(driveService, scanId, driveScan, folderPath, account.ClientKey, rootId, known, scanData)
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
	go db.SaveDriveScanToDb(scanId, driveRecord(driveScan, account.ClientKey, rootId), scanData)

	return scanId, nil
}

// unfilteredQueries are the Request page's default Drive queries, which
// match everything in a scan's scope that isn't trashed (and, with 'me' in
// owners, that the account owns). Only a scan with one of these deletes
// from the account's record what it didn't see. See docs/specs/browse.md,
// "Updating it".
var unfilteredQueries = map[string]bool{
	"mimeType != '" + folderMimeType + "' and trashed = false":                    false,
	"mimeType != '" + folderMimeType + "' and trashed = false and 'me' in owners": true,
}

// driveRecord is what a scan updates in its account's record; nil for a
// scan by refresh token, which has no linked account.
func driveRecord(driveScan GDriveScan, clientKey string, rootId string) *db.DriveRecord {
	if clientKey == "" {
		return nil
	}
	ownedOnly, sawAll := unfilteredQueries[driveScan.QueryString]
	return &db.DriveRecord{
		ClientKey: clientKey,
		MyDriveId: rootId,
		FolderId:  driveScan.FolderId,
		Recursive: driveScan.Recursive || driveScan.FolderId == "",
		SawAll:    sawAll,
		OwnedOnly: ownedOnly,
	}
}

// myDriveId is the ID of the account's My Drive folder.
func myDriveId(driveService *drive.Service) (string, error) {
	root, err := driveService.Files.Get("root").Fields("id").Do()
	if err != nil {
		return "", fmt.Errorf("failed to look up the My Drive folder: %w", err)
	}
	return root.Id, nil
}

// checkFolder returns the folder id, or a RequestError when the account
// can't scan it.
func checkFolder(driveService *drive.Service, id string) (*drive.File, error) {
	if !ValidFolderId(id) {
		return nil, &RequestError{Message: "That isn't a Google Drive folder ID."}
	}
	folder, err := driveService.Files.Get(id).Fields("id, name, mimeType, trashed, parents, modifiedTime, ownedByMe").Do()
	var apiErr *googleapi.Error
	if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
		return nil, &RequestError{Message: "Folder not found, or this account can't see it."}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to look up folder %s: %w", id, err)
	}
	if folder.MimeType != folderMimeType {
		return nil, &RequestError{Message: fmt.Sprintf("%q is a file, not a folder.", folder.Name)}
	}
	if folder.Trashed {
		return nil, &RequestError{Message: fmt.Sprintf("Folder %q is in the trash.", folder.Name)}
	}
	return folder, nil
}

// startCloudDrive saves the files the scan covers, each with its folder
// path: under folderPath (the folder's own path) for a folder scan, or
// under "My Drive" for the whole Drive. Then it saves a row for each
// folder below that, with the total size and file count under it, as local
// scans do. Every folder it lists, and the known ones, go to the account's
// record only. rootId is the My Drive folder's ID.
func startCloudDrive(driveService *drive.Service, scanId int, driveScan GDriveScan, folderPath string, clientKey string, rootId string, known []*drive.File, scanData chan<- db.FileData) error {
	lock.Lock()
	defer lock.Unlock()
	resetCounters()
	ticker := time.NewTicker(5 * time.Second)
	done := make(chan bool)
	go logProgress(scanId, clientKey, time.Now(), done, ticker, notification.GetPublisher(clientKey))
	defer func() {
		done <- true
		ticker.Stop()
	}()

	// Drive allows about 12,000 calls a minute per user; stay well below.
	throttler := rate.NewLimiter(20, 5)
	for _, folder := range known {
		recordFolder(folder, scanData)
	}
	var folders []*folderNode
	if driveScan.FolderId != "" {
		var err error
		if folders, err = walkFolder(driveService, driveScan, folderPath, throttler, scanData); err != nil {
			return err
		}
	} else {
		tree, err := listFolders(driveService, rootId, throttler)
		if err != nil {
			return err
		}
		for _, folder := range tree.folders {
			recordFolder(folder, scanData)
		}
		err = listFiles(driveService, driveScan.QueryString, throttler, func(file *drive.File) {
			if file.MimeType != folderMimeType {
				saveFile(file, tree.nodeFor(file.Parents), scanData)
			}
		})
		if err != nil {
			return err
		}
		folders = tree.created
	}
	for _, folder := range folders {
		scanData <- db.FileData{
			FileName:  folder.name,
			FilePath:  folder.path,
			FileId:    folder.id,
			IsDir:     true,
			Size:      uint(folder.size),
			FileCount: folder.files,
			ModTime:   parseTime(folder.modTime),
		}
	}
	return nil
}

// Where paths start: files in My Drive, and files whose folder the account
// can't see, typically ones shared with it, as the Drive UI shows them.
// Any file's path is the same whichever way it was scanned.
const (
	myDrivePath      = "My Drive"
	sharedWithMePath = "Shared with me"
)

// folderNode is a folder in a scan, with the totals of the files saved
// under it so far. Folders are tracked by ID, not path: a Drive folder's
// name can contain "/".
type folderNode struct {
	id, name, path, modTime string
	parent                  *folderNode // nil for a root
	size                    int64
	files                   uint
}

// add counts a file of size in n and every folder above it.
func (n *folderNode) add(size int64) {
	for ; n != nil; n = n.parent {
		n.size += size
		n.files++
	}
}

func (n *folderNode) child(folder *drive.File) *folderNode {
	return &folderNode{id: folder.Id, name: folder.Name, path: n.path + "/" + folder.Name,
		modTime: folder.ModifiedTime, parent: n}
}

// driveFolders is every folder the account can see, for a whole-Drive
// scan, and the nodes made for those that files were saved under.
type driveFolders struct {
	folders               map[string]*drive.File
	nodes                 map[string]*folderNode
	myDrive, sharedWithMe *folderNode // the roots, which get no row
	created               []*folderNode
}

// listFolders lists every folder the account can see. rootId is My
// Drive's.
func listFolders(driveService *drive.Service, rootId string, throttler *rate.Limiter) (*driveFolders, error) {
	tree := &driveFolders{
		folders:      map[string]*drive.File{},
		myDrive:      &folderNode{id: rootId, path: myDrivePath},
		sharedWithMe: &folderNode{path: sharedWithMePath},
	}
	tree.nodes = map[string]*folderNode{rootId: tree.myDrive}
	err := listFiles(driveService, fmt.Sprintf("mimeType = '%s'", folderMimeType), throttler, func(file *drive.File) {
		tree.folders[file.Id] = file
	})
	if err != nil {
		return nil, err
	}
	return tree, nil
}

// nodeFor is the folder of a file with the given parents. Drive files have
// one parent now; only the first is used.
func (t *driveFolders) nodeFor(parents []string) *folderNode {
	if len(parents) == 0 {
		return t.sharedWithMe
	}
	return t.node(parents[0], 0)
}

func (t *driveFolders) node(id string, depth int) *folderNode {
	if n, ok := t.nodes[id]; ok {
		return n
	}
	folder, ok := t.folders[id]
	// depth guards against a loop of parents.
	if !ok || depth > 100 {
		return t.sharedWithMe
	}
	parent := t.sharedWithMe
	if len(folder.Parents) > 0 {
		parent = t.node(folder.Parents[0], depth+1)
	}
	n := parent.child(folder)
	t.nodes[id] = n
	t.created = append(t.created, n)
	return n
}

// pathOfFolder is folder's full path: its parents' names, looked up one
// level at a time, up to My Drive (rootId), or up to the first parent the
// account can't see, which puts it under "Shared with me". It also returns
// the parents looked up, below My Drive.
func pathOfFolder(driveService *drive.Service, folder *drive.File, rootId string) (string, []*drive.File, error) {
	if folder.Id == rootId {
		return myDrivePath, nil, nil
	}
	path := folder.Name
	var above []*drive.File
	for depth := 0; depth < 100; depth++ { // guards against a loop of parents
		if len(folder.Parents) == 0 {
			break
		}
		if folder.Parents[0] == rootId {
			return myDrivePath + "/" + path, above, nil
		}
		parent, err := driveService.Files.Get(folder.Parents[0]).
			Fields("id, name, parents, mimeType, modifiedTime, ownedByMe, trashed").Do()
		var apiErr *googleapi.Error
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("failed to look up folder %s: %w", folder.Parents[0], err)
		}
		path = parent.Name + "/" + path
		above = append(above, parent)
		folder = parent
	}
	return sharedWithMePath + "/" + path, above, nil
}

// walkFolder saves the files in driveScan's folder that match its query,
// breadth first, and those in its subfolders when Recursive, and returns
// the subfolders walked. Every subfolder is walked, whether it matches the
// query or not: a folder owned by someone else can hold files the user
// owns. Trashed folders aren't walked, and shortcuts aren't followed.
// Paths start at folderPath.
func walkFolder(driveService *drive.Service, driveScan GDriveScan, folderPath string, throttler *rate.Limiter, scanData chan<- db.FileData) ([]*folderNode, error) {
	root := &folderNode{id: driveScan.FolderId, path: folderPath}
	queue := []*folderNode{root}
	var walked []*folderNode
	visited := map[string]bool{driveScan.FolderId: true}
	for len(queue) > 0 {
		folder := queue[0]
		queue = queue[1:]
		err := listFiles(driveService, folderQuery(folder.id, driveScan.QueryString), throttler, func(file *drive.File) {
			if file.MimeType != folderMimeType {
				saveFile(file, folder, scanData)
				return
			}
			recordFolder(file, scanData)
			if driveScan.Recursive && !file.Trashed && !visited[file.Id] {
				visited[file.Id] = true
				sub := folder.child(file)
				queue = append(queue, sub)
				walked = append(walked, sub)
			}
		})
		if err != nil {
			return nil, err
		}
	}
	return walked, nil
}

// folderQuery lists a folder's subfolders, and its files that match query.
func folderQuery(folderId string, query string) string {
	inFolder := fmt.Sprintf("'%s' in parents", folderId)
	if strings.TrimSpace(query) == "" {
		return inFolder
	}
	return fmt.Sprintf("%s and ((mimeType = '%s' and trashed = false) or (%s))", inFolder, folderMimeType, query)
}

// listFiles calls each for every file matching query, page by page.
func listFiles(driveService *drive.Service, query string, throttler *rate.Limiter, each func(*drive.File)) error {
	filesListCall := driveService.Files.List().PageSize(pageSize).Q(query).Fields(googleapi.Field(strings.Join(append(addPrefix(fields, "files/"), paginationFields...), ",")))
	for {
		if err := throttler.Wait(context.Background()); err != nil {
			return err
		}
		fileList, err := filesListCall.Do()
		if err != nil {
			return fmt.Errorf("failed to list drive files for query '%s': %w", query, err)
		}
		if fileList.IncompleteSearch {
			return errors.New("incomplete search from drive API")
		}
		for _, file := range fileList.Files {
			each(file)
		}
		if fileList.NextPageToken == "" {
			return nil
		}
		filesListCall = filesListCall.PageToken(fileList.NextPageToken)
	}
}

// saveFile saves a file found in folder, and counts it there.
func saveFile(file *drive.File, folder *folderNode, scanData chan<- db.FileData) {
	folder.add(file.Size)
	scanData <- db.FileData{
		FileName:  file.Name,
		FilePath:  folder.path + "/" + file.Name,
		FileId:    file.Id,
		Size:      uint(file.Size),
		ModTime:   parseTime(file.ModifiedTime),
		Md5Hash:   file.Md5Checksum,
		FileCount: 1,
		Drive:     driveItem(file),
	}
	counter_processed.Add(1)
}

// recordFolder saves a folder to the account's record only.
func recordFolder(folder *drive.File, scanData chan<- db.FileData) {
	scanData <- db.FileData{RecordOnly: true, Drive: driveItem(folder)}
}

// driveItem is file as the account's record keeps it.
func driveItem(file *drive.File) *db.DriveItem {
	item := &db.DriveItem{
		FileId:    file.Id,
		Name:      file.Name,
		IsDir:     file.MimeType == folderMimeType,
		MimeType:  file.MimeType,
		Size:      file.Size,
		Md5:       file.Md5Checksum,
		OwnedByMe: file.OwnedByMe,
		Trashed:   file.Trashed,
	}
	if len(file.Parents) > 0 {
		item.ParentId = file.Parents[0]
	}
	if file.ModifiedTime != "" {
		item.Modified = parseTime(file.ModifiedTime)
	}
	return item
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
	FolderId     string // a folder to scan, instead of the whole Drive
	Recursive    bool   // with FolderId: its subfolders too
	RefreshToken string
	ClientKey    string // a linked account, used instead of RefreshToken
}
