package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"testing"
)

// count is the number of rows a query counts.
func count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Get(&n, query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// finishedScanOf is scanOf's scan of scanType, marked completed.
func finishedScanOf(t *testing.T, user int64, scanType, name, clientKey string) int {
	t.Helper()
	scanId, err := LogStartScan(scanType, user)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveScanMetadata(name, clientKey, "", "", scanId); err != nil {
		t.Fatal(err)
	}
	if err := MarkScanCompleted(scanId); err != nil {
		t.Fatal(err)
	}
	return scanId
}

// linkBoth links an account with Gmail and Drive access.
func linkBoth(t *testing.T, user int64, sub, name, key string) {
	t.Helper()
	if _, err := LinkAccount(user, GoogleLink{GoogleSub: sub, DisplayName: name, RefreshToken: "rt-" + key,
		Scope: "https://www.googleapis.com/auth/gmail.readonly https://www.googleapis.com/auth/drive.metadata.readonly"}, key); err != nil {
		t.Fatal(err)
	}
}

func messages(t *testing.T, scanId int, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if _, err := db.Exec(`INSERT INTO messagemetadata (message_id, size_estimate, scan_id) VALUES ($1, 10, $2)`,
			id, scanId); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeleteAgentDriveData(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	mine := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "mbp", "seagate1")
	other := agentDrive(t, alice, "22222222-2222-2222-2222-222222222222", "optiplex", "seagate1")
	agentFiles(t, mine, 1, map[string]int64{"a/x": 5, "a/y": 6, "z": 7})
	agentFiles(t, other, 1, map[string]int64{"a/x": 5})
	if _, err := db.Exec(`INSERT INTO agent_scan_runs (drive_pk, run_id, started_at, row_version) VALUES ($1, 1, now(), 1)`, mine); err != nil {
		t.Fatal(err)
	}
	if err := rebuildAgentTotals(mine, 1); err != nil {
		t.Fatal(err)
	}
	if label, err := AgentDriveLabel(mine); err != nil || label != "seagate1 (mbp)" {
		t.Errorf("label = %q, %v", label, err)
	}

	counts, err := DeleteAgentDriveData(mine)
	if err != nil || counts["files"] != 3 {
		t.Fatalf("counts %v, %v; want 3 files", counts, err)
	}
	for table, query := range map[string]string{
		"agent_drives":       `SELECT count(*) FROM agent_drives WHERE id = $1`,
		"agent_files":        `SELECT count(*) FROM agent_files WHERE drive_pk = $1`,
		"agent_dir_listings": `SELECT count(*) FROM agent_dir_listings WHERE drive_pk = $1`,
		"agent_scan_runs":    `SELECT count(*) FROM agent_scan_runs WHERE drive_pk = $1`,
	} {
		if n := count(t, query, mine); n != 0 {
			t.Errorf("%s still has %d rows of the deleted drive", table, n)
		}
	}
	if n := count(t, `SELECT count(*) FROM browse_folder_totals WHERE source = $1`, agentSource(mine)); n != 0 {
		t.Errorf("%d cached totals left", n)
	}
	// The same physical drive from another box, and the agent, stay.
	if n := count(t, `SELECT count(*) FROM agent_files WHERE drive_pk = $1`, other); n != 1 {
		t.Errorf("the other box's copy has %d files, want 1", n)
	}
	if n := count(t, `SELECT count(*) FROM agent_agents`); n != 2 {
		t.Errorf("%d agents left, want 2", n)
	}
	if _, err := DeleteAgentDriveData(mine); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting it again: %v, want ErrNotFound", err)
	}
}

