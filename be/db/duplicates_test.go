package db

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"testing"
	"time"
)

// dupFile is an agent file for the duplicates tests: its key is md5 when
// set, else blake3; status "error" for a file the agent couldn't read.
type dupFile struct {
	path   string
	size   int64
	md5    string
	blake3 string
	status string
	mtime  string
}

func dupAgentFiles(t *testing.T, drive int64, files ...dupFile) {
	t.Helper()
	for _, f := range files {
		status, mtime := f.status, f.mtime
		if status == "" {
			status = "hashed"
		}
		if mtime == "" {
			mtime = "2026-09-01T10:00:00Z"
		}
		if _, err := db.Exec(`INSERT INTO agent_files
				(drive_pk, path_key, relative_path, size, mtime, content_hash, md5, status, row_version)
			VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, 1)`,
			drive, sum(f.path), f.path, f.size, mtime, f.blake3, f.md5, status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`UPDATE agent_drives SET acked_version = acked_version + 1 WHERE id = $1`, drive); err != nil {
		t.Fatal(err)
	}
}

// buildDups builds user's index, failing the test on an error.
func buildDups(t *testing.T, user int64) {
	t.Helper()
	fingerprint, err := dupFingerprint(user)
	if err != nil {
		t.Fatal(err)
	}
	if err := BuildDuplicates(user, fingerprint); err != nil {
		t.Fatal(err)
	}
}

// dupGroups is a page of user's groups as "name size×copies reclaimable
// [same] sources: member paths".
func dupGroups(t *testing.T, user int64, filter DupFilter) []string {
	t.Helper()
	page, err := GetDupGroups(user, filter, 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range page.Groups {
		var paths []string
		for _, m := range g.Members {
			paths = append(paths, m.Source+"="+m.Path)
		}
		sort.Strings(paths)
		same := ""
		if g.SamePhysical {
			same = " same"
		}
		got = append(got, g.Name+" "+itoa(g.Size)+"x"+itoa(g.Copies)+" "+itoa(g.Reclaimable)+same+": "+
			strings.Join(paths, ", "))
	}
	return got
}

func linkDrive(t *testing.T, user int64, sub, clientKey string) {
	t.Helper()
	if _, err := LinkAccount(user, GoogleLink{GoogleSub: sub, DisplayName: "al***ce@example.com",
		Scope: "https://www.googleapis.com/auth/drive.metadata.readonly"}, clientKey); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateFiles(t *testing.T) {
	browseMigrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	linkDrive(t, alice, "sub-a", "k1")

	// Drive: a copy of "aaa", and what's left out: an empty file, a
	// system file, a trashed file, a Google Doc.
	withMd5 := func(item DriveItem, md5 string) DriveItem {
		item.Md5 = md5
		return item
	}
	binned := withMd5(driveFile("binned", "A", "binned.bin", 100), strings.Repeat("a", 32))
	binned.Trashed = true
	driveScan(t, alice, wholeDrive, false,
		dir("A", myDrive, "A"),
		withMd5(driveFile("f1", "A", "one.bin", 100), strings.Repeat("a", 32)),
		withMd5(driveFile("empty", "A", "empty", 0), strings.Repeat("e", 32)),
		withMd5(driveFile("ds", "A", ".DS_Store", 6), strings.Repeat("d", 32)),
		binned,
		driveFile("doc", "A", "a doc", 0))

	// Cloud Storage: another copy, and an old version that doesn't count.
	scan := gcsScan(t, alice)
	if err := SaveGcsBucket("k1", GcsBucketRecord{Bucket: "b1", ProjectId: "p"}, scan); err != nil {
		t.Fatal(err)
	}
	live, old := obj("x/copy.bin", 2, GcsLive, 100, "STANDARD"), obj("x/old.bin", 1, GcsNoncurrent, 100, "STANDARD")
	live.Md5Hash, old.Md5Hash = strings.Repeat("a", 32), strings.Repeat("a", 32)
	if err := UpsertGcsObjects("k1", "b1", scan, []GcsObject{live, old}); err != nil {
		t.Fatal(err)
	}

	// Agent drives: seagate1 uploaded from two boxes (one physical drive),
	// and seagate2.
	s1a := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate1")
	s1b := agentDrive(t, alice, "22222222-2222-2222-2222-222222222222", "mac", "seagate1")
	s2 := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate2")
	if _, err := db.Exec(`UPDATE agent_drives SET physical_drive_id = 7 WHERE id IN ($1, $2)`, s1a, s1b); err != nil {
		t.Fatal(err)
	}
	dupAgentFiles(t, s1a,
		dupFile{path: "m/one.bin", size: 100, md5: strings.Repeat("a", 32), blake3: "b-a"},
		dupFile{path: "same.bin", size: 50, blake3: "b-same"},
		dupFile{path: "big.iso", size: 5000, blake3: "b-big"},
		dupFile{path: "Thumbs.db", size: 9, blake3: "b-thumbs"},
		dupFile{path: "dir/._res", size: 9, blake3: "b-thumbs"},
		dupFile{path: "locked", size: 100, status: "error"})
	dupAgentFiles(t, s1b,
		dupFile{path: "same.bin", size: 50, blake3: "b-same"},
		dupFile{path: "Thumbs.db", size: 9, blake3: "b-thumbs"})
	dupAgentFiles(t, s2,
		dupFile{path: "backup/big.iso", size: 5000, blake3: "b-big"},
		dupFile{path: "backup/big copy.iso", size: 5000, blake3: "b-big"},
		dupFile{path: "locked", size: 100, status: "error"})
	// Bob's copy is his own.
	bobs := agentDrive(t, bob, "33333333-3333-3333-3333-333333333333", "bobs", "disk")
	dupAgentFiles(t, bobs, dupFile{path: "one.bin", size: 100, md5: strings.Repeat("a", 32)})

	if s, err := GetDupSummary(alice); err != nil || s.BuiltAt != nil || !s.Updating {
		t.Fatalf("summary before the first build = %+v, %v", s, err)
	}
	buildDups(t, alice)
	buildDups(t, bob)

	d, g, a1, a1b, a2 := "google:k1:drive", "google:k1:gcs:b1", "agent:"+itoa(s1a), "agent:"+itoa(s1b), "agent:"+itoa(s2)
	want := []string{
		"big.iso 5000x3 10000: " + a1 + "=big.iso, " + a2 + "=backup/big copy.iso, " + a2 + "=backup/big.iso",
		"one.bin 100x3 200: " + a1 + "=m/one.bin, " + d + "=My Drive/A/one.bin, " + g + "=x/copy.bin",
		"same.bin 50x2 0 same: " + a1 + "=same.bin, " + a1b + "=same.bin",
	}
	if got := dupGroups(t, alice, DupFilter{Kind: DupFile}); !equal(got, want) {
		t.Errorf("groups =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if got := dupGroups(t, bob, DupFilter{Kind: DupFile}); len(got) != 0 {
		t.Errorf("bob's groups = %v, want none", got)
	}

	// Filters.
	for _, c := range []struct {
		filter DupFilter
		want   []string
	}{
		{DupFilter{Kind: DupFile, HideSamePhysical: true}, []string{"big.iso", "one.bin"}},
		{DupFilter{Kind: DupFile, Across: true}, []string{"big.iso", "one.bin"}},
		{DupFilter{Kind: DupFile, Source: g}, []string{"one.bin"}},
		{DupFilter{Kind: DupFile, Source: a1b}, []string{"same.bin"}},
		{DupFilter{Kind: DupFile, MinSize: 1000}, []string{"big.iso"}},
	} {
		var got []string
		for _, line := range dupGroups(t, alice, c.filter) {
			got = append(got, strings.Fields(line)[0])
		}
		if !equal(got, c.want) {
			t.Errorf("%+v: %v, want %v", c.filter, got, c.want)
		}
	}

	page, _ := GetDupGroups(alice, DupFilter{Kind: DupFile, Source: g}, 1)
	if len(page.Groups) != 1 || page.Labels[g] != "b1 · Cloud Storage" || page.Labels[d] != "al***ce@example.com · Google Drive" ||
		page.Labels[a1] != "seagate1 (optiplex)" {
		t.Fatalf("labels = %v", page.Labels)
	}
	links := map[string]string{}
	for _, m := range page.Groups[0].Members {
		links[m.Source] = m.Item + " in " + m.Folder
	}
	if links[d] != "f1 in A" || links[g] != "x/copy.bin in b1/x/" || links[a1] != "m/one.bin in m" {
		t.Errorf("items and folders = %v", links)
	}

	summary, err := GetDupSummary(alice)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Reclaimable != 10200 || summary.Groups[DupFile] != 3 || summary.BuiltAt == nil || summary.Updating {
		t.Errorf("summary = %+v", summary)
	}
	bySource := map[string]int64{}
	for _, s := range summary.BySource {
		bySource[s.Source] = s.Bytes
	}
	if bySource[a2] != 10000 || bySource[a1] != 5100 || bySource[d] != 100 || bySource[a1b] != 0 {
		t.Errorf("by source = %+v", summary.BySource)
	}

	members, err := GetDupMembers(alice, page.Groups[0].Id, 1)
	if err != nil || members.Total != 3 || len(members.Members) != 3 {
		t.Errorf("members = %+v, %v", members, err)
	}
	if _, err := GetDupMembers(bob, page.Groups[0].Id, 1); err != ErrNotFound {
		t.Errorf("bob reading alice's group: err = %v, want ErrNotFound", err)
	}
}

func TestDuplicateFolders(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	linkDrive(t, alice, "sub-a", "k1")
	s1 := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate1")
	s2 := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate2")
	dupAgentFiles(t, s1,
		dupFile{path: "Photos/a.jpg", size: 10, blake3: "ka"},
		dupFile{path: "Photos/2024/b.jpg", size: 20, blake3: "kb"},
		dupFile{path: "Photos/2024/.DS_Store", size: 6, blake3: "ds1"},
		// Same files, other names: not the same folder.
		dupFile{path: "Renamed/x.jpg", size: 10, blake3: "ka"},
		dupFile{path: "Renamed/2024/b.jpg", size: 20, blake3: "kb"},
		dupFile{path: "Mixed/m.txt", size: 3, md5: strings.Repeat("c", 32)},
		dupFile{path: "Mixed/locked", size: 3, status: "error"})
	dupAgentFiles(t, s2,
		dupFile{path: "backup/Photos/a.jpg", size: 10, blake3: "ka"},
		dupFile{path: "backup/Photos/2024/b.jpg", size: 20, blake3: "kb"},
		dupFile{path: "backup/Photos/2024/Thumbs.db", size: 7, blake3: "th"},
		dupFile{path: "docs/m.txt", size: 3, md5: strings.Repeat("c", 32)})
	// A Drive folder with the same file as docs/: matched by MD5.
	withMd5 := driveFile("m1", "D", "m.txt", 3)
	withMd5.Md5 = strings.Repeat("c", 32)
	driveScan(t, alice, wholeDrive, false, dir("D", myDrive, "Docs"), withMd5)
	buildDups(t, alice)

	a1, a2, d := "agent:"+itoa(s1), "agent:"+itoa(s2), "google:k1:drive"
	// Photos and backup/Photos match, so their 2024s aren't listed again;
	// Renamed/2024 matches both 2024s, so it is, with them.
	want := []string{
		"2024 20x3 40: " + a1 + "=Photos/2024, " + a1 + "=Renamed/2024, " + a2 + "=backup/Photos/2024",
		"Photos 30x2 30: " + a1 + "=Photos, " + a2 + "=backup/Photos",
		"Docs 3x2 3: " + a2 + "=docs, " + d + "=My Drive/Docs",
	}
	if got := dupGroups(t, alice, DupFilter{Kind: DupFolder}); !equal(got, want) {
		t.Errorf("folder groups =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	page, _ := GetDupGroups(alice, DupFilter{Kind: DupFolder, Source: d}, 1)
	for _, m := range page.Groups[0].Members {
		if m.Source == d && (m.Item != "D" || m.Folder != "D") {
			t.Errorf("Drive folder member = %+v, want its ID", m)
		}
		if m.Source == a2 && (m.Item != "docs" || m.Files != 1 || m.Size != 3) {
			t.Errorf("agent folder member = %+v", m)
		}
	}
	summary, _ := GetDupSummary(alice)
	if len(summary.Uncomparable) != 1 || summary.Uncomparable[0].Source != a1 || summary.Uncomparable[0].Folders != 2 {
		// Mixed, and seagate1's root above it.
		t.Errorf("uncomparable = %+v, want seagate1's Mixed and root", summary.Uncomparable)
	}
}

func TestSignerIgnoresOrderButNotNames(t *testing.T) {
	sign := func(files ...string) map[string]string {
		sort.Strings(files)
		s := newSigner("src", "Source", sql.NullInt64{})
		for _, f := range files {
			path, key, _ := strings.Cut(f, "=")
			s.add(path, key, 1)
		}
		sigs := map[string]string{}
		for _, f := range s.finish() {
			sigs[f.path] = f.sig
		}
		return sigs
	}
	a := sign("x/a=1", "x/b=2", "x/s/c=3", "y/b=2", "y/a=1", "y/s/c=3", "z/a=1", "z/b=2", "z/t/c=3",
		"x b/a=1", "x.txt=9", "x0/a=1")
	if a["x"] != a["y"] {
		t.Error("x and y hold the same, but their signatures differ")
	}
	if a["x"] == a["z"] {
		t.Error("z's subfolder has another name, but z signs like x")
	}
	if a["x/s"] != a["z/t"] {
		t.Error("x/s and z/t hold the same, but their signatures differ")
	}
	if _, ok := a["x b"]; !ok || a["x b"] == a["x"] {
		t.Errorf("x b, sorted before x/, = %q", a["x b"])
	}
	// A folder isn't a file of the same content.
	b := sign("f/a=1")
	c := sign("a=1")
	if b["f"] != c[""] {
		t.Error("f's contents are the root's, but they sign differently")
	}
}

func TestLikelyDuplicatePhotos(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	linkDrive(t, alice, "sub-a", "k1")

	// Taken at 10:00 UTC, 4000×3000.
	scanId, err := LogStartScan("google_photos", alice)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveScanMetadata("someone", "k1", "Picked in Google Photos", "", scanId); err != nil {
		t.Fatal(err)
	}
	taken := time.Date(2020, 1, 1, 10, 0, 0, 0, time.UTC)
	w, h := 4000, 3000
	size := int64(900)
	if err := SavePickedItems(scanId, []PickedItem{
		{MediaItemId: "p1", MediaType: "PHOTO", Filename: "IMG_1.JPG", CreateTime: &taken, Width: &w, Height: &h,
			Size: &size, SizeSource: SizeFromHead},
		{MediaItemId: "p2", MediaType: "PHOTO", Filename: "IMG_2.JPG", CreateTime: &taken, SizeSource: SizeFromHead},
	}); err != nil {
		t.Fatal(err)
	}
	MarkScanCompleted(scanId)

	image := func(id, name string, capture time.Time, width int64) DriveItem {
		item := driveFile(id, myDrive, name, 1000)
		item.Md5 = strings.Repeat(id[len(id)-1:], 32)
		item.CaptureTime, item.Width, item.Height = capture, width, 3000
		return item
	}
	driveScan(t, alice, wholeDrive, false,
		// Its local time in New York: a match.
		image("d1", "img_1.jpg", taken.Add(-5*time.Hour), 4000),
		// Seven minutes off whole quarter-hours: not.
		image("d2", "IMG_1.JPG", taken.Add(-5*time.Hour+7*time.Minute), 4000),
		// Other dimensions: not.
		image("d3", "IMG_1.JPG", taken, 1000),
		// No capture time: its modified time, which is far off.
		driveFile("d4", myDrive, "IMG_1.JPG", 5))
	drive := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate1")
	dupAgentFiles(t, drive,
		dupFile{path: "pics/IMG_1.JPG", size: 1000, blake3: "x1", mtime: "2020-01-01T10:00:30Z"},
		dupFile{path: "pics/old/IMG_1.JPG", size: 1000, blake3: "x2", mtime: "2020-01-01T10:05:00Z"},
		dupFile{path: "IMG_3.JPG", size: 1000, blake3: "x3", mtime: "2020-01-01T10:00:00Z"})
	buildDups(t, alice)

	a := "agent:" + itoa(drive)
	want := []string{"IMG_1.JPG 900x3 0: " + a + "=pics/IMG_1.JPG, google:k1:drive=My Drive/img_1.jpg, google:k1:photos=IMG_1.JPG"}
	if got := dupGroups(t, alice, DupFilter{Kind: DupPhoto}); !equal(got, want) {
		t.Errorf("photo groups =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	page, _ := GetDupGroups(alice, DupFilter{Kind: DupPhoto}, 1)
	if g := page.Groups[0]; g.Match != "name and capture time, dimensions; name and modified time" ||
		g.Members[0].Source != "google:k1:photos" || g.Members[0].Item != "p1" {
		t.Errorf("photo group = %+v", g)
	}
	if s, _ := GetDupSummary(alice); s.Groups[DupPhoto] != 1 || s.Reclaimable != 0 {
		t.Errorf("summary = %+v; likely photos reclaim nothing", s)
	}
}

func TestDuplicatesRebuildWhenTheirInputsChange(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	drive := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "optiplex", "seagate1")
	dupAgentFiles(t, drive, dupFile{path: "a", size: 1, blake3: "k"}, dupFile{path: "b", size: 1, blake3: "k"})

	builtAt := func() time.Time {
		s, err := GetDupSummary(alice)
		if err != nil || s.BuiltAt == nil {
			t.Fatalf("summary = %+v, %v", s, err)
		}
		return *s.BuiltAt
	}
	checkDuplicates(context.Background())
	first := builtAt()
	checkDuplicates(context.Background())
	if builtAt() != first {
		t.Error("an unchanged index was rebuilt")
	}
	// An upload raises the drive's version.
	dupAgentFiles(t, drive, dupFile{path: "c", size: 1, blake3: "k"})
	checkDuplicates(context.Background())
	if builtAt() == first {
		t.Error("the index wasn't rebuilt after an upload")
	}
	if got := dupGroups(t, alice, DupFilter{Kind: DupFile}); len(got) != 1 || !strings.HasPrefix(got[0], "a 1x3 2:") {
		t.Errorf("groups after the upload = %v", got)
	}
	// So does a physical drive's match.
	second := builtAt()
	if _, err := db.Exec(`UPDATE agent_drives SET physical_drive_id = 3`); err != nil {
		t.Fatal(err)
	}
	checkDuplicates(context.Background())
	if builtAt() == second {
		t.Error("the index wasn't rebuilt after a drive was matched")
	}
}
