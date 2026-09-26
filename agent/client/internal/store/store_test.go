package store

import (
	"context"
	"sort"
	"testing"
	"time"
)

// These tests pin the store's behaviour before the remote-sync change feed
// (PR 3) changes its writers.

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

var ctx = context.Background()

func rec(drive, path string, size int64, hash string) FileRecord {
	return FileRecord{
		DriveID: drive, RelPath: path, Size: size, MTimeUnix: 1700000000, Mode: 0o644,
		QuickSig: "sig", ContentHash: hash, HashAlgo: "blake3", Status: StatusHashed,
		ScannedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

func TestOpenTwiceIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		st, err := Open(dir)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		st.Close()
	}
}

func TestDrives(t *testing.T) {
	st := open(t)
	if _, found, err := st.ExistingDriveRoot("d1"); err != nil || found {
		t.Fatalf("new drive: found=%v err=%v", found, err)
	}
	if _, err := st.DriveRoot("d1"); err == nil {
		t.Error("DriveRoot of an unknown drive should fail")
	}
	if err := st.UpsertDrive("d1", "/mnt/a", "Backup"); err != nil {
		t.Fatal(err)
	}
	// The backup root only takes effect the first time.
	if err := st.UpsertDrive("d1", "/mnt/b", "Other"); err != nil {
		t.Fatal(err)
	}
	root, _ := st.DriveRoot("d1")
	backup, _ := st.BackupRoot("d1")
	if root != "/mnt/b" || backup != "Backup" {
		t.Errorf("root=%q backup=%q, want /mnt/b and Backup", root, backup)
	}
	if err := st.SetBackupRoot("d1", "Jyo"); err != nil {
		t.Fatal(err)
	}
	if backup, _ := st.BackupRoot("d1"); backup != "Jyo" {
		t.Errorf("backup = %q after SetBackupRoot", backup)
	}
}