func TestDeleteServiceDataGmail(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	linkBoth(t, alice, "a1", "al***ce@example.com", "k1")
	linkBoth(t, alice, "a2", "al***c2@example.com", "k2")
	gmail := finishedScanOf(t, alice, "gmail", "al***ce@example.com", "k1")
	drive := finishedScanOf(t, alice, "google_drive", "al***ce@example.com", "k1")
	otherAccount := finishedScanOf(t, alice, "gmail", "al***c2@example.com", "k2")
	messages(t, gmail, "m1", "m2")
	messages(t, otherAccount, "m3")

	// Another user can't reach it: none of bob's scans are of k1.
	if counts, err := DeleteServiceData(bob, "k1", ServiceGmail); err != nil || counts["scans"] != 0 {
		t.Fatalf("bob's delete: %v, %v; want nothing deleted", counts, err)
	}
	counts, err := DeleteServiceData(alice, "k1", ServiceGmail)
	if err != nil || counts["scans"] != 1 || counts["messages"] != 2 {
		t.Fatalf("counts %v, %v; want 1 scan, 2 messages", counts, err)
	}
	if n := count(t, `SELECT count(*) FROM scans WHERE id = $1`, gmail); n != 0 {
		t.Error("the Gmail scan is still there")
	}
	// The account, its Drive scan, and the other account's Gmail stay.
	if n := count(t, `SELECT count(*) FROM scans WHERE id IN ($1, $2)`, drive, otherAccount); n != 2 {
		t.Errorf("%d of the other scans left, want 2", n)
	}
	if n := count(t, `SELECT count(*) FROM messagemetadata WHERE scan_id = $1`, otherAccount); n != 1 {
		t.Errorf("the other account has %d messages, want 1", n)
	}
	if _, err := GetOAuthToken(alice, "k1"); err != nil {
		t.Errorf("the account was unlinked: %v", err)
	}

	// Not while one of its scans runs.
	running := scanOf(t, alice, "al***ce@example.com", "k1", "is:unread")
	if _, err := DeleteServiceData(alice, "k1", ServiceGmail); !errors.Is(err, ErrScanRunning) {
		t.Errorf("with scan %d running: %v, want ErrScanRunning", running, err)
	}
}

func TestDeleteServiceDataOfEachService(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	linkBoth(t, alice, "a1", "al***ce@example.com", "k1")
	gmail := finishedScanOf(t, alice, "gmail", "al***ce@example.com", "k1")
	messages(t, gmail, "m1")
	driveScanId := finishedScanOf(t, alice, "google_drive", "al***ce@example.com", "k1")
	gcsScan := finishedScanOf(t, alice, "gcs", "al***ce@example.com", "k1")
	photosScan := finishedScanOf(t, alice, "google_photos", "al***ce@example.com", "k1")
	driveScan(t, alice, wholeDrive, false, dir("A", myDrive, "A"), driveFile("f1", "A", "f1", 10))
	if err := SaveGcsBucket("k1", GcsBucketRecord{Bucket: "b", ProjectId: "p"}, gcsScan); err != nil {
		t.Fatal(err)
	}
	if err := UpsertGcsObjects("k1", "b", gcsScan, []GcsObject{{Name: "o", Generation: 1, State: "live", Size: 3}}); err != nil {
		t.Fatal(err)
	}
	if err := SavePickedItems(photosScan, []PickedItem{{MediaItemId: "p1", MediaType: "PHOTO", Filename: "a.jpg", SizeSource: "head"}}); err != nil {
		t.Fatal(err)
	}

	// Drive: its scans and record; the rest stays.
	counts, err := DeleteServiceData(alice, "k1", ServiceDrive)
	if err != nil || counts["scans"] != 1 || counts["drive_items"] != 2 {
		t.Fatalf("drive: %v, %v; want 1 scan, 2 items", counts, err)
	}
	if n := count(t, `SELECT count(*) FROM scans WHERE id = $1`, driveScanId); n != 0 {
		t.Error("the Drive scan is still there")
	}
	if n := count(t, `SELECT count(*) FROM drive_accounts WHERE client_key = 'k1'`); n != 0 {
		t.Error("the Drive record's account row is still there")
	}
	// Cloud Storage.
	counts, err = DeleteServiceData(alice, "k1", "gcs")
	if err != nil || counts["scans"] != 1 || counts["gcs_objects"] != 1 || counts["gcs_buckets"] != 1 {
		t.Fatalf("gcs: %v, %v", counts, err)
	}
	// Photos.
	counts, err = DeleteServiceData(alice, "k1", "photos")
	if err != nil || counts["scans"] != 1 || counts["photos_items"] != 1 {
		t.Fatalf("photos: %v, %v", counts, err)
	}
	// Gmail, and the account, are untouched.
	if n := count(t, `SELECT count(*) FROM messagemetadata WHERE scan_id = $1`, gmail); n != 1 {
		t.Errorf("Gmail lost its messages: %d", n)
	}
	if _, err := GetOAuthToken(alice, "k1"); err != nil {
		t.Errorf("the account was unlinked: %v", err)
	}
	if _, err := DeleteServiceData(alice, "k1", "calendar"); err == nil {
		t.Error("an unknown service was accepted")
	}
}

