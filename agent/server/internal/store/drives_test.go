package store_test

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/server/internal/store"
	"github.com/jyothri/bhandaar/agent/server/internal/testdb"
	"github.com/jyothri/bhandaar/agent/wire"
)

var ctx = context.Background()

type fixture struct {
	st   *store.Store
	user int64
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	st := testdb.New(t)
	uid, err := st.CreateUser(ctx, "jyothri", "h")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{st: st, user: uid}
}

// agent registers an agent for user with a hostname.
func (f fixture) agent(t *testing.T, user int64, hostname string) string {
	t.Helper()
	id := uuid.NewString()
	if err := f.st.UpsertAgent(ctx, store.Agent{ID: id, UserID: user, Hostname: hostname}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f fixture) open(t *testing.T, agent, drive, stream string, id *wire.Identity) wire.DriveOpenResponse {
	t.Helper()
	resp, err := f.st.OpenDrive(ctx, f.user, agent, drive, wire.DriveOpenRequest{StreamID: stream, DriveRoot: "/mnt/" + drive, Identity: id})
	if err != nil {
		t.Fatalf("OpenDrive: %v", err)
	}
	return resp
}

func ident(uuid, typ, src, serial string) *wire.Identity {
	return &wire.Identity{FSUUID: uuid, FSType: typ, FSUUIDSource: src, HWSerial: serial}
}

func physicalID(t *testing.T, r wire.DriveOpenResponse) int64 {
	t.Helper()
	if r.PhysicalDrive == nil {
		t.Fatalf("not linked: %+v", r)
	}
	return r.PhysicalDrive.ID
}

func TestOpenNewDrive(t *testing.T) {
	f := newFixture(t)
	a := f.agent(t, f.user, "optiplex7070")
	stream := uuid.NewString()
	r := f.open(t, a, "seagate2", stream, ident("abcd-1234", "EXT4", "linux", "na8f2k1x"))
	if r.Reset || len(r.AckedRanges) != 0 || r.DriveID != "seagate2" || r.StreamID != stream {
		t.Errorf("response = %+v", r)
	}
	if r.PhysicalDrive == nil || r.PhysicalDrive.CloneOf != nil || len(r.PhysicalDrive.Linked) != 0 {
		t.Errorf("physical = %+v", r.PhysicalDrive)
	}
	var fsUUID, fsType, serial string
	f.st.Pool.QueryRow(ctx, `SELECT fs_uuid, fs_type, hw_serial FROM agent_physical_drives`).Scan(&fsUUID, &fsType, &serial)
	if fsUUID != "ABCD1234" || fsType != "ext4" || serial != "NA8F2K1X" {
		t.Errorf("normalised identity = %s %s %s", fsUUID, fsType, serial)
	}
}

func TestReopenAndStreamReset(t *testing.T) {
	f := newFixture(t)
	a := f.agent(t, f.user, "h")
	stream := uuid.NewString()
	f.open(t, a, "d", stream, nil)
	var pk int64
	f.st.Pool.QueryRow(ctx, `SELECT id FROM agent_drives`).Scan(&pk)
	f.st.Pool.Exec(ctx, `INSERT INTO agent_sync_ranges VALUES ($1, 0, 100), ($1, 150, 200)`, pk)
	f.st.Pool.Exec(ctx, `UPDATE agent_drives SET acked_version = 100 WHERE id = $1`, pk)
	f.st.Pool.Exec(ctx, `INSERT INTO agent_files VALUES ($1, '\x01', 'a', NULL, 1, now(), 420, 'h', 'blake3', 'hashed', NULL, now(), 5)`, pk)
	f.st.Pool.Exec(ctx, `INSERT INTO agent_tombstones VALUES ($1, 'file', '\x02', 7)`, pk)

	// Same stream: the ranges come back, the root is updated.
	r, err := f.st.OpenDrive(ctx, f.user, a, "d", wire.DriveOpenRequest{StreamID: stream, DriveRoot: "/media/new"})
	if err != nil || r.Reset || len(r.AckedRanges) != 2 || r.AckedRanges[1] != (wire.Range{150, 200}) {
		t.Fatalf("reopen = %+v, %v", r, err)
	}
	var root string
	f.st.Pool.QueryRow(ctx, `SELECT drive_root FROM agent_drives`).Scan(&root)
	if root != "/media/new" {
		t.Errorf("root = %q", root)
	}

	// A new stream wipes the drive.
	r = f.open(t, a, "d", uuid.NewString(), nil)
	if !r.Reset || len(r.AckedRanges) != 0 {
		t.Errorf("reset = %+v", r)
	}
	for _, table := range []string{"agent_files", "agent_tombstones", "agent_sync_ranges"} {
		var n int
		f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n)
		if n != 0 {
			t.Errorf("%s: %d rows survived the reset", table, n)
		}
	}
	var acked int64
	f.st.Pool.QueryRow(ctx, `SELECT acked_version FROM agent_drives`).Scan(&acked)
	if acked != 0 {
		t.Errorf("acked_version = %d", acked)
	}
}

