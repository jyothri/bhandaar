package db

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// A database from before the Picker API loses the Library API's tables.
func TestMigrateDropsPhotosLibraryTables(t *testing.T) {
	useTestDB(t)
	if _, err := db.Exec(agentserverDDL + `;
		CREATE TABLE photosmediaitem (id serial PRIMARY KEY, scan_id INT NOT NULL);
		CREATE TABLE photometadata (id serial PRIMARY KEY,
			photos_media_item_id INT NOT NULL REFERENCES photosmediaitem (id));
		CREATE TABLE videometadata (id serial PRIMARY KEY,
			photos_media_item_id INT NOT NULL REFERENCES photosmediaitem (id))`); err != nil {
		t.Fatal(err)
	}
	if err := migrateDB(); err != nil {
		t.Fatalf("migrateDB: %v", err)
	}
	var left []string
	if err := db.Select(&left, `SELECT table_name FROM information_schema.tables
		WHERE table_schema = current_schema()
			AND table_name IN ('photosmediaitem', 'photometadata', 'videometadata')`); err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Errorf("tables left after migrateDB: %v", left)
	}
}

func pickerSession(t *testing.T, userID int64, key string) PickerSession {
	t.Helper()
	s := PickerSession{SessionKey: key, PickerId: "picker-" + key, UserID: userID, ClientKey: "account-1",
		PickerUri: "https://photos.google.com/picker/" + key, PickBy: time.Now().Add(30 * time.Minute)}
	if err := SavePickerSession(s); err != nil {
		t.Fatalf("SavePickerSession(%s): %v", key, err)
	}
	return s
}

func TestPickerSessions(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")

	pickerSession(t, alice, "k1")
	if err := SavePickerSession(PickerSession{SessionKey: "k2", PickerId: "p2", UserID: alice, ClientKey: "a",
		PickerUri: "u", PickBy: time.Now()}); !errors.Is(err, ErrPickActive) {
		t.Errorf("a second pick while one waits: err = %v, want ErrPickActive", err)
	}
	if active, err := HasActivePick(alice); err != nil || !active {
		t.Errorf("HasActivePick(alice) = %v, %v; want true", active, err)
	}
	if active, err := HasActivePick(bob); err != nil || active {
		t.Errorf("HasActivePick(bob) = %v, %v; want false", active, err)
	}
	if s, err := ActivePickerSession(alice); err != nil || s.SessionKey != "k1" {
		t.Errorf("ActivePickerSession(alice) = %+v, %v; want k1", s, err)
	}
	if _, err := ActivePickerSession(bob); !errors.Is(err, ErrNotFound) {
		t.Errorf("ActivePickerSession(bob) err = %v, want ErrNotFound", err)
	}
	if _, err := GetPickerSession(bob, "k1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetPickerSession(bob, alice's) err = %v, want ErrNotFound", err)
	}

	// waiting → scanning, with its scan; a second move from waiting fails.
	scanId, err := LogStartScan("google_photos", alice)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		from, to string
		scanId   int
		want     bool
	}{
		{PickWaiting, PickScanning, 0, true},
		{PickWaiting, PickCancelled, 0, false},
		{PickScanning, PickScanning, scanId, true},
	} {
		if moved, err := MovePickerSession("k1", step.from, step.to, step.scanId); err != nil || moved != step.want {
			t.Errorf("MovePickerSession(%s → %s) = %v, %v; want %v", step.from, step.to, moved, err, step.want)
		}
	}
	s, err := GetPickerSession(alice, "k1")
	if err != nil || s.State != PickScanning || s.ScanId.Int64 != int64(scanId) || s.PickerId != "picker-k1" {
		t.Errorf("GetPickerSession(alice, k1) = %+v, %v", s, err)
	}

	// Still scanning, so still one at a time; once done, another may start.
	if err := SavePickerSession(PickerSession{SessionKey: "k2", PickerId: "p2", UserID: alice, ClientKey: "a",
		PickerUri: "u", PickBy: time.Now()}); !errors.Is(err, ErrPickActive) {
		t.Errorf("a second pick while one scans: err = %v, want ErrPickActive", err)
	}
	MovePickerSession("k1", PickScanning, PickDone, 0)
	pickerSession(t, alice, "k2")

	// Deleting the scan keeps the session, without its scan.
	if err := DeleteScan(scanId); err != nil {
		t.Fatal(err)
	}
	if s, _ := GetPickerSession(alice, "k1"); s.ScanId.Valid {
		t.Errorf("session k1 still has scan %d after it was deleted", s.ScanId.Int64)
	}
}

func TestEndPickerSessions(t *testing.T) {
	migrated(t)
	alice, bob, carol := addUser(t, "alice"), addUser(t, "bob"), addUser(t, "carol")
	pickerSession(t, alice, "waiting")
	pickerSession(t, bob, "scanning")
	MovePickerSession("scanning", PickWaiting, PickScanning, 0)
	pickerSession(t, carol, "cancelled")
	MovePickerSession("cancelled", PickWaiting, PickCancelled, 0)

	if err := endPickerSessions(); err != nil {
		t.Fatal(err)
	}
	for user, want := range map[int64]string{alice: PickExpired, bob: PickDone, carol: PickCancelled} {
		for _, key := range []string{"waiting", "scanning", "cancelled"} {
			if s, err := GetPickerSession(user, key); err == nil && s.State != want {
				t.Errorf("%s after a restart: state = %s, want %s", key, s.State, want)
			}
		}
	}
}

func TestPickedItemsAndSummary(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	scanId, err := LogStartScan("google_photos", alice)
	if err != nil {
		t.Fatal(err)
	}
	size := func(n int64) *int64 { return &n }
	var items []PickedItem
	for i := range 23 {
		items = append(items, PickedItem{MediaItemId: fmt.Sprintf("item-%02d", i), MediaType: "PHOTO",
			Filename: fmt.Sprintf("IMG_%02d.jpg", i), Size: size(100), SizeSource: SizeFromHead})
	}
	items[3].Size, items[3].SizeSource = nil, SizeUnavailable
	items = append(items, items[0]) // listed twice; saved once
	if err := SavePickedItems(scanId, items); err != nil {
		t.Fatal(err)
	}

	page, err := PickedItems(scanId, 3)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 23 || page.PageSize != 10 || len(page.Items) != 3 || page.Items[0].MediaItemId != "item-20" {
		t.Errorf("page 3 = %+v, want 3 of 23 items from item-20", page)
	}
	summary, err := GetScanSummary(scanId)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ItemCount != 23 || summary.TotalBytes != 2200 || summary.UnsizedCount != 1 || summary.FolderCount != 0 {
		t.Errorf("summary = %+v, want 23 items, 2200 bytes, 1 unsized", summary)
	}

	if err := DeleteScan(scanId); err != nil {
		t.Fatal(err)
	}
	if page, err := PickedItems(scanId, 1); err != nil || page.Total != 0 {
		t.Errorf("after DeleteScan: %d items, %v; want none", page.Total, err)
	}
}