func TestDeleteAccountData(t *testing.T) {
	migrated(t)
	alice := addUser(t, "alice")
	linkBoth(t, alice, "a1", "al***ce@example.com", "k1")
	linkBoth(t, alice, "a2", "al***c2@example.com", "k2")
	gmail := finishedScanOf(t, alice, "gmail", "al***ce@example.com", "k1")
	messages(t, gmail, "m1")
	gcsScan := finishedScanOf(t, alice, "gcs", "al***ce@example.com", "k1")
	keep := finishedScanOf(t, alice, "gmail", "al***c2@example.com", "k2")
	messages(t, keep, "m2")
	// k1's Drive record (driveScan records under k1) and Cloud Storage.
	driveScan(t, alice, wholeDrive, false, dir("A", myDrive, "A"), driveFile("f1", "A", "f1", 10))
	for _, key := range []string{"k1", "k2"} {
		if err := SaveGcsBucket(key, GcsBucketRecord{Bucket: "b", ProjectId: "p"}, gcsScan); err != nil {
			t.Fatal(err)
		}
		if err := UpsertGcsObjects(key, "b", gcsScan, []GcsObject{{Name: "o", Generation: 1, State: "live", Size: 3}}); err != nil {
			t.Fatal(err)
		}
		if err := RebuildGcsPrefixTotals(key, "b"); err != nil {
			t.Fatal(err)
		}
	}

	counts, err := DeleteAccountData(alice, "k1")
	if err != nil {
		t.Fatal(err)
	}
	// The Gmail and Cloud Storage scans; driveScan's scan records no
	// scanmetadata, so it isn't one of k1's, though its Drive items are.
	want := map[string]int64{"scans": 2, "messages": 1, "photos_items": 0, "drive_items": 2, "gcs_objects": 1, "gcs_buckets": 1}
	if !maps.Equal(counts, want) {
		t.Errorf("counts %v, want %v", counts, want)
	}
	for _, q := range []string{
		`SELECT count(*) FROM privatetokens WHERE client_key = 'k1'`,
		`SELECT count(*) FROM scanmetadata WHERE client_key = 'k1'`,
		`SELECT count(*) FROM drive_items WHERE client_key = 'k1'`,
		`SELECT count(*) FROM drive_accounts WHERE client_key = 'k1'`,
		`SELECT count(*) FROM gcs_objects WHERE client_key = 'k1'`,
		`SELECT count(*) FROM gcs_prefix_totals WHERE client_key = 'k1'`,
		`SELECT count(*) FROM gcs_buckets WHERE client_key = 'k1'`,
		`SELECT count(*) FROM browse_folder_totals WHERE source = 'drive:k1'`,
	} {
		if n := count(t, q); n != 0 {
			t.Errorf("%s = %d, want 0", q, n)
		}
	}
	// The other account is untouched.
	for q, want := range map[string]int{
		`SELECT count(*) FROM privatetokens WHERE client_key = 'k2'`: 1,
		`SELECT count(*) FROM gcs_objects WHERE client_key = 'k2'`:   1,
		`SELECT count(*) FROM gcs_buckets WHERE client_key = 'k2'`:   1,
	} {
		if n := count(t, q); n != want {
			t.Errorf("%s = %d, want %d", q, n, want)
		}
	}
	if n := count(t, `SELECT count(*) FROM messagemetadata WHERE scan_id = $1`, keep); n != 1 {
		t.Errorf("the other account has %d messages, want 1", n)
	}
	if _, err := DeleteAccountData(alice, "k1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting it again: %v, want ErrNotFound", err)
	}
	scanOf(t, alice, "al***c2@example.com", "k2", "")
	if _, err := DeleteAccountData(alice, "k2"); !errors.Is(err, ErrScanRunning) {
		t.Errorf("with a scan running: %v, want ErrScanRunning", err)
	}
}

