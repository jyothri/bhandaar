package db

import (
	"context"
	"crypto/sha256"
	"maps"
	"path"
	"strconv"
	"strings"
	"testing"
)

// agentDrivesDDL is the part of agentserver's schema (its migration 0003)
// that Browse reads.
const agentDrivesDDL = `
CREATE TABLE agent_agents (
  id        UUID PRIMARY KEY,
  user_id   BIGINT NOT NULL REFERENCES agent_users(id),
  hostname  TEXT
);
CREATE TABLE agent_drives (
  id                BIGSERIAL PRIMARY KEY,
  agent_id          UUID NOT NULL REFERENCES agent_agents(id),
  drive_id          TEXT NOT NULL,
  physical_drive_id BIGINT,
  last_synced_at    TIMESTAMPTZ
);
CREATE TABLE agent_files (
  drive_pk       BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  path_key       BYTEA NOT NULL,
  relative_path  TEXT NOT NULL,
  raw_path       BYTEA,
  size           BIGINT NOT NULL,
  mtime          TIMESTAMPTZ NOT NULL,
  content_hash   TEXT,
  status         TEXT NOT NULL,
  error_message  TEXT,
  row_version    BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, path_key)
);
CREATE TABLE agent_dir_listings (
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  entry_key       BYTEA NOT NULL,
  parent_key      BYTEA NOT NULL,
  relative_path   TEXT NOT NULL,
  child_name      TEXT NOT NULL,
  raw_path        BYTEA,
  raw_child_name  BYTEA,
  is_dir          BOOLEAN NOT NULL,
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, entry_key)
);
CREATE TABLE agent_scan_runs (
  drive_pk     BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  run_id       BIGINT NOT NULL,
  started_at   TIMESTAMPTZ NOT NULL,
  finished_at  TIMESTAMPTZ,
  files_seen   BIGINT,
  interrupted  BOOLEAN,
  row_version  BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, run_id)
);
CREATE TABLE agent_tombstones (
  drive_pk     BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  kind         TEXT NOT NULL,
  key          BYTEA NOT NULL,
  row_version  BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, kind, key)
);`

func sum(parts ...string) []byte {
	h := sha256.Sum256([]byte(strings.Join(parts, "")))
	return h[:]
}

// agentDrive adds a drive of an agent of user's on hostname, and returns
// its ID.
func agentDrive(t *testing.T, user int64, agent string, hostname string, driveId string) int64 {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO agent_agents (id, user_id, hostname) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, agent, user, hostname); err != nil {
		t.Fatal(err)
	}
	var pk int64
	if err := db.Get(&pk, `INSERT INTO agent_drives (agent_id, drive_id) VALUES ($1, $2) RETURNING id`,
		agent, driveId); err != nil {
		t.Fatal(err)
	}
	return pk
}

