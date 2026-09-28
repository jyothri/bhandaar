package db

import (
	"reflect"
	"testing"
)

func TestScanDataKeepsCloudFileIds(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	scanId, err := LogStartScan("google_drive", alice)
	if err != nil {
		t.Fatal(err)
	}
	scanData := make(chan FileData, 2)
	scanData <- FileData{FileName: "q1.pdf", FilePath: "Desktop/Qns/q1.pdf", FileId: "1AbC", Size: 10, FileCount: 1}
	scanData <- FileData{FileName: "notes.txt", FilePath: "/home/alice/notes.txt", Size: 5, FileCount: 1}
	close(scanData)
	SaveStatToDb(scanId, scanData)

	rows, count, err := GetScanDataFromDb(scanId, 1)
	if err != nil || count != 2 {
		t.Fatalf("scan data = %d rows, %v; want 2", count, err)
	}
	// In path order.
	if got := rows[1]; got.Path != "Desktop/Qns/q1.pdf" || got.FileId == nil || *got.FileId != "1AbC" {
		t.Errorf("cloud file = %+v, want its folder path and file ID", got)
	}
	if got := rows[0]; got.FileId != nil {
		t.Errorf("local file has file_id %q, want NULL", *got.FileId)
	}
}

func TestScanDataPutsFoldersBeforeTheirContents(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	scanId, err := LogStartScan("google_drive", alice)
	if err != nil {
		t.Fatal(err)
	}
	scanData := make(chan FileData, 5)
	// Folder rows come last, as the Drive scan saves them.
	scanData <- FileData{FileName: "z.txt", FilePath: "My Drive/A/z.txt", FileId: "z", Size: 1, FileCount: 1}
	scanData <- FileData{FileName: "q.pdf", FilePath: "My Drive/A/B/q.pdf", FileId: "q", Size: 2, FileCount: 1}
	scanData <- FileData{FileName: "B", FilePath: "My Drive/A/B", FileId: "b", IsDir: true, Size: 2, FileCount: 1}
	scanData <- FileData{FileName: "Bc", FilePath: "My Drive/A/Bc", FileId: "bc", IsDir: true}
	close(scanData)
	SaveStatToDb(scanId, scanData)

	rows, _, err := GetScanDataFromDb(scanId, 1)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	want := []string{"My Drive/A/B", "My Drive/A/B/q.pdf", "My Drive/A/Bc", "My Drive/A/z.txt"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("order %v, want %v", paths, want)
	}
	if !rows[0].IsDir || rows[0].FileCount != 1 || rows[1].IsDir {
		t.Errorf("rows %+v", rows)
	}

	summary, err := GetScanSummary(scanId)
	if err != nil {
		t.Fatal(err)
	}
	// Folder rows don't count as items.
	if summary.ItemCount != 2 || summary.TotalBytes != 3 || summary.FolderCount != 2 || summary.ScanType != "google_drive" {
		t.Errorf("summary %+v, want 2 files, 3 bytes, 2 folders", summary)
	}
}

func TestGmailSummaryCountsMessages(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	scanId := scanOf(t, alice, "ali****ce@example.com", "k1", "is:unread")
	if _, err := db.Exec(`INSERT INTO messagemetadata (message_id, date, subject, size_estimate, scan_id)
		VALUES ('m1', '2026-09-01', 'old', 100, $1), ('m2', '2026-09-02', 'new', 50, $1)`, scanId); err != nil {
		t.Fatal(err)
	}

	summary, err := GetScanSummary(scanId)
	if err != nil || summary.ItemCount != 2 || summary.TotalBytes != 150 ||
		summary.Name != "ali****ce@example.com" || summary.ClientKey != "k1" || summary.SearchFilter != "is:unread" || summary.Status != "Running" {
		t.Errorf("summary %+v, %v", summary, err)
	}
	messages, count, err := GetMessageMetadataFromDb(scanId, 1)
	if err != nil || count != 2 || messages[0].Subject != "new" || messages[0].SizeEstimate != 50 {
		t.Errorf("messages %+v, %d, %v; want newest first", messages, count, err)
	}
}
