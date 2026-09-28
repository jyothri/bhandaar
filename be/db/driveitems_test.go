package db

import (
	"maps"
	"testing"
)

const myDrive = "my-drive-root-01"

// driveScan saves items as one Drive scan of account k1 and returns the
// scan's ID. fail marks the scan failed before it ends, as the collector
// does.
func driveScan(t *testing.T, user int64, record DriveRecord, fail bool, items ...DriveItem) int {
	t.Helper()
	scanId, err := LogStartScan("google_drive", user)
	if err != nil {
		t.Fatal(err)
	}
	record.ClientKey, record.MyDriveId = "k1", myDrive
	scanData := make(chan FileData, len(items))
	for i := range items {
		item := items[i]
		scanData <- FileData{FileName: item.Name, FileId: item.FileId, IsDir: item.IsDir,
			RecordOnly: item.IsDir, Drive: &item}
	}
	if fail {
		MarkScanFailed(scanId, "boom")
	}
	close(scanData)
	SaveDriveScanToDb(scanId, &record, scanData)
	return scanId
}

// recorded returns account k1's record, by file ID, as "<parent>/<name>".
func recorded(t *testing.T) map[string]string {
	t.Helper()
	rows := []struct {
		FileId string `db:"file_id"`
		Parent string `db:"parent"`
		Name   string `db:"name"`
	}{}
	if err := db.Select(&rows, `SELECT file_id, COALESCE(parent_id, '') AS parent, name
		FROM drive_items WHERE client_key = 'k1'`); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.FileId] = r.Parent + "/" + r.Name
	}
	return got
}

func dir(id, parent, name string) DriveItem {
	return DriveItem{FileId: id, ParentId: parent, Name: name, IsDir: true, MimeType: "application/vnd.google-apps.folder", OwnedByMe: true}
}

func driveFile(id, parent, name string, size int64) DriveItem {
	return DriveItem{FileId: id, ParentId: parent, Name: name, Size: size, MimeType: "text/plain", OwnedByMe: true}
}

var wholeDrive = DriveRecord{Recursive: true, SawAll: true}

func TestDriveRecordAddsUpdatesRenamesAndMoves(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	driveScan(t, alice, wholeDrive, false,
		dir("A", myDrive, "A"), dir("B", myDrive, "B"),
		driveFile("f1", "A", "one.txt", 10), driveFile("f2", "A", "two.txt", 20))
	want := map[string]string{"A": myDrive + "/A", "B": myDrive + "/B", "f1": "A/one.txt", "f2": "A/two.txt"}
	if got := recorded(t); !maps.Equal(got, want) {
		t.Fatalf("after the first scan: %v, want %v", got, want)
	}

	// f1 renamed, f2 moved to B and grown, folder A renamed.
	scan2 := driveScan(t, alice, wholeDrive, false,
		dir("A", myDrive, "A2"), dir("B", myDrive, "B"),
		driveFile("f1", "A", "uno.txt", 10), driveFile("f2", "B", "two.txt", 25))
	want = map[string]string{"A": myDrive + "/A2", "B": myDrive + "/B", "f1": "A/uno.txt", "f2": "B/two.txt"}
	if got := recorded(t); !maps.Equal(got, want) {
		t.Errorf("after the second scan: %v, want %v", got, want)
	}
	var size int64
	var seen int
	if err := db.QueryRow(`SELECT size, last_seen_scan FROM drive_items WHERE file_id = 'f2'`).Scan(&size, &seen); err != nil || size != 25 || seen != scan2 {
		t.Errorf("f2: size %d, last seen by %d, %v; want 25, %d", size, seen, err, scan2)
	}
	// The scan's results are there too, but not the folders it recorded.
	if _, count, _ := GetScanDataFromDb(scan2, 1); count != 2 {
		t.Errorf("scan data has %d rows, want the 2 files", count)
	}
	var myDriveId string
	if err := db.Get(&myDriveId, `SELECT my_drive_id FROM drive_accounts WHERE client_key = 'k1'`); err != nil || myDriveId != myDrive {
		t.Errorf("My Drive = %q, %v", myDriveId, err)
	}
}