// agentFiles uploads files (path → size) to drive at version, with the
// directory listings above them, as agentserver keeps them.
func agentFiles(t *testing.T, drive int64, version int64, files map[string]int64) {
	t.Helper()
	listed := func(parent, child string, isDir bool) {
		entry := sum(parent, "\x00", child)
		if _, err := db.Exec(`INSERT INTO agent_dir_listings
				(drive_pk, entry_key, parent_key, relative_path, child_name, is_dir, row_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
			drive, entry, sum(parent), parent, child, isDir, version); err != nil {
			t.Fatal(err)
		}
	}
	for p, size := range files {
		status, message := "hashed", ""
		if size < 0 {
			size, status, message = 0, "error", "permission denied"
		}
		if _, err := db.Exec(`INSERT INTO agent_files
				(drive_pk, path_key, relative_path, size, mtime, status, error_message, row_version)
			VALUES ($1, $2, $3, $4, '2026-09-01T10:00:00Z', $5, NULLIF($6, ''), $7)
			ON CONFLICT (drive_pk, path_key) DO UPDATE SET size = EXCLUDED.size, row_version = EXCLUDED.row_version`,
			drive, sum(p), p, size, status, message, version); err != nil {
			t.Fatal(err)
		}
		isDir := false
		for dir := p; dir != "."; dir = path.Dir(dir) {
			parent := path.Dir(dir)
			if parent == "." {
				parent = ""
			}
			listed(parent, path.Base(dir), isDir)
			isDir = true
		}
	}
}

func browseMigrated(t *testing.T) {
	t.Helper()
	migrated(t)
	if _, err := db.Exec(agentDrivesDDL); err != nil {
		t.Fatal(err)
	}
}

// entries is a folder page's subfolders and files, in order, as
// "name files bytes" and "name size".
func entries(t *testing.T, page FolderPage, err error) []string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range page.Folders {
		got = append(got, f.Name+"/ "+itoa(f.Files)+" "+itoa(f.Bytes))
	}
	for _, f := range page.Files {
		size := "-"
		if f.Size != nil {
			size = itoa(*f.Size)
		}
		if f.Error != "" {
			size = f.Error
		}
		got = append(got, f.Name+" "+size)
	}
	return got
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func equal(a, b []string) bool {
	return strings.Join(a, "|") == strings.Join(b, "|")
}

func TestAgentChildrenAndTotals(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	drive := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate1")
	agentFiles(t, drive, 1, map[string]int64{
		"top.txt": 5, "Photos/a.jpg": 100, "Photos/2024/b.jpg": 200, "Docs/c.pdf": 50, "Docs/locked": -1,
	})
	// A folder with nothing scanned under it is still listed.
	if _, err := db.Exec(`INSERT INTO agent_dir_listings (drive_pk, entry_key, parent_key, relative_path, child_name, is_dir, row_version)
		VALUES ($1, $2, $3, '', 'Empty', true, 1)`, drive, sum("", "\x00", "Empty"), sum("")); err != nil {
		t.Fatal(err)
	}

	// Before the first build, totals are added up live.
	page, err := AgentChildren(drive, "", 1)
	want := []string{"Photos/ 2 300", "Docs/ 2 50", "Empty/ 0 0", "top.txt 5"}
	if got := entries(t, page, err); !equal(got, want) || !page.Updating {
		t.Errorf("root before the build = %v (updating %v), want %v", got, page.Updating, want)
	}
	if page.Totals != (FolderTotals{5, 355}) {
		t.Errorf("root's own totals before the build = %+v, want 5 files, 355 bytes", page.Totals)
	}
	if page, _ := AgentChildren(drive, "Photos", 1); page.Totals != (FolderTotals{2, 300}) {
		t.Errorf("Photos' own totals before the build = %+v", page.Totals)
	}

	checkTotals(context.Background())
	page, err = AgentChildren(drive, "", 1)
	if got := entries(t, page, err); !equal(got, want) || page.Updating {
		t.Errorf("root after the build = %v (updating %v), want %v", got, page.Updating, want)
	}
	page, err = AgentChildren(drive, "Photos", 1)
	if got := entries(t, page, err); !equal(got, []string{"2024/ 1 200", "a.jpg 100"}) {
		t.Errorf("Photos = %v", got)
	}
	if page.Totals != (FolderTotals{2, 300}) {
		t.Errorf("Photos' own totals = %+v", page.Totals)
	}
	if len(page.Path) != 1 || page.Path[0].Id != "Photos" {
		t.Errorf("Photos path = %+v", page.Path)
	}
	page, err = AgentChildren(drive, "Docs", 1)
	if got := entries(t, page, err); !equal(got, []string{"c.pdf 50", "locked permission denied"}) {
		t.Errorf("Docs = %v", got)
	}
	if _, err := AgentChildren(drive, "Nope", 1); err != ErrNotFound {
		t.Errorf("a folder that isn't there: err = %v, want ErrNotFound", err)
	}

	// An upload raises the version: the totals are out of date until the
	// next check, which rebuilds them.
	agentFiles(t, drive, 2, map[string]int64{"Photos/2024/new.jpg": 1000})
	page, err = AgentChildren(drive, "", 1)
	if got := entries(t, page, err); !equal(got, want) || !page.Updating {
		t.Errorf("root after an upload = %v (updating %v), want the cached %v", got, page.Updating, want)
	}
	checkTotals(context.Background())
	page, err = AgentChildren(drive, "", 1)
	want = []string{"Photos/ 3 1300", "Docs/ 2 50", "Empty/ 0 0", "top.txt 5"}
	if got := entries(t, page, err); !equal(got, want) || page.Updating {
		t.Errorf("root after the rebuild = %v (updating %v), want %v", got, page.Updating, want)
	}

	sources, err := BrowseSources(alice)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources = %+v, %v", sources, err)
	}
	if s := sources[0]; s.Kind != "agent" || s.Name != "seagate1 (optiplex)" || *s.Files != 6 || *s.Bytes != 1355 {
		t.Errorf("source = %+v", s)
	}
	if sources, _ := BrowseSources(addUser(t, "bob")); len(sources) != 0 {
		t.Errorf("bob's sources = %+v, want none", sources)
	}
	if owned, _ := AgentDriveOwnedBy(alice, drive); !owned {
		t.Error("alice doesn't own her drive")
	}
}

func TestAgentChildrenPages(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	drive := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate1")
	files := map[string]int64{"sub/x": 1}
	for i := 0; i < BrowsePageSize+10; i++ {
		files["f"+itoa(int64(i+1))] = int64(i + 1)
	}
	agentFiles(t, drive, 1, files)

	page, err := AgentChildren(drive, "", 1)
	if err != nil || len(page.Folders) != 1 || len(page.Files) != BrowsePageSize-1 || page.Entries != BrowsePageSize+11 {
		t.Fatalf("page 1: %d folders, %d files of %d, %v", len(page.Folders), len(page.Files), page.Entries, err)
	}
	if *page.Files[0].Size != BrowsePageSize+10 {
		t.Errorf("page 1 starts with size %d, want the largest", *page.Files[0].Size)
	}
	page, err = AgentChildren(drive, "", 2)
	if err != nil || len(page.Folders) != 0 || len(page.Files) != 11 || *page.Files[10].Size != 1 {
		t.Errorf("page 2: %d folders, %d files, %v", len(page.Folders), len(page.Files), err)
	}
}

func TestAgentDriveStatus(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	drive := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate1")
	status, err := GetAgentDriveStatus(drive)
	if err != nil || status.LastScan != nil || status.LastSyncedAt != nil {
		t.Fatalf("status of a new drive = %+v, %v", status, err)
	}
	if _, err := db.Exec(`UPDATE agent_drives SET last_synced_at = now(), physical_drive_id = 1 WHERE id = $1`, drive); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO agent_scan_runs (drive_pk, run_id, started_at, finished_at, files_seen, interrupted, row_version)
		VALUES ($1, 1, '2026-09-01', '2026-09-01 01:00', 10, false, 1),
			($1, 2, '2026-09-02', NULL, 3, true, 2)`, drive); err != nil {
		t.Fatal(err)
	}
	status, err = GetAgentDriveStatus(drive)
	if err != nil || status.LastScan == nil || *status.LastScan.FilesSeen != 3 || !*status.LastScan.Interrupted ||
		status.LastSyncedAt == nil || *status.PhysicalDrive != 1 {
		t.Errorf("status = %+v, %v; want the interrupted run 2", status, err)
	}
}

func TestDriveChildrenAndTotals(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	if _, err := LinkAccount(alice, GoogleLink{GoogleSub: "sub-a", DisplayName: "al***ce@example.com",
		Scope: "https://www.googleapis.com/auth/drive.metadata.readonly"}, "k1"); err != nil {
		t.Fatal(err)
	}
	// Nothing recorded yet.
	page, err := DriveChildren("k1", "", 1)
	if got := entries(t, page, err); len(got) != 0 {
		t.Errorf("roots before any scan = %v", got)
	}

	trashed := driveFile("binned", "A", "binned", 1000)
	trashed.Trashed = true
	// Its parent isn't visible: shared with the account.
	shared := driveFile("s1", "hidden-folder", "s1", 7)
	driveScan(t, alice, wholeDrive, false,
		dir("A", myDrive, "A"), dir("B", "A", "B"), dir("Empty", myDrive, "Empty"),
		driveFile("a1", "A", "a1", 10), driveFile("b1", "B", "b1", 20), driveFile("top", myDrive, "top", 1),
		trashed, shared)

	page, err = DriveChildren("k1", "", 1)
	if got := entries(t, page, err); !equal(got, []string{"My Drive/ 3 31", "Shared with me/ 1 7"}) || page.Updating {
		t.Errorf("roots = %v (updating %v)", got, page.Updating)
	}
	if page.Totals != (FolderTotals{4, 38}) {
		t.Errorf("the roots' totals = %+v, want the account's", page.Totals)
	}
	page, err = DriveChildren("k1", myDrive, 1)
	if got := entries(t, page, err); !equal(got, []string{"A/ 2 30", "Empty/ 0 0", "top 1"}) {
		t.Errorf("My Drive = %v", got)
	}
	page, err = DriveChildren("k1", "B", 1)
	if got := entries(t, page, err); !equal(got, []string{"b1 20"}) || page.Totals != (FolderTotals{1, 20}) {
		t.Errorf("B = %v, totals %+v", got, page.Totals)
	}
	var path []string
	for _, p := range page.Path {
		path = append(path, p.Name)
	}
	if !equal(path, []string{"My Drive", "A", "B"}) {
		t.Errorf("B's path = %v", path)
	}
	page, err = DriveChildren("k1", SharedWithMe, 1)
	if got := entries(t, page, err); !equal(got, []string{"s1 7"}) || page.Totals != (FolderTotals{1, 7}) {
		t.Errorf("Shared with me = %v, totals %+v", got, page.Totals)
	}
	if _, err := DriveChildren("k1", "a1", 1); err != ErrNotFound {
		t.Errorf("a file as a folder: err = %v, want ErrNotFound", err)
	}

	// Without cached totals, they're added up live.
	if _, err := db.Exec(`DELETE FROM browse_folder_totals; DELETE FROM browse_totals_state`); err != nil {
		t.Fatal(err)
	}
	page, err = DriveChildren("k1", myDrive, 1)
	if got := entries(t, page, err); !equal(got, []string{"A/ 2 30", "Empty/ 0 0", "top 1"}) || !page.Updating ||
		page.Totals != (FolderTotals{3, 31}) {
		t.Errorf("My Drive, live = %v (updating %v, totals %+v)", got, page.Updating, page.Totals)
	}
	checkTotals(context.Background())
	if state, _ := getTotalsState(driveSource("k1")); !state.Built {
		t.Error("the check didn't build the account's totals")
	}

	sources, err := BrowseSources(alice)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources = %+v, %v", sources, err)
	}
	d, g := sources[0].Services[ServiceDrive], sources[0].Services[ServiceGmail]
	if !d.Granted || *d.Files != 4 || *d.Bytes != 38 || d.UpdatedAt == nil || g.Granted || g.Files != nil {
		t.Errorf("services = drive %+v, gmail %+v", d, g)
	}
}

