package collect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

// fakeDrive serves a fake Drive API over a tree of files, keyed by parent
// ID. files.list answers a query's "'<id>' in parents" with that folder's
// children, or with every file when the query names no parent; it doesn't
// evaluate the rest of the query, which it records. Lists are paged two
// files at a time.
type fakeDrive struct {
	t        *testing.T
	children map[string][]*drive.File
	mu       sync.Mutex
	queries  []string
}

const (
	rootId  = "root-folder-0001"
	subId   = "sub-folder-00001"
	deepId  = "deep-folder-0001"
	trashId = "trash-folder-001"
)

func folder(id, name string) *drive.File {
	return &drive.File{Id: id, Name: name, MimeType: folderMimeType}
}

func file(id string, size int64) *drive.File {
	return &drive.File{Id: id, Name: id + ".txt", MimeType: "text/plain", Size: size,
		ModifiedTime: "2026-09-01T10:00:00Z", Md5Checksum: "md5-" + id}
}

// testTree is root → a, sub → (b, deep → c, a loop back to root), a trashed
// folder → x, and a shortcut; plus s, and a folder Proj → p, in a folder
// the account can't see.
func testTree() map[string][]*drive.File {
	trashed := folder(trashId, "Old")
	trashed.Trashed = true
	return map[string][]*drive.File{
		rootId: {file("a", 10), folder(subId, "Sub"), trashed,
			{Id: "shortcut-1", Name: "Link", MimeType: "application/vnd.google-apps.shortcut"}},
		subId:              {file("b", 20), folder(deepId, "Deep")},
		deepId:             {file("c", 30), folder(rootId, "Loop")},
		trashId:            {file("x", 40)},
		"hidden-folder-01": {file("s", 50), folder("proj-folder-0001", "Proj")},
		"proj-folder-0001": {file("p", 60)},
	}
}

var parentPattern = regexp.MustCompile(`^'([^']+)' in parents`)

