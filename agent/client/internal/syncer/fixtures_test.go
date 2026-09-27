package syncer

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/remote/remotetest"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

// The golden fixtures in agent/wire/testdata are the contract with
// agentserver, which tests against the same files.

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "wire", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Local rows equivalent to changes-batch.json map to exactly its changes.
func TestFeedMatchesBatchFixture(t *testing.T) {
	var want wire.ChangeBatch
	if err := json.Unmarshal(fixture(t, "changes-batch.json"), &want); err != nil {
		t.Fatal(err)
	}

	e := newEnv(t)
	e.drive("seagate2")
	scanned := func(s string) time.Time { tm, _ := time.Parse(time.RFC3339, s); return tm }
	recs := []store.FileRecord{
		{RelPath: "Jyo/Backup/a.jpg", Size: 482113, MTimeUnix: 1726000000, Mode: 420, ContentHash: "9f2c4be1d0a7a95c1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d",
			HashAlgo: "blake3", Status: "hashed", ScannedAt: scanned("2026-09-24T09:58:01Z")},
		{RelPath: "Jyo/Backup/b.mov", Size: 0, MTimeUnix: 1726000100, Mode: 420, Status: "error", ErrorMessage: "read: input/output error",
			ScannedAt: scanned("2026-09-24T09:58:02Z")},
		{RelPath: "Jyo/caf\xe9", Size: 3, MTimeUnix: 1726000200, Mode: 420, ContentHash: "abc", HashAlgo: "blake3", Status: "hashed",
			ScannedAt: scanned("2026-09-24T09:58:03Z")},
		{RelPath: "Jyo/Backup/old.txt", Size: 1, Status: "hashed", ContentHash: "x", HashAlgo: "blake3", ScannedAt: scanned("2026-09-24T09:58:03Z")},
	}
	for i := range recs {
		recs[i].DriveID = "seagate2"
	}
	if err := e.st.UpsertFiles(ctx, recs); err != nil {
		t.Fatal(err)
	}
	e.del("seagate2", "Jyo/Backup/old.txt")
	e.dirs("seagate2", "", "Jyo")
	e.st.SyncDirListings(ctx, "seagate2", map[string][]store.DirChild{"Jyo/Backup": {{Name: "photos", IsDir: true}, {Name: "tmp"}}}, true)
	e.st.SyncDirListings(ctx, "seagate2", map[string][]store.DirChild{"Jyo/Backup": {{Name: "photos", IsDir: true}}}, true)
	if _, err := e.st.StartScanRun("seagate2"); err != nil {
		t.Fatal(err)
	}

	// What the store API can't set: the fixture's timestamps, the run id,
	// and a running scan's counters.
	db, err := sql.Open("sqlite", filepath.Join(e.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`UPDATE dir_listings SET is_dir = 1, first_seen_at = '2026-09-24 09:50:00+00:00' WHERE drive_id = 'seagate2'`,
		`UPDATE scan_runs SET id = 42, started_at = '2026-09-24 09:49:00+00:00', files_seen = 0, bytes_hashed = 0, interrupted = NULL`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}

	page, _, err := e.feed.Page(ctx, "seagate2", 0, e.head(), 100)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]wire.Change{}
	for _, en := range page {
		got[remotetest.Key(en.Change)+"\x00"+en.Change.Op] = en.Change
	}
	if len(got) != len(want.Changes) {
		t.Errorf("%d local entries, fixture has %d", len(got), len(want.Changes))
	}
	for _, w := range want.Changes {
		g, ok := got[remotetest.Key(w)+"\x00"+w.Op]
		if !ok {
			t.Errorf("no local entry for fixture change %+v", w)
			continue
		}
		g.V = w.V
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		var gm, wm any
		json.Unmarshal(gb, &gm)
		json.Unmarshal(wb, &wm)
		if !reflect.DeepEqual(gm, wm) {
			t.Errorf("mapping differs:\nlocal:   %s\nfixture: %s", gb, wb)
		}
	}
}

// The client sends drive-open-request.json for the same drive, and decodes
// the fixture responses.
func TestClientMatchesDriveFixtures(t *testing.T) {
	var sent []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			sent, _ = io.ReadAll(r.Body)
			w.Write(fixture(t, "drive-open-response.json"))
		case http.MethodPost:
			w.Write(fixture(t, "changes-response.json"))
		}
	}))
	defer srv.Close()
	c, err := remote.New(remote.Options{RemoteURL: srv.URL, AgentID: "7b0e0000-0000-4000-8000-000000000000"})
	if err != nil {
		t.Fatal(err)
	}

	d := store.SyncDrive{DriveID: "seagate2", DriveRoot: "/mnt/seagate2", BackupRoot: "Jyo/Backup",
		Identity: store.DriveIdentity{FSUUID: "ABCD1234", FSType: "exfat", FSUUIDSource: "linux", HWSerial: "NA8F2K1X"}}
	req := wire.DriveOpenRequest{StreamID: "c1f5a9e0-6f0b-4a8e-9a53-2d1c0b9e7f31", DriveRoot: d.DriveRoot, BackupRoot: d.BackupRoot, Identity: identityOf(d.Identity)}
	open, err := c.OpenDrive(ctx, "at", "seagate2", req)
	if err != nil {
		t.Fatal(err)
	}
	var gm, wm any
	json.Unmarshal(sent, &gm)
	json.Unmarshal(fixture(t, "drive-open-request.json"), &wm)
	if !reflect.DeepEqual(gm, wm) {
		t.Errorf("open request %s differs from the fixture", sent)
	}
	if !reflect.DeepEqual(open.AckedRanges, []wire.Range{{0, 18234}, {20011, 20510}}) || open.PhysicalDrive.Linked[0].Hostname != "macbook" {
		t.Errorf("open response = %+v", open)
	}

	resp, err := c.PostChanges(ctx, "at", "seagate2", "k", []byte{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Applied != 7 || len(resp.Rejected) != 1 || resp.Rejected[0].V != 18241 || checkRanges(resp.AckedRanges) != nil {
		t.Errorf("changes response = %+v", resp)
	}
}