func TestAccountMessages(t *testing.T) {
	browseMigrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	scan1 := scanOf(t, alice, "ali****ce@example.com", "k1", "is:unread")
	scan2 := scanOf(t, alice, "ali****ce@example.com", "k1", "after:2026/09/01")
	other := scanOf(t, bob, "b**@example.com", "k2", "")
	if _, err := db.Exec(`INSERT INTO messagemetadata (message_id, date, subject, size_estimate, scan_id) VALUES
		('m1', '2026-09-01', 'old big', 900, $1), ('m2', '2026-09-03', 'new small', 5, $2),
		('m3', '2026-09-02', 'middle', 50, $2), ('b1', '2026-09-04', 'bobs', 1, $3)`, scan1, scan2, other); err != nil {
		t.Fatal(err)
	}
	subjects := func(p AccountMessagePage) []string {
		var s []string
		for _, m := range p.Messages {
			s = append(s, m.Subject)
		}
		return s
	}
	bySize, err := AccountMessages(alice, "k1", "size", 1)
	if err != nil || bySize.Total != 3 || !equal(subjects(bySize), []string{"old big", "middle", "new small"}) ||
		bySize.Messages[0].ScanId != scan1 {
		t.Errorf("by size = %v of %d, %v", subjects(bySize), bySize.Total, err)
	}
	byDate, _ := AccountMessages(alice, "k1", "date", 1)
	if !equal(subjects(byDate), []string{"new small", "middle", "old big"}) {
		t.Errorf("by date = %v", subjects(byDate))
	}
	if theirs, _ := AccountMessages(alice, "k2", "", 1); theirs.Total != 0 {
		t.Errorf("alice sees %d of bob's messages", theirs.Total)
	}
	totals, err := gmailServiceTotals(alice, "k1")
	if err != nil || *totals.Files != 3 || *totals.Bytes != 955 {
		t.Errorf("gmail totals = %+v, %v", totals, err)
	}
}

func TestDriveTotalsMatchAFullAddingUp(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	driveScan(t, alice, wholeDrive, false,
		dir("A", myDrive, "A"), dir("B", "A", "B"), dir("C", "B", "C"),
		driveFile("c1", "C", "c1", 3), driveFile("b1", "B", "b1", 2), driveFile("a1", "A", "a1", 1))
	cached, err := cachedTotals(driveSource("k1"), []string{"", myDrive, "A", "B", "C"})
	if err != nil {
		t.Fatal(err)
	}
	live, err := liveDriveTotals("k1")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]FolderTotals{"": {3, 6}, myDrive: {3, 6}, "A": {3, 6}, "B": {2, 5}, "C": {1, 3}}
	if !maps.Equal(cached, want) || !maps.Equal(live, want) {
		t.Errorf("cached %v, live %v, want %v", cached, live, want)
	}
}