func TestMatching(t *testing.T) {
	f := newFixture(t)
	linux, mac, other := f.agent(t, f.user, "optiplex7070"), f.agent(t, f.user, "macbook"), f.agent(t, f.user, "optiplex7050")

	// Same filesystem ID, same serial: one physical drive, linked both ways.
	a := f.open(t, linux, "seagate2", uuid.NewString(), ident("EXT4ID", "ext4", "linux", "S1"))
	b := f.open(t, mac, "seagate", uuid.NewString(), ident("EXT4ID", "ext4", "macos", "S1"))
	if physicalID(t, a) != physicalID(t, b) {
		t.Error("ext4 from Linux and macOS not linked")
	}
	if len(b.PhysicalDrive.Linked) != 1 || b.PhysicalDrive.Linked[0].Hostname != "optiplex7070" || b.PhysicalDrive.Linked[0].DriveID != "seagate2" {
		t.Errorf("linked = %+v", b.PhysicalDrive.Linked)
	}

	// No serial on either side still matches.
	c := f.open(t, other, "wd", uuid.NewString(), ident("EXT4ID", "ext4", "linux", ""))
	if physicalID(t, c) != physicalID(t, a) {
		t.Error("a missing serial should still match")
	}

	// A clone: same filesystem ID, every candidate's serial differs.
	clone := f.open(t, other, "clone", uuid.NewString(), ident("EXT4ID", "ext4", "linux", "S9"))
	if physicalID(t, clone) == physicalID(t, a) || clone.PhysicalDrive.CloneOf == nil || *clone.PhysicalDrive.CloneOf != physicalID(t, a) {
		t.Errorf("clone = %+v", clone.PhysicalDrive)
	}

	// No filesystem ID: not linked.
	if r := f.open(t, linux, "net", uuid.NewString(), ident("", "smbfs", "linux", "S1")); r.PhysicalDrive != nil {
		t.Errorf("no fs_uuid but linked: %+v", r.PhysicalDrive)
	}
}

func TestMatchingSameSourceRuleForExFAT(t *testing.T) {
	f := newFixture(t)
	linux, mac := f.agent(t, f.user, "l"), f.agent(t, f.user, "m")
	a := f.open(t, linux, "x", uuid.NewString(), ident("SAMEID", "exfat", "linux", ""))
	b := f.open(t, mac, "x", uuid.NewString(), ident("SAMEID", "exfat", "macos", ""))
	if physicalID(t, a) == physicalID(t, b) {
		t.Error("exFAT IDs from different OSes were linked")
	}
	// The same source still links.
	c := f.open(t, f.agent(t, f.user, "l2"), "x", uuid.NewString(), ident("SAMEID", "exfat", "linux", ""))
	if physicalID(t, c) != physicalID(t, a) {
		t.Error("exFAT from the same OS not linked")
	}
}

func TestSerialFilledInLater(t *testing.T) {
	f := newFixture(t)
	a := f.agent(t, f.user, "h")
	first := f.open(t, a, "d", uuid.NewString(), ident("ID1", "ntfs", "linux", ""))
	second := f.open(t, f.agent(t, f.user, "h2"), "d", uuid.NewString(), ident("ID1", "ntfs", "linux", "SER"))
	if physicalID(t, first) != physicalID(t, second) {
		t.Fatal("not linked")
	}
	var serial string
	f.st.Pool.QueryRow(ctx, `SELECT hw_serial FROM agent_physical_drives WHERE id = $1`, physicalID(t, first)).Scan(&serial)
	if serial != "SER" {
		t.Errorf("serial = %q, want it filled in", serial)
	}
}