func TestUpsertAndGetFile(t *testing.T) {
	st := open(t)
	if got, err := st.GetFile("d1", "a.txt"); err != nil || got != nil {
		t.Fatalf("missing file: %v, %v", got, err)
	}
	r := rec("d1", "a.txt", 10, "h1")
	if err := st.UpsertFiles(ctx, []FileRecord{r}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetFile("d1", "a.txt")
	if err != nil || got == nil {
		t.Fatalf("GetFile: %v, %v", got, err)
	}
	if got.Size != 10 || got.ContentHash != "h1" || got.Status != StatusHashed || got.Mode != 0o644 ||
		got.MTimeUnix != 1700000000 || !got.ScannedAt.Equal(r.ScannedAt) || got.ComparisonStatus != "" {
		t.Errorf("GetFile = %+v", got)
	}
	ex, err := st.Existing("d1", "a.txt")
	if err != nil || ex == nil || ex.Size != 10 || ex.Status != StatusHashed {
		t.Errorf("Existing = %+v, %v", ex, err)
	}
}

func TestUpsertResetsComparison(t *testing.T) {
	st := open(t)
	if err := st.UpsertFiles(ctx, []FileRecord{rec("d1", "a.txt", 10, "h1")}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateComparisonStatuses(ctx, []ComparisonUpdate{{
		DriveID: "d1", RelPath: "a.txt", ComparisonStatus: ComparisonRelocated,
		ComparedAgainstDriveID: "d2", CounterpartRelativePath: "b.txt",
	}}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetFile("d1", "a.txt")
	if got.ComparisonStatus != ComparisonRelocated || got.CounterpartRelativePath != "b.txt" || got.ComparedAt.IsZero() {
		t.Fatalf("after compare: %+v", got)
	}
	if s, _ := st.FileComparisonStatus("d1", "a.txt"); s != ComparisonRelocated {
		t.Errorf("FileComparisonStatus = %q", s)
	}
	// A re-hash makes the comparison stale.
	if err := st.UpsertFiles(ctx, []FileRecord{rec("d1", "a.txt", 11, "h2")}); err != nil {
		t.Fatal(err)
	}
	got, _ = st.GetFile("d1", "a.txt")
	if got.ComparisonStatus != "" || got.ComparedAgainstDriveID != "" || got.CounterpartRelativePath != "" || !got.ComparedAt.IsZero() {
		t.Errorf("comparison not reset: %+v", got)
	}
	if got.Size != 11 || got.ContentHash != "h2" {
		t.Errorf("not updated: %+v", got)
	}
}

func TestListAndDeleteFiles(t *testing.T) {
	st := open(t)
	errRec := rec("d1", "bad.txt", 1, "")
	errRec.Status, errRec.ErrorMessage = StatusError, "read: input/output error"
	if err := st.UpsertFiles(ctx, []FileRecord{rec("d1", "a", 1, "h"), rec("d1", "b", 1, "h"), errRec, rec("d2", "a", 1, "h")}); err != nil {
		t.Fatal(err)
	}
	all, _ := st.ListFiles("d1", "")
	hashed, _ := st.ListFiles("d1", StatusHashed)
	if len(all) != 3 || len(hashed) != 2 {
		t.Errorf("ListFiles: all=%d hashed=%d", len(all), len(hashed))
	}
	if err := st.DeleteFiles(ctx, "d1", []string{"a", "bad.txt", "no-such"}); err != nil {
		t.Fatal(err)
	}
	paths, _ := st.ListFileRelativePaths("d1")
	if len(paths) != 1 || paths[0] != "b" {
		t.Errorf("after delete: %v", paths)
	}
	if other, _ := st.ListFileRelativePaths("d2"); len(other) != 1 {
		t.Errorf("other drive touched: %v", other)
	}
}

func children(t *testing.T, st *Store, drive, parent string) []string {
	t.Helper()
	cs, err := st.ListChildren(drive, parent)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		name := c.Name
		if c.IsDir {
			name += "/"
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A tree: "" has a/ and f; a has b/; a/b has x.
var tree = map[string][]DirChild{
	"":    {{Name: "a", IsDir: true}, {Name: "f"}},
	"a":   {{Name: "b", IsDir: true}},
	"a/b": {{Name: "x"}},
}

func TestSyncDirListingsWithoutDeleteStale(t *testing.T) {
	st := open(t)
	if err := st.SyncDirListings(ctx, "d1", tree, false); err != nil {
		t.Fatal(err)
	}
	// A later, partial observation only adds.
	if err := st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": {{Name: "g"}}}, false); err != nil {
		t.Fatal(err)
	}
	if got := children(t, st, "d1", ""); !equal(got, []string{"a/", "f", "g"}) {
		t.Errorf("children of root = %v", got)
	}
}

func TestSyncDirListingsDeleteStaleCascades(t *testing.T) {
	st := open(t)
	if err := st.SyncDirListings(ctx, "d1", tree, false); err != nil {
		t.Fatal(err)
	}
	// Directory a is gone: its row goes, and so do the listings under it.
	if err := st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": {{Name: "f"}}}, true); err != nil {
		t.Fatal(err)
	}
	if got := children(t, st, "d1", ""); !equal(got, []string{"f"}) {
		t.Errorf("root = %v", got)
	}
	if got := children(t, st, "d1", "a"); len(got) != 0 {
		t.Errorf("a's listing survived: %v", got)
	}
	if got := children(t, st, "d1", "a/b"); len(got) != 0 {
		t.Errorf("a/b's listing survived the cascade: %v", got)
	}
}

func TestSyncDirListingsKeepsFirstSeen(t *testing.T) {
	st := open(t)
	firstSeen := func() (time.Time, bool) {
		var ts time.Time
		var isDir bool
		if err := st.db.QueryRow(`SELECT first_seen_at, is_dir FROM dir_listings WHERE drive_id = 'd1' AND relative_path = '' AND child_name = 'c'`).
			Scan(&ts, &isDir); err != nil {
			t.Fatal(err)
		}
		return ts, isDir
	}
	if err := st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": {{Name: "c"}}}, true); err != nil {
		t.Fatal(err)
	}
	t1, _ := firstSeen()
	time.Sleep(5 * time.Millisecond)
	// c became a directory; first_seen_at stays.
	if err := st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": {{Name: "c", IsDir: true}}}, true); err != nil {
		t.Fatal(err)
	}
	t2, isDir := firstSeen()
	if !t1.Equal(t2) || !isDir {
		t.Errorf("first_seen %v -> %v, is_dir %v", t1, t2, isDir)
	}
}

func TestClearDrive(t *testing.T) {
	st := open(t)
	for _, d := range []string{"d1", "d2"} {
		if err := st.UpsertDrive(d, "/mnt/"+d, ""); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertFiles(ctx, []FileRecord{rec(d, "a", 1, "h")}); err != nil {
			t.Fatal(err)
		}
		if err := st.SyncDirListings(ctx, d, tree, false); err != nil {
			t.Fatal(err)
		}
		if err := st.UpsertFolderStatus(d, "a", "", FolderPartial, map[string]int{"common": 1}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.StartScanRun(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ClearDrive("d1"); err != nil {
		t.Fatal(err)
	}
	count := func(table, drive string) int {
		var n int
		if err := st.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE drive_id = ?`, drive).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, table := range []string{"files", "scan_runs", "dir_listings", "folder_status"} {
		if n := count(table, "d1"); n != 0 {
			t.Errorf("%s: %d rows left for d1", table, n)
		}
		if n := count(table, "d2"); n == 0 {
			t.Errorf("%s: d2's rows were cleared too", table)
		}
	}
	// The drives row stays, for UpsertDrive to update.
	if _, found, _ := st.ExistingDriveRoot("d1"); !found {
		t.Error("ClearDrive removed the drives row")
	}
}

func TestScanRuns(t *testing.T) {
	st := open(t)
	if err := st.UpsertDrive("d1", "/mnt/d1", ""); err != nil {
		t.Fatal(err)
	}
	completed := func() bool {
		var ts *time.Time
		if err := st.db.QueryRow(`SELECT last_scan_completed_at FROM drives WHERE drive_id = 'd1'`).Scan(&ts); err != nil {
			t.Fatal(err)
		}
		return ts != nil
	}
	id, err := st.StartScanRun("d1")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.FinishScanRun(id, "d1", 5, 100, true); err != nil {
		t.Fatal(err)
	}
	if completed() {
		t.Error("an interrupted run set last_scan_completed_at")
	}
	id2, _ := st.StartScanRun("d1")
	if id2 <= id {
		t.Errorf("run ids %d then %d", id, id2)
	}
	if err := st.FinishScanRun(id2, "d1", 5, 100, false); err != nil {
		t.Fatal(err)
	}
	if !completed() {
		t.Error("a finished run didn't set last_scan_completed_at")
	}
}

func TestFolderStatus(t *testing.T) {
	st := open(t)
	if fs, err := st.GetFolderStatus("d1", "a"); err != nil || fs != nil {
		t.Fatalf("missing: %v, %v", fs, err)
	}
	if err := st.UpsertFolderStatus("d1", "", "", ComparisonCommon, map[string]int{"common": 3}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFolderStatus("d1", "a/b", "a", FolderPartial, map[string]int{"common": 1, "missing": 2}); err != nil {
		t.Fatal(err)
	}
	root, _ := st.GetFolderStatus("d1", "")
	sub, _ := st.GetFolderStatus("d1", "a/b")
	if root == nil || !root.IsRoot || root.Counts["common"] != 3 {
		t.Errorf("root = %+v", root)
	}
	if sub == nil || sub.IsRoot || sub.ParentPath != "a" || sub.Status != FolderPartial || sub.Counts["missing"] != 2 {
		t.Errorf("sub = %+v", sub)
	}
}