func TestDriveRecordDeletesWhatAnUnfilteredScanDidntSee(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	// A → (a1, Sub → s1), B → b1, Gone → Deeper → g1; plus a trashed file
	// and one someone else owns.
	shared := driveFile("theirs", "A", "theirs.txt", 1)
	shared.OwnedByMe = false
	trashed := driveFile("binned", "A", "binned.txt", 1)
	trashed.Trashed = true
	all := []DriveItem{
		dir("A", myDrive, "A"), dir("Sub", "A", "Sub"), dir("B", myDrive, "B"),
		dir("Gone", "A", "Gone"), dir("Deeper", "Gone", "Deeper"),
		driveFile("a1", "A", "a1", 1), driveFile("s1", "Sub", "s1", 1), driveFile("b1", "B", "b1", 1),
		driveFile("g1", "Deeper", "g1", 1), shared, trashed,
	}
	driveScan(t, alice, DriveRecord{SawAll: false}, false, all...)
	before := recorded(t)

	has := func(ids ...string) map[string]string {
		m := map[string]string{}
		for _, id := range ids {
			m[id] = before[id]
		}
		return m
	}

	// A filtered scan of A that sees only a1 deletes nothing.
	driveScan(t, alice, DriveRecord{FolderId: "A", Recursive: true}, false, driveFile("a1", "A", "a1", 1))
	if got := recorded(t); !maps.Equal(got, before) {
		t.Fatalf("after a filtered scan: %v, want %v", got, before)
	}
	// Nor does an unfiltered one that fails.
	driveScan(t, alice, DriveRecord{FolderId: "A", Recursive: true, SawAll: true}, true, driveFile("a1", "A", "a1", 1))
	if got := recorded(t); !maps.Equal(got, before) {
		t.Fatalf("after a failed scan: %v, want %v", got, before)
	}

	// A's own files only, without subfolders: s1 and g1 are below that.
	driveScan(t, alice, DriveRecord{FolderId: "A", SawAll: true, OwnedOnly: true}, false,
		dir("Sub", "A", "Sub"), dir("Gone", "A", "Gone"))
	// a1 is gone; theirs isn't owned, so an owned-only scan couldn't see it;
	// binned is trashed.
	want := has("A", "Sub", "B", "Gone", "Deeper", "s1", "b1", "g1", "theirs", "binned")
	if got := recorded(t); !maps.Equal(got, want) {
		t.Fatalf("after a scan of A only: %v, want %v", got, want)
	}

	// A and its subfolders, where Gone has been deleted: its files go, then
	// it and Deeper, left empty. B is outside the scope.
	driveScan(t, alice, DriveRecord{FolderId: "A", Recursive: true, SawAll: true}, false,
		dir("Sub", "A", "Sub"), driveFile("s1", "Sub", "s1", 1), shared)
	want = has("A", "Sub", "B", "s1", "b1", "theirs", "binned")
	if got := recorded(t); !maps.Equal(got, want) {
		t.Fatalf("after a scan of A and subfolders: %v, want %v", got, want)
	}

	// The whole Drive, where only B is left. The query leaves out trashed
	// files, so binned stays, and so A, which isn't empty.
	driveScan(t, alice, wholeDrive, false, dir("B", myDrive, "B"), driveFile("b1", "B", "b1", 1))
	want = has("A", "B", "b1", "binned")
	if got := recorded(t); !maps.Equal(got, want) {
		t.Errorf("after a whole-Drive scan: %v, want %v", got, want)
	}
}

func TestDriveRecordSkipsMyDrive(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	driveScan(t, alice, wholeDrive, false, dir(myDrive, "", "My Drive"), driveFile("f", myDrive, "f", 1))
	if got := recorded(t); !maps.Equal(got, map[string]string{"f": myDrive + "/f"}) {
		t.Errorf("record %v, want only f", got)
	}
}