func TestAmbiguousCandidates(t *testing.T) {
	f := newFixture(t)
	// Two physical drives with the same ID and no serials (made directly;
	// the normal flow would have linked them).
	var older, newer int64
	f.st.Pool.QueryRow(ctx, `INSERT INTO agent_physical_drives (user_id, fs_uuid, fs_type) VALUES ($1, 'AMB', 'ext4') RETURNING id`, f.user).Scan(&older)
	f.st.Pool.QueryRow(ctx, `INSERT INTO agent_physical_drives (user_id, fs_uuid, fs_type) VALUES ($1, 'AMB', 'ext4') RETURNING id`, f.user).Scan(&newer)

	a := f.agent(t, f.user, "h")
	if r := f.open(t, a, "d", uuid.NewString(), ident("AMB", "ext4", "linux", "")); physicalID(t, r) != older {
		t.Errorf("linked %d, want the oldest %d", physicalID(t, r), older)
	}
	// A drive already linked to the newer one keeps its link.
	b := f.agent(t, f.user, "h2")
	f.open(t, b, "d", uuid.NewString(), nil)
	f.st.Pool.Exec(ctx, `UPDATE agent_drives SET physical_drive_id = $1 WHERE agent_id = $2`, newer, b)
	if r := f.open(t, b, "d", uuid.NewString(), ident("AMB", "ext4", "linux", "")); physicalID(t, r) != newer {
		t.Errorf("linked %d, want the existing link %d", physicalID(t, r), newer)
	}
}

func TestUsersDontLink(t *testing.T) {
	f := newFixture(t)
	other, _ := f.st.CreateUser(ctx, "someone", "h")
	a := f.open(t, f.agent(t, f.user, "h"), "d", uuid.NewString(), ident("ID", "ext4", "linux", "S"))
	b, err := f.st.OpenDrive(ctx, other, f.agent(t, other, "h"), "d", wire.DriveOpenRequest{StreamID: uuid.NewString(), DriveRoot: "/x", Identity: ident("ID", "ext4", "linux", "S")})
	if err != nil {
		t.Fatal(err)
	}
	if physicalID(t, a) == physicalID(t, b) {
		t.Error("two users' drives were linked")
	}
}

// Two agents of one user open the same new drive at once: one physical drive.
func TestConcurrentOpenOfNewDrive(t *testing.T) {
	for round := 0; round < 5; round++ {
		f := newFixture(t)
		agents := []string{f.agent(t, f.user, "a"), f.agent(t, f.user, "b"), f.agent(t, f.user, "c")}
		var wg sync.WaitGroup
		for _, a := range agents {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := f.st.OpenDrive(ctx, f.user, a, "d", wire.DriveOpenRequest{StreamID: uuid.NewString(), DriveRoot: "/d",
					Identity: ident("NEWID", "ext4", "linux", "S")}); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		var n int
		f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_physical_drives`).Scan(&n)
		if n != 1 {
			t.Fatalf("round %d: %d physical drives, want 1", round, n)
		}
	}
}

// One agent opening the same new drive twice at once: one drive row.
func TestConcurrentOpenSameAgent(t *testing.T) {
	f := newFixture(t)
	a := f.agent(t, f.user, "a")
	stream := uuid.NewString()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.st.OpenDrive(ctx, f.user, a, "d", wire.DriveOpenRequest{StreamID: stream, DriveRoot: "/d"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var n int
	f.st.Pool.QueryRow(ctx, `SELECT count(*) FROM agent_drives`).Scan(&n)
	if n != 1 {
		t.Errorf("%d drive rows", n)
	}
}

func TestListDrives(t *testing.T) {
	f := newFixture(t)
	a, b := f.agent(t, f.user, "one"), f.agent(t, f.user, "two")
	f.open(t, a, "z", uuid.NewString(), ident("P", "ext4", "linux", ""))
	f.open(t, a, "a", uuid.NewString(), nil)
	f.open(t, b, "b", uuid.NewString(), ident("P", "ext4", "linux", ""))
	list, err := f.st.ListDrives(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].DriveID != "a" || list[1].DriveID != "z" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].PhysicalDrive != nil || list[1].PhysicalDrive == nil || len(list[1].PhysicalDrive.Linked) != 1 ||
		list[1].PhysicalDrive.Linked[0].Hostname != "two" {
		t.Errorf("physical = %+v / %+v", list[0].PhysicalDrive, list[1].PhysicalDrive)
	}
	if list[0].AckedRanges == nil {
		t.Error("acked_ranges should be [] not null")
	}
}
