package store

import (
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for the change feed (schema version 1).

func quietOpen(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := OpenWith(dir, Options{Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func (s *Store) clock(t *testing.T) int64 {
	t.Helper()
	var v int64
	if err := s.db.QueryRow(`SELECT v FROM sync_clock`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (s *Store) fileVersion(t *testing.T, drive, path string) int64 {
	t.Helper()
	var v int64
	if err := s.db.QueryRow(`SELECT row_version FROM files WHERE drive_id = ? AND relative_path = ?`, drive, path).Scan(&v); err != nil {
		t.Fatalf("file %s: %v", path, err)
	}
	return v
}

func (s *Store) dirVersion(t *testing.T, drive, parent, child string) int64 {
	t.Helper()
	var v int64
	if err := s.db.QueryRow(`SELECT row_version FROM dir_listings WHERE drive_id = ? AND relative_path = ? AND child_name = ?`,
		drive, parent, child).Scan(&v); err != nil {
		t.Fatalf("dir_listing %q/%q: %v", parent, child, err)
	}
	return v
}

// tombstone returns the tombstone's version, or 0 if there's none.
func (s *Store) tombstone(t *testing.T, drive, kind, path, child string) int64 {
	t.Helper()
	var v int64
	err := s.db.QueryRow(`SELECT row_version FROM sync_tombstones WHERE drive_id = ? AND kind = ? AND relative_path = ? AND child_name = ?`,
		drive, kind, path, child).Scan(&v)
	if err == sql.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// checkKeysOnce fails if any key is both a row and a tombstone.
func (s *Store) checkKeysOnce(t *testing.T) {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`
		SELECT count(*) FROM sync_tombstones t WHERE
		  (t.kind = 'file' AND EXISTS (SELECT 1 FROM files f WHERE f.drive_id = t.drive_id AND f.relative_path = t.relative_path))
		  OR (t.kind = 'dir_child' AND EXISTS (SELECT 1 FROM dir_listings d WHERE d.drive_id = t.drive_id
		        AND d.relative_path = t.relative_path AND d.child_name = t.child_name))`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d keys are both a row and a tombstone", n)
	}
}

func TestNewStoreIsCurrentSchema(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	var v int
	st.db.QueryRow(`SELECT version FROM schema_version`).Scan(&v)
	if v != SchemaVersion() || v != 1 {
		t.Errorf("schema version = %d", v)
	}
	if st.clock(t) != 0 {
		t.Errorf("clock = %d", st.clock(t))
	}
}

func TestUpsertFilesBumps(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	if err := st.UpsertFiles(ctx, []FileRecord{rec("d1", "a", 1, "h"), rec("d1", "b", 1, "h")}); err != nil {
		t.Fatal(err)
	}
	a1, b1 := st.fileVersion(t, "d1", "a"), st.fileVersion(t, "d1", "b")
	if a1 == 0 || b1 == 0 || a1 == b1 || st.clock(t) < max(a1, b1) {
		t.Errorf("versions a=%d b=%d clock=%d", a1, b1, st.clock(t))
	}
	if err := st.UpsertFiles(ctx, []FileRecord{rec("d1", "a", 2, "h2")}); err != nil {
		t.Fatal(err)
	}
	if a2 := st.fileVersion(t, "d1", "a"); a2 <= max(a1, b1) {
		t.Errorf("re-upsert version %d, want above %d", a2, max(a1, b1))
	}
}

func TestComparisonAndFolderStatusDontBump(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	st.UpsertFiles(ctx, []FileRecord{rec("d1", "a", 1, "h")})
	v, clock := st.fileVersion(t, "d1", "a"), st.clock(t)
	if err := st.UpdateComparisonStatuses(ctx, []ComparisonUpdate{{DriveID: "d1", RelPath: "a", ComparisonStatus: ComparisonCommon}}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFolderStatus("d1", "", "", ComparisonCommon, map[string]int{"common": 1}); err != nil {
		t.Fatal(err)
	}
	if st.fileVersion(t, "d1", "a") != v || st.clock(t) != clock {
		t.Error("a comparison or folder-status update bumped a version")
	}
}

func TestDeleteFilesTombstones(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	st.UpsertFiles(ctx, []FileRecord{rec("d1", "a", 1, "h"), rec("d1", "b", 1, "h")})
	va := st.fileVersion(t, "d1", "a")
	if err := st.DeleteFiles(ctx, "d1", []string{"a", "never-existed"}); err != nil {
		t.Fatal(err)
	}
	if tv := st.tombstone(t, "d1", "file", "a", ""); tv <= va {
		t.Errorf("tombstone version %d, want above %d", tv, va)
	}
	if st.tombstone(t, "d1", "file", "never-existed", "") != 0 {
		t.Error("tombstone for a file that had no row")
	}
	st.checkKeysOnce(t)

	// The file comes back: the row supersedes the tombstone.
	st.UpsertFiles(ctx, []FileRecord{rec("d1", "a", 1, "h")})
	if st.tombstone(t, "d1", "file", "a", "") != 0 {
		t.Error("re-inserted file kept its tombstone")
	}
	st.checkKeysOnce(t)
}

func TestSyncDirListingsVersions(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	listing := map[string][]DirChild{
		"":    {{Name: "a", IsDir: true}, {Name: "f"}},
		"a":   {{Name: "b", IsDir: true}},
		"a/b": {{Name: "x"}},
	}
	if err := st.SyncDirListings(ctx, "d1", listing, true); err != nil {
		t.Fatal(err)
	}
	vf := st.dirVersion(t, "d1", "", "f")
	clock := st.clock(t)

	// Seen again unchanged: last_seen_at only, no bump.
	if err := st.SyncDirListings(ctx, "d1", listing, true); err != nil {
		t.Fatal(err)
	}
	if st.dirVersion(t, "d1", "", "f") != vf || st.clock(t) != clock {
		t.Error("a last_seen_at touch bumped a version")
	}

	// f becomes a directory: bump.
	listing[""] = []DirChild{{Name: "a", IsDir: true}, {Name: "f", IsDir: true}}
	st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": listing[""]}, true)
	if v := st.dirVersion(t, "d1", "", "f"); v <= vf {
		t.Errorf("is_dir change: version %d, want above %d", v, vf)
	}

	// a disappears: its row and everything under it get tombstones.
	before := st.clock(t)
	st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": {{Name: "f", IsDir: true}}}, true)
	for _, k := range [][2]string{{"", "a"}, {"a", "b"}, {"a/b", "x"}} {
		if tv := st.tombstone(t, "d1", "dir_child", k[0], k[1]); tv <= before {
			t.Errorf("tombstone %q/%q = %d, want above %d", k[0], k[1], tv, before)
		}
	}
	st.checkKeysOnce(t)

	// a comes back: its tombstone goes.
	st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": {{Name: "a", IsDir: true}, {Name: "f", IsDir: true}}}, true)
	if st.tombstone(t, "d1", "dir_child", "", "a") != 0 {
		t.Error("re-inserted child kept its tombstone")
	}
	st.checkKeysOnce(t)
}

func TestSyncDirListingsWithoutDeleteStaleWritesNoTombstones(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": {{Name: "a"}, {Name: "b"}}}, false)
	st.SyncDirListings(ctx, "d1", map[string][]DirChild{"": {{Name: "a"}}}, false)
	var n int
	st.db.QueryRow(`SELECT count(*) FROM sync_tombstones`).Scan(&n)
	if n != 0 {
		t.Errorf("%d tombstones", n)
	}
}

func TestScanRunsBump(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	st.UpsertDrive("d1", "/mnt/d1", "")
	id, err := st.StartScanRun("d1")
	if err != nil {
		t.Fatal(err)
	}
	version := func() int64 {
		var v int64
		st.db.QueryRow(`SELECT row_version FROM scan_runs WHERE id = ?`, id).Scan(&v)
		return v
	}
	v1 := version()
	if err := st.FinishScanRun(id, "d1", 3, 30, false); err != nil {
		t.Fatal(err)
	}
	if v1 == 0 || version() <= v1 {
		t.Errorf("versions %d then %d", v1, version())
	}
}

func TestClearDriveResetsSyncState(t *testing.T) {
	st := quietOpen(t, t.TempDir())
	for _, d := range []string{"d1", "d2"} {
		st.UpsertDrive(d, "/mnt/"+d, "")
		st.UpsertFiles(ctx, []FileRecord{rec(d, "a", 1, "h"), rec(d, "b", 1, "h")})
		st.DeleteFiles(ctx, d, []string{"b"})
		st.db.Exec(`INSERT INTO sync_ranges VALUES (?, 10, 20)`, d)
		st.db.Exec(`INSERT INTO sync_rejected VALUES (?, 5, 'file', 'x', '', 'bad', ?)`, d, time.Now())
		st.db.Exec(`UPDATE drives SET sync_stream_id = 'stream', synced_version = 7, synced_at = ? WHERE drive_id = ?`, time.Now(), d)
	}
	if err := st.ClearDrive("d1"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"sync_tombstones", "sync_ranges", "sync_rejected", "files"} {
		var n1, n2 int
		st.db.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE drive_id = 'd1'`).Scan(&n1)
		st.db.QueryRow(`SELECT count(*) FROM ` + table + ` WHERE drive_id = 'd2'`).Scan(&n2)
		if n1 != 0 || n2 == 0 {
			t.Errorf("%s: d1 has %d rows, d2 has %d", table, n1, n2)
		}
	}
	var stream sql.NullString
	var synced int64
	var at sql.NullTime
	st.db.QueryRow(`SELECT sync_stream_id, synced_version, synced_at FROM drives WHERE drive_id = 'd1'`).Scan(&stream, &synced, &at)
	if stream.Valid || synced != 0 || at.Valid {
		t.Errorf("d1 sync columns not reset: %v %d %v", stream, synced, at)
	}
	st.db.QueryRow(`SELECT sync_stream_id FROM drives WHERE drive_id = 'd2'`).Scan(&stream)
	if stream.String != "stream" {
		t.Error("d2's stream was reset")
	}
}

// oldStateDB creates a state.db as a driveagent from before schema
// versioning left it, with some data.
func oldStateDB(t *testing.T, dir string, files int) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range baseSchema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	mustExec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO drives (drive_id, drive_root) VALUES ('d1', '/mnt/d1')`)
	mustExec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO files (drive_id, relative_path, size, mtime_unix, mode, status, scanned_at)
		SELECT 'd1', 'f' || i, i, 0, 420, 'hashed', '2026-01-01' FROM n`, files)
	mustExec(`INSERT INTO dir_listings VALUES ('d1', '', 'x', 1, '2026-01-01', '2026-01-01'), ('d1', 'x', 'y', 0, '2026-01-01', '2026-01-01')`)
	mustExec(`INSERT INTO scan_runs (drive_id, started_at, finished_at, interrupted) VALUES ('d1', '2026-01-01', '2026-01-01', 0)`)
}

func TestBackfill(t *testing.T) {
	dir := t.TempDir()
	oldStateDB(t, dir, 5)
	var log bytes.Buffer
	st, err := OpenWith(dir, Options{Log: &log})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if !strings.Contains(log.String(), "upgrading state.db (one-time, 8 rows)") {
		t.Errorf("log = %q", log.String())
	}

	// Every row numbered, distinct, increasing in rowid order within each
	// table, and the clock at the maximum.
	rows, err := st.db.Query(`
		SELECT 'files', rowid, row_version FROM files
		UNION ALL SELECT 'dir_listings', rowid, row_version FROM dir_listings
		UNION ALL SELECT 'scan_runs', rowid, row_version FROM scan_runs
		ORDER BY 1, 2`)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	var maxV int64
	last := map[string]int64{}
	for rows.Next() {
		var table string
		var rowid, v int64
		rows.Scan(&table, &rowid, &v)
		if v <= 0 || seen[v] || v <= last[table] {
			t.Errorf("%s rowid %d: version %d (duplicate, zero or out of order)", table, rowid, v)
		}
		seen[v], last[table] = true, v
		maxV = max(maxV, v)
	}
	rows.Close()
	if len(seen) != 8 || st.clock(t) != maxV {
		t.Errorf("%d versions, clock %d, max %d", len(seen), st.clock(t), maxV)
	}

	// New writes go above the backfill.
	st.UpsertFiles(ctx, []FileRecord{rec("d1", "new", 1, "h")})
	if st.fileVersion(t, "d1", "new") <= maxV {
		t.Error("new write not above the backfilled history")
	}
	f1, clock := st.fileVersion(t, "d1", "f1"), st.clock(t)
	st.Close()

	// Re-opening is a no-op.
	log.Reset()
	st2, err := OpenWith(dir, Options{Log: &log})
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if log.Len() != 0 || st2.fileVersion(t, "d1", "f1") != f1 || st2.clock(t) != clock {
		t.Errorf("re-open changed something; logged %q", log.String())
	}
}

// Progress is printed per chunk of rows. (Measured separately: 250,000 rows
// backfill in about 2 s; under -race, pure-Go SQLite takes ~70 s, so the
// test uses small chunks instead of a large file.)
func TestBackfillReportsProgress(t *testing.T) {
	old := backfillChunk
	backfillChunk = 1000
	t.Cleanup(func() { backfillChunk = old })
	dir := t.TempDir()
	oldStateDB(t, dir, 2500)
	var log bytes.Buffer
	st, err := OpenWith(dir, Options{Log: &log})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	want := "upgrading state.db (one-time, 2503 rows)…\n  1000 / 2503 rows\n  2000 / 2503 rows\nupgraded state.db (2503 rows)\n"
	if log.String() != want {
		t.Errorf("log = %q\nwant  %q", log.String(), want)
	}
	var distinct, zero int
	st.db.QueryRow(`SELECT count(DISTINCT row_version), count(*) FILTER (WHERE row_version = 0) FROM files`).Scan(&distinct, &zero)
	if distinct != 2500 || zero != 0 {
		t.Errorf("distinct=%d zero=%d", distinct, zero)
	}
}

func TestMigrationGuardsExistingColumns(t *testing.T) {
	// A column already present (e.g. added by hand) doesn't break the ALTERs.
	dir := t.TempDir()
	oldStateDB(t, dir, 1)
	db, _ := sql.Open("sqlite", filepath.Join(dir, "state.db"))
	if _, err := db.Exec(`ALTER TABLE files ADD COLUMN row_version INTEGER NOT NULL DEFAULT 0`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	quietOpen(t, dir)
}

func TestNewerSchemaRefused(t *testing.T) {
	dir := t.TempDir()
	quietOpen(t, dir).db.Exec(`UPDATE schema_version SET version = 99`)
	if _, err := OpenWith(dir, Options{Log: io.Discard}); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("err = %v", err)
	}
}

// Two processes (two *sql.DB handles on one file) write the feed at once:
// no SQLITE_BUSY_SNAPSHOT, every version distinct, and versions become
// visible in order: once a reader has seen version V, no entry at or below
// V appears later.
func TestConcurrentWritersFromTwoHandles(t *testing.T) {
	dir := t.TempDir()
	a, b := quietOpen(t, dir), quietOpen(t, dir)
	reader := quietOpen(t, dir)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	writer := func(st *Store, drive string) {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			p := fmt.Sprintf("p%d", i)
			if err := st.UpsertFiles(ctx, []FileRecord{rec(drive, p, 1, "h"), rec(drive, p+"x", 1, "h")}); err != nil {
				errs <- err
				return
			}
			// Read-then-write, the case BEGIN IMMEDIATE is for.
			listing := map[string][]DirChild{"": {{Name: p}}, p: {{Name: "c" + p}}}
			if err := st.SyncDirListings(ctx, drive, listing, i%2 == 0); err != nil {
				errs <- err
				return
			}
			if i%3 == 0 {
				if err := st.DeleteFiles(ctx, drive, []string{p + "x"}); err != nil {
					errs <- err
					return
				}
			}
		}
	}
	const feedCount = `SELECT
		(SELECT count(*) FROM files WHERE row_version <= ?1) + (SELECT count(*) FROM dir_listings WHERE row_version <= ?1) +
		(SELECT count(*) FROM sync_tombstones WHERE row_version <= ?1)`
	var readerErr error
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		// Tombstones replace deleted rows at a higher version, so an entry
		// at or below a seen version can disappear, never appear.
		var seenMax, seenCount int64
		for {
			select {
			case <-stop:
				return
			default:
			}
			tx, err := reader.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				readerErr = err
				return
			}
			var hi, cnt int64
			tx.QueryRow(`SELECT v FROM sync_clock`).Scan(&hi)
			tx.QueryRow(feedCount, seenMax).Scan(&cnt)
			tx.Rollback()
			if seenMax > 0 && cnt > seenCount {
				readerErr = fmt.Errorf("entries at or below version %d grew from %d to %d", seenMax, seenCount, cnt)
				return
			}
			tx, _ = reader.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
			tx.QueryRow(feedCount, hi).Scan(&cnt)
			tx.Rollback()
			seenMax, seenCount = hi, cnt
		}
	}()
	wg.Add(4)
	go writer(a, "d1")
	go writer(b, "d2")
	go writer(a, "d3")
	go writer(b, "d4")
	wg.Wait()
	close(stop)
	<-readerDone
	close(errs)
	for err := range errs {
		t.Errorf("writer: %v", err)
	}
	if readerErr != nil {
		t.Error(readerErr)
	}

	var total, distinct int
	reader.db.QueryRow(`SELECT count(*), count(DISTINCT v) FROM (
		SELECT row_version v FROM files UNION ALL SELECT row_version FROM dir_listings
		UNION ALL SELECT row_version FROM sync_tombstones)`).Scan(&total, &distinct)
	if total != distinct {
		t.Errorf("%d feed entries but %d distinct versions", total, distinct)
	}
	reader.checkKeysOnce(t)
}