func newFakeDrive(t *testing.T, children map[string][]*drive.File) (*fakeDrive, *drive.Service) {
	t.Helper()
	f := &fakeDrive{t: t, children: children}
	mux := http.NewServeMux()
	mux.HandleFunc("/files", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		f.mu.Lock()
		f.queries = append(f.queries, q)
		f.mu.Unlock()
		// Each file with its parent, as Drive gives it.
		withParent := func(parent string) []*drive.File {
			kids := []*drive.File{}
			for _, kid := range f.children[parent] {
				copied := *kid
				copied.Parents = []string{parent}
				kids = append(kids, &copied)
			}
			return kids
		}
		var all []*drive.File
		if m := parentPattern.FindStringSubmatch(q); m != nil {
			all = withParent(m[1])
		} else {
			// Every file, in a stable order, so pages don't overlap.
			parents := slices.Sorted(maps.Keys(f.children))
			for _, parent := range parents {
				all = append(all, withParent(parent)...)
			}
		}
		start := 0
		if token := r.URL.Query().Get("pageToken"); token != "" {
			json.Unmarshal([]byte(token), &start)
		}
		end := min(start+2, len(all))
		list := drive.FileList{Files: all[start:end]}
		if end < len(all) {
			next, _ := json.Marshal(end)
			list.NextPageToken = string(next)
		}
		json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Path[len("/files/"):]
		if id == "root" { // My Drive
			json.NewEncoder(w).Encode(drive.File{Id: rootId})
			return
		}
		for parent, kids := range f.children {
			for _, kid := range kids {
				// The loop entry reuses root's ID; root has no parent.
				if kid.Id == id && id != rootId {
					copied := *kid
					copied.Parents = []string{parent}
					json.NewEncoder(w).Encode(copied)
					return
				}
			}
		}
		if id == rootId {
			json.NewEncoder(w).Encode(folder(rootId, "Root"))
			return
		}
		http.Error(w, `{"error":{"code":404,"message":"File not found"}}`, http.StatusNotFound)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	svc, err := drive.NewService(context.Background(),
		option.WithEndpoint(server.URL+"/"), option.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	return f, svc
}

// runDriveScan runs startCloudDrive, with "Desktop" as a folder scan's
// folder, and returns the paths of the files saved, by file ID.
func runDriveScan(t *testing.T, svc *drive.Service, scan GDriveScan) map[string]string {
	t.Helper()
	files, _ := runDriveScanWithFolders(t, svc, scan)
	return files
}

// runDriveScanWithFolders also returns the folder rows saved, by path, as
// "<file ID> <size> <file count>".
func runDriveScanWithFolders(t *testing.T, svc *drive.Service, scan GDriveScan) (map[string]string, map[string]string) {
	t.Helper()
	files, folders := map[string]string{}, map[string]string{}
	for _, fd := range driveScanRows(t, svc, scan, nil) {
		if fd.RecordOnly {
			continue
		}
		if fd.IsDir {
			folders[fd.FilePath] = fmt.Sprintf("%s %d %d", fd.FileId, fd.Size, fd.FileCount)
		} else {
			files[fd.FileId] = fd.FilePath
		}
	}
	return files, folders
}

// driveScanRows runs startCloudDrive, with "Desktop" as a folder scan's
// folder and known as the folders known before, and returns every row.
func driveScanRows(t *testing.T, svc *drive.Service, scan GDriveScan, known []*drive.File) []db.FileData {
	t.Helper()
	scanData := make(chan db.FileData, 100)
	if err := startCloudDrive(svc, 1, scan, "Desktop", "drive-test-"+t.Name(), rootId, known, scanData); err != nil {
		t.Fatalf("startCloudDrive: %v", err)
	}
	close(scanData)
	var rows []db.FileData
	for fd := range scanData {
		rows = append(rows, fd)
	}
	return rows
}

func TestDriveWholeDriveSkipsFolders(t *testing.T) {
	fake, svc := newFakeDrive(t, testTree())

	got := runDriveScan(t, svc, GDriveScan{QueryString: "trashed = false"})

	// The fake ignores the query, so the trashed folder's file shows up too.
	want := map[string]string{
		"a":          "My Drive/a.txt",
		"b":          "My Drive/Sub/b.txt",
		"c":          "My Drive/Sub/Deep/c.txt",
		"x":          "My Drive/Old/x.txt",
		"shortcut-1": "My Drive/Link",
		"s":          "Shared with me/s.txt",
		"p":          "Shared with me/Proj/p.txt",
	}
	if !maps.Equal(got, want) {
		t.Errorf("saved %v, want %v", got, want)
	}
	// One listing of the folders, for their paths, then the files.
	wantQueries := []string{"mimeType = 'application/vnd.google-apps.folder'", "trashed = false"}
	if got := slices.Compact(slices.Clone(fake.queries)); !slices.Equal(got, wantQueries) {
		t.Errorf("queries %q, want %q", got, wantQueries)
	}
}

func TestDriveFolderScanWalksSubfoldersWhenRecursive(t *testing.T) {
	fake, svc := newFakeDrive(t, testTree())

	got := runDriveScan(t, svc, GDriveScan{FolderId: rootId, Recursive: true, QueryString: "'me' in owners"})

	// Not x: its folder is trashed. The loop back to root is walked once.
	want := map[string]string{
		"a":          "Desktop/a.txt",
		"b":          "Desktop/Sub/b.txt",
		"c":          "Desktop/Sub/Deep/c.txt",
		"shortcut-1": "Desktop/Link",
	}
	if !maps.Equal(got, want) {
		t.Errorf("saved %v, want %v", got, want)
	}
	wantQueries := []string{
		folderQuery(rootId, "'me' in owners"),
		folderQuery(subId, "'me' in owners"),
		folderQuery(deepId, "'me' in owners"),
	}
	// Each folder once, in breadth-first order; a folder with more than a
	// page of files is listed once per page.
	if got := slices.Compact(slices.Clone(fake.queries)); !slices.Equal(got, wantQueries) {
		t.Errorf("queries %q, want %q", got, wantQueries)
	}
}

func TestDriveFolderScanWithoutSubfolders(t *testing.T) {
	_, svc := newFakeDrive(t, testTree())

	got := runDriveScan(t, svc, GDriveScan{FolderId: rootId})

	if want := map[string]string{"a": "Desktop/a.txt", "shortcut-1": "Desktop/Link"}; !maps.Equal(got, want) {
		t.Errorf("saved %v, want %v", got, want)
	}
}

func TestFolderQuery(t *testing.T) {
	if got := folderQuery(rootId, ""); got != "'"+rootId+"' in parents" {
		t.Errorf("no filter: %q", got)
	}
	want := "'" + rootId + "' in parents and ((mimeType = 'application/vnd.google-apps.folder' and trashed = false) or (trashed = false and 'me' in owners))"
	if got := folderQuery(rootId, "trashed = false and 'me' in owners"); got != want {
		t.Errorf("with filter:\n got %q\nwant %q", got, want)
	}
}

func TestCheckFolder(t *testing.T) {
	_, svc := newFakeDrive(t, testTree())

	if f, err := checkFolder(svc, subId); err != nil || f.Name != "Sub" {
		t.Errorf("checkFolder(sub) = %+v, %v", f, err)
	}
	bad := map[string]string{
		"missing-folder-1": "Folder not found",
		trashId:            "in the trash",
		"a":                "isn't a Google Drive folder ID", // too short to be an ID
		"x' or name != '":  "isn't a Google Drive folder ID",
	}
	for id, want := range bad {
		_, err := checkFolder(svc, id)
		var requestErr *RequestError
		if !errors.As(err, &requestErr) || !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(requestErr.Message) {
			t.Errorf("checkFolder(%q) = %v, want a RequestError saying %q", id, err, want)
		}
	}

	// A file, not a folder.
	tree := testTree()
	tree[rootId] = append(tree[rootId], file("a-file-id-0001", 1))
	_, svc = newFakeDrive(t, tree)
	var requestErr *RequestError
	if _, err := checkFolder(svc, "a-file-id-0001"); !errors.As(err, &requestErr) {
		t.Errorf("checkFolder(file) = %v, want a RequestError", err)
	}
}

func TestDriveScanPublishesProgress(t *testing.T) {
	_, svc := newFakeDrive(t, testTree())
	clientKey := "drive-test-" + t.Name()
	events, unsubscribe := notification.Subscribe(clientKey)
	defer unsubscribe()

	runDriveScan(t, svc, GDriveScan{FolderId: rootId, Recursive: true})

	select {
	case progress := <-events:
		if progress.ScanId != 1 || progress.ClientKey != clientKey || progress.ProcessedCount != 4 {
			t.Errorf("progress = %+v, want scan 1 of %s with 4 files", progress, clientKey)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no progress published")
	}
}

func TestPathOfFolder(t *testing.T) {
	_, svc := newFakeDrive(t, testTree())
	want := map[string]string{
		rootId:             "My Drive",
		subId:              "My Drive/Sub",
		deepId:             "My Drive/Sub/Deep",
		"proj-folder-0001": "Shared with me/Proj",
	}
	for id, path := range want {
		folder, err := checkFolder(svc, id)
		if err != nil {
			t.Fatalf("checkFolder(%s): %v", id, err)
		}
		if got, _, err := pathOfFolder(svc, folder, rootId); err != nil || got != path {
			t.Errorf("pathOfFolder(%s) = %q, %v; want %q", id, got, err, path)
		}
	}
}

func TestDriveFolderScanSavesFolderTotals(t *testing.T) {
	_, svc := newFakeDrive(t, testTree())

	_, folders := runDriveScanWithFolders(t, svc, GDriveScan{FolderId: rootId, Recursive: true})

	// Like local scans: every folder below the one scanned, with everything
	// under it; not the scanned folder itself. Sub holds b (20) and, in
	// Deep, c (30). The trashed folder isn't walked.
	want := map[string]string{
		"Desktop/Sub":      subId + " 50 2",
		"Desktop/Sub/Deep": deepId + " 30 1",
	}
	if !maps.Equal(folders, want) {
		t.Errorf("folders %v, want %v", folders, want)
	}

	// Without subfolders, there are none.
	if _, folders := runDriveScanWithFolders(t, svc, GDriveScan{FolderId: rootId}); len(folders) != 0 {
		t.Errorf("folders %v, want none", folders)
	}
}

func TestDriveWholeDriveSavesFolderTotals(t *testing.T) {
	_, svc := newFakeDrive(t, testTree())

	_, folders := runDriveScanWithFolders(t, svc, GDriveScan{QueryString: "trashed = false"})

	// Folders with files under them; not the roots, My Drive and Shared
	// with me. (The fake ignores the query, so the trashed Old counts.)
	want := map[string]string{
		"My Drive/Sub":        subId + " 50 2",
		"My Drive/Sub/Deep":   deepId + " 30 1",
		"My Drive/Old":        trashId + " 40 1",
		"Shared with me/Proj": "proj-folder-0001 60 1",
	}
	if !maps.Equal(folders, want) {
		t.Errorf("folders %v, want %v", folders, want)
	}
}

func TestFolderNamesMayContainSlashes(t *testing.T) {
	tree := testTree()
	tree[rootId] = append(tree[rootId], folder("slash-folder-001", "2024/25"))
	tree["slash-folder-001"] = []*drive.File{file("t", 5)}
	_, svc := newFakeDrive(t, tree)

	files, folders := runDriveScanWithFolders(t, svc, GDriveScan{FolderId: rootId, Recursive: true})

	if files["t"] != "Desktop/2024/25/t.txt" || folders["Desktop/2024/25"] != "slash-folder-001 5 1" {
		t.Errorf("file t at %q, folder rows %v", files["t"], folders)
	}
	// Its total isn't mistaken for a folder "2024".
	if _, ok := folders["Desktop/2024"]; ok {
		t.Errorf("a row for a folder that doesn't exist: %v", folders)
	}
}

func TestPathOfFolderReturnsTheFoldersAbove(t *testing.T) {
	_, svc := newFakeDrive(t, testTree())
	folder, err := checkFolder(svc, deepId)
	if err != nil {
		t.Fatal(err)
	}
	_, above, err := pathOfFolder(svc, folder, rootId)
	if err != nil || len(above) != 1 || above[0].Id != subId || above[0].Parents[0] != rootId {
		t.Errorf("above = %+v, %v; want Sub, in My Drive", above, err)
	}
}

// recordItems returns the Drive items of rows, by file ID, as
// "<parent> <name> <dir|file> <size> <record-only>".
func recordItems(rows []db.FileData) map[string]string {
	items := map[string]string{}
	for _, fd := range rows {
		if fd.Drive == nil {
			continue
		}
		kind := "file"
		if fd.Drive.IsDir {
			kind = "dir"
		}
		items[fd.Drive.FileId] = fmt.Sprintf("%s %s %s %d %v", fd.Drive.ParentId, fd.Drive.Name, kind, fd.Drive.Size, fd.RecordOnly)
	}
	return items
}

func TestDriveFolderScanRecordsFilesAndFolders(t *testing.T) {
	tree := testTree()
	tree[deepId] = []*drive.File{file("c", 30)} // without the loop back to root
	_, svc := newFakeDrive(t, tree)
	above := folder("above-folder-001", "Above")
	above.Parents = []string{rootId}
	scanned := folder(subId, "Sub")
	scanned.Parents = []string{"above-folder-001"}

	rows := driveScanRows(t, svc, GDriveScan{FolderId: subId, Recursive: true}, []*drive.File{above, scanned})

	// Files go to the results and the record; every folder listed, and the
	// known ones, to the record only (the walk's folder rows carry no item).
	want := map[string]string{
		"above-folder-001": rootId + " Above dir 0 true",
		subId:              "above-folder-001 Sub dir 0 true",
		"b":                subId + " b.txt file 20 false",
		deepId:             subId + " Deep dir 0 true",
		"c":                deepId + " c.txt file 30 false",
	}
	if got := recordItems(rows); !maps.Equal(got, want) {
		t.Errorf("record items\n got %v\nwant %v", got, want)
	}
}

func TestDriveWholeDriveRecordsEveryFolder(t *testing.T) {
	tree := testTree()
	// A folder with nothing in it is still recorded.
	tree[rootId] = append(tree[rootId], folder("empty-folder-01", "Empty"))
	_, svc := newFakeDrive(t, tree)

	items := recordItems(driveScanRows(t, svc, GDriveScan{QueryString: "trashed = false"}, nil))

	for id, want := range map[string]string{
		"empty-folder-01": rootId + " Empty dir 0 true",
		trashId:           rootId + " Old dir 0 true",
		"a":               rootId + " a.txt file 10 false",
		"s":               "hidden-folder-01 s.txt file 50 false",
	} {
		if items[id] != want {
			t.Errorf("item %s = %q, want %q", id, items[id], want)
		}
	}
}

func TestDriveRecord(t *testing.T) {
	folderQ := "mimeType != 'application/vnd.google-apps.folder'"
	cases := []struct {
		query             string
		sawAll, ownedOnly bool
	}{
		{folderQ + " and trashed = false", true, false},
		{folderQ + " and trashed = false and 'me' in owners", true, true},
		{folderQ + " and trashed = false and mimeType contains 'image/'", false, false},
		{folderQ, false, false},
		{"", false, false},
	}
	for _, c := range cases {
		r := driveRecord(GDriveScan{QueryString: c.query, FolderId: subId}, "k1", rootId)
		if r.SawAll != c.sawAll || r.OwnedOnly != c.ownedOnly || r.FolderId != subId || r.MyDriveId != rootId || r.Recursive {
			t.Errorf("driveRecord(%q) = %+v", c.query, r)
		}
	}
	if r := driveRecord(GDriveScan{}, "k1", rootId); !r.Recursive {
		t.Errorf("a whole-Drive scan's record isn't recursive: %+v", r)
	}
	if r := driveRecord(GDriveScan{RefreshToken: "rt"}, "", rootId); r != nil {
		t.Errorf("a scan without a linked account has record %+v, want nil", r)
	}
}