func TestDeletionJobs(t *testing.T) {
	migrated(t)
	alice, bob := addUser(t, "alice"), addUser(t, "bob")
	job, started, err := StartDeletion(alice, ServiceGmail, "k1", "al***ce@example.com · Gmail")
	if err != nil || !started || job.Status != JobRunning {
		t.Fatalf("start = %+v, %v, %v", job, started, err)
	}
	// A second request for the same target gets the running job.
	again, started, err := StartDeletion(alice, ServiceGmail, "k1", "x")
	if err != nil || started || again.ID != job.ID {
		t.Errorf("again = %+v, %v, %v; want job %d", again, started, err, job.ID)
	}
	if err := FinishDeletion(job.ID, map[string]int64{"messages": 7}, "", nil); err != nil {
		t.Fatal(err)
	}
	done, err := GetDeletion(alice, job.ID)
	var counts map[string]int64
	json.Unmarshal(done.Counts, &counts)
	if err != nil || done.Status != JobDone || counts["messages"] != 7 || done.FinishedAt == nil {
		t.Errorf("done = %+v, %v", done, err)
	}
	if _, err := GetDeletion(bob, job.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("bob sees alice's job: %v", err)
	}
	// Once finished, the target can be deleted again.
	if _, started, _ := StartDeletion(alice, ServiceGmail, "k1", "x"); !started {
		t.Error("a new job didn't start after the last one finished")
	}

	// A job cut off by a restart is failed at the next start; old ones go.
	if err := migrateDeletions(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, `SELECT count(*) FROM deletions WHERE status = 'running'`); n != 0 {
		t.Errorf("%d jobs still running after a restart", n)
	}
	if _, err := db.Exec(`UPDATE deletions SET finished_at = now() - interval '31 days' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := PurgeDeletionJobs(); err != nil {
		t.Fatal(err)
	}
	if n := count(t, `SELECT count(*) FROM deletions`); n != 1 {
		t.Errorf("%d jobs after the purge, want 1", n)
	}
}

func TestAccountLabels(t *testing.T) {
	labels := AccountLabels([]Account{
		{ClientKey: "JVVYZFCI0PaW", DisplayName: "jyo****ri@gmail.com"},
		{ClientKey: "m3ybPs6SiJ7t", DisplayName: "jyo****ri@gmail.com"},
		{ClientKey: "3hFhspIw2n6t", DisplayName: "jyo****i2@gmail.com"},
	})
	want := map[string]string{
		"JVVYZFCI0PaW": "jyo****ri@gmail.com · JVVY",
		"m3ybPs6SiJ7t": "jyo****ri@gmail.com · m3yb",
		"3hFhspIw2n6t": "jyo****i2@gmail.com",
	}
	if !maps.Equal(labels, want) {
		t.Errorf("labels %v, want %v", labels, want)
	}
}

func TestGetSettingsData(t *testing.T) {
	browseMigrated(t)
	alice := addUser(t, "alice")
	linkBoth(t, alice, "a1", "al***ce@example.com", "k1")
	gmail := finishedScanOf(t, alice, "gmail", "al***ce@example.com", "k1")
	messages(t, gmail, "m1", "m2")
	mbp := agentDrive(t, alice, "11111111-1111-1111-1111-111111111111", "mbp", "seagate1")
	opti := agentDrive(t, alice, "22222222-2222-2222-2222-222222222222", "optiplex", "seagate1")
	agentFiles(t, mbp, 1, map[string]int64{"x": 5})
	if _, err := db.Exec(`UPDATE agent_drives SET physical_drive_id = 1 WHERE id IN ($1, $2)`, mbp, opti); err != nil {
		t.Fatal(err)
	}

	data, err := GetSettingsData(alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(data.Accounts) != 1 {
		t.Fatalf("accounts %+v", data.Accounts)
	}
	a := data.Accounts[0]
	if a.Label != "al***ce@example.com" || a.Scans != 1 || a.ServiceScans["gmail"] != 1 || *a.Recorded["gmail"].Files != 2 ||
		a.Recorded["drive"].Files != nil || a.Job != nil || a.RunningScan != 0 {
		t.Errorf("account %+v", a)
	}
	if len(data.Drives) != 2 || data.Drives[0].Hostname != "mbp" || data.Drives[0].Files != 1 ||
		len(data.Drives[0].OtherCopies) != 1 || data.Drives[0].OtherCopies[0] != "optiplex" {
		t.Errorf("drives %+v", data.Drives)
	}
	if _, _, err := StartDeletion(alice, DeleteAgentDrive, fmt.Sprint(mbp), "seagate1 (mbp)"); err != nil {
		t.Fatal(err)
	}
	data, _ = GetSettingsData(alice)
	if data.Drives[0].Job == nil || data.Drives[0].Job.Status != JobRunning {
		t.Errorf("the drive's running job isn't listed: %+v", data.Drives[0])
	}
	if other, _ := GetSettingsData(addUser(t, "bob")); len(other.Accounts) != 0 || len(other.Drives) != 0 {
		t.Errorf("bob sees %+v", other)
	}
}
