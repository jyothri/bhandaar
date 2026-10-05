package scan

import (
	"context"
	"crypto/md5"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"lukechampine.com/blake3"

	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/client/internal/testutil"
)

// These tests pin scan's behaviour before remote sync (PR 3) restructures
// its preflight.

func open(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func hashOf(s string) string {
	sum := blake3.Sum256([]byte(s))
	return fmt.Sprintf("%x", sum[:])
}

func run(t *testing.T, st *store.Store, opts Options) Stats {
	t.Helper()
	if opts.DriveID == "" {
		opts.DriveID = "d1"
	}
	p, err := Prepare(st, opts)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	stats, err := Run(context.Background(), st, p, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return stats
}

func paths(t *testing.T, st *store.Store) []string {
	t.Helper()
	ps, err := st.ListFileRelativePaths("d1")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(ps)
	return ps
}

func childNames(t *testing.T, st *store.Store, parent string) []string {
	t.Helper()
	cs, err := st.ListChildren("d1", parent)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		n := c.Name
		if c.IsDir {
			n += "/"
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func same(a, b []string) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

var tree = testutil.Tree{
	"a.txt":         "alpha",
	"docs/b.txt":    "bravo",
	"docs/c.txt":    "charlie",
	"docs/deep/d":   "delta",
	"empty/":        "",
	"photos/e.jpg":  "echo",
	"photos/f.jpeg": "foxtrot",
}

func TestFirstScanHashesEverything(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)

	s := run(t, st, Options{RootPath: root})
	if s.FilesSeen != 6 || s.FilesHashed != 6 || s.FilesSkipped != 0 || s.FilesErrored != 0 || s.FilesDeleted != 0 || s.DeletionsSkipped {
		t.Errorf("stats = %+v", s)
	}
	if s.BytesHashed != int64(len("alpha")+len("bravo")+len("charlie")+len("delta")+len("echo")+len("foxtrot")) {
		t.Errorf("bytes hashed = %d", s.BytesHashed)
	}
	f, err := st.GetFile("d1", "docs/b.txt")
	if err != nil || f == nil {
		t.Fatalf("GetFile: %v %v", f, err)
	}
	if f.ContentHash != hashOf("bravo") || f.HashAlgo != "blake3" || f.Status != store.StatusHashed ||
		f.Size != 5 || f.MTimeUnix != testutil.BaseTime.Unix() {
		t.Errorf("record = %+v", f)
	}
	if root2, _ := st.DriveRoot("d1"); root2 != root {
		t.Errorf("drive root = %q, want %q", root2, root)
	}
	if got := childNames(t, st, ""); !same(got, []string{"a.txt", "docs/", "empty/", "photos/"}) {
		t.Errorf("root listing = %v", got)
	}
	if got := childNames(t, st, "docs"); !same(got, []string{"b.txt", "c.txt", "deep/"}) {
		t.Errorf("docs listing = %v", got)
	}
}

func TestRescanSkipsUnchanged(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)
	run(t, st, Options{RootPath: root})

	s := run(t, st, Options{RootPath: root})
	if s.FilesSeen != 6 || s.FilesSkipped != 6 || s.FilesHashed != 0 || s.FilesDeleted != 0 {
		t.Errorf("rescan stats = %+v", s)
	}
}

func TestRescanPicksUpChanges(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)
	run(t, st, Options{RootPath: root})

	// Change one file (new size and mtime), delete one, delete a directory,
	// add one.
	testutil.WriteFile(t, filepath.Join(root, "a.txt"), "alpha, changed", testutil.BaseTime.Add(time.Hour))
	if err := os.Remove(filepath.Join(root, "docs/c.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "docs/deep")); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, filepath.Join(root, "photos/g.png"), "golf", testutil.BaseTime)

	s := run(t, st, Options{RootPath: root})
	if s.FilesSeen != 5 || s.FilesHashed != 2 || s.FilesSkipped != 3 || s.FilesDeleted != 2 || s.DeletionsSkipped {
		t.Errorf("stats = %+v", s)
	}
	if got := paths(t, st); !same(got, []string{"a.txt", "docs/b.txt", "photos/e.jpg", "photos/f.jpeg", "photos/g.png"}) {
		t.Errorf("files = %v", got)
	}
	if f, _ := st.GetFile("d1", "a.txt"); f.ContentHash != hashOf("alpha, changed") {
		t.Errorf("a.txt not re-hashed: %+v", f)
	}
	if got := childNames(t, st, "docs"); !same(got, []string{"b.txt"}) {
		t.Errorf("docs listing = %v", got)
	}
	if got := childNames(t, st, "docs/deep"); len(got) != 0 {
		t.Errorf("removed directory's listing survived: %v", got)
	}
}

func TestSameSizeAndMtimeIsSkipped(t *testing.T) {
	// Scan trusts (size, mtime): a same-size edit that keeps the mtime isn't re-hashed.
	root := t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"a": "aaaa"})
	st := open(t)
	run(t, st, Options{RootPath: root})
	testutil.WriteFile(t, filepath.Join(root, "a"), "bbbb", testutil.BaseTime)
	if s := run(t, st, Options{RootPath: root}); s.FilesSkipped != 1 || s.FilesHashed != 0 {
		t.Errorf("stats = %+v", s)
	}
}

func TestSubfolderScanUnderDriveRoot(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)

	s := run(t, st, Options{RootPath: filepath.Join(root, "docs"), DriveRoot: root, BackupRoot: "docs"})
	if s.FilesSeen != 3 {
		t.Errorf("stats = %+v", s)
	}
	if got := paths(t, st); !same(got, []string{"docs/b.txt", "docs/c.txt", "docs/deep/d"}) {
		t.Errorf("files = %v (paths are relative to the drive root)", got)
	}
	// The ancestors are listed (one level), so siblings are known.
	if got := childNames(t, st, ""); !same(got, []string{"a.txt", "docs/", "empty/", "photos/"}) {
		t.Errorf("root listing = %v", got)
	}
	if b, _ := st.BackupRoot("d1"); b != "docs" {
		t.Errorf("backup root = %q", b)
	}

	// A later scan of another subfolder accumulates, and doesn't delete docs/.
	run(t, st, Options{RootPath: filepath.Join(root, "photos"), DriveRoot: root})
	if got := paths(t, st); len(got) != 5 {
		t.Errorf("files after second subfolder = %v", got)
	}
}

func TestPathOutsideDriveRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	st := open(t)
	if _, err := Prepare(st, Options{DriveID: "d1", RootPath: other, DriveRoot: root}); err == nil {
		t.Error("a --path outside --drive-root should fail")
	}
}

func TestRootConflict(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, rootA, testutil.Tree{"a": "from A"})
	testutil.WriteTree(t, rootB, testutil.Tree{"b": "from B"})
	st := open(t)
	run(t, st, Options{RootPath: rootA})

	_, err := Prepare(st, Options{DriveID: "d1", RootPath: rootB})
	var conflict *RootConflict
	if !errors.As(err, &conflict) || conflict.OldRoot != rootA || conflict.NewRoot != rootB {
		t.Fatalf("err = %v, want RootConflict", err)
	}
	if got := paths(t, st); !same(got, []string{"a"}) {
		t.Errorf("a refused scan changed the checkpoint: %v", got)
	}

	// --replace-root discards the old root's data first.
	run(t, st, Options{RootPath: rootB, ReplaceRoot: true})
	if got := paths(t, st); !same(got, []string{"b"}) {
		t.Errorf("after replace-root: %v", got)
	}
	if r, _ := st.DriveRoot("d1"); r != rootB {
		t.Errorf("drive root = %q", r)
	}
}

// --replace-root clears the old root's data in Prepare, before Run walks.
func TestReplaceRootClearsInPrepare(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, rootA, testutil.Tree{"a": "from A"})
	testutil.WriteTree(t, rootB, testutil.Tree{"b": "from B"})
	st := open(t)
	run(t, st, Options{RootPath: rootA})

	p, err := Prepare(st, Options{DriveID: "d1", RootPath: rootB, ReplaceRoot: true})
	if err != nil || !p.Replaced {
		t.Fatalf("Prepare: %+v, %v", p, err)
	}
	if got := paths(t, st); len(got) != 0 {
		t.Errorf("old root's files still there after Prepare: %v", got)
	}
}

func TestRunNeedsPrepare(t *testing.T) {
	root := t.TempDir()
	st := open(t)
	p := Prepared{DriveID: "d1", ScanPath: root, DriveRoot: root}
	if _, err := Run(context.Background(), st, p, Options{}); err == nil {
		t.Error("Run without a recorded drive should fail")
	}
}

func TestUnreadableDirectoryDisablesDeletion(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read everything")
	}
	root := t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"keep": "k", "gone": "g", "locked/x": "x"})
	st := open(t)
	run(t, st, Options{RootPath: root})

	if err := os.Remove(filepath.Join(root, "gone")); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(root, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	s := run(t, st, Options{RootPath: root})
	if !s.DeletionsSkipped || s.FilesDeleted != 0 {
		t.Errorf("stats = %+v, want deletions skipped", s)
	}
	if got := paths(t, st); !same(got, []string{"gone", "keep", "locked/x"}) {
		t.Errorf("files = %v; nothing should be deleted", got)
	}

	// Once readable again, a clean scan deletes.
	os.Chmod(locked, 0o755)
	if s := run(t, st, Options{RootPath: root}); s.DeletionsSkipped || s.FilesDeleted != 1 {
		t.Errorf("clean rescan stats = %+v", s)
	}
}

func TestUnreadableFileIsRecordedAsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read everything")
	}
	root := t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"ok": "fine", "secret": "no"})
	secret := filepath.Join(root, "secret")
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(secret, 0o644) })
	st := open(t)

	s := run(t, st, Options{RootPath: root})
	if s.FilesErrored != 1 || s.FilesHashed != 1 || s.DeletionsSkipped {
		t.Errorf("stats = %+v", s)
	}
	f, _ := st.GetFile("d1", "secret")
	if f == nil || f.Status != store.StatusError || f.ErrorMessage == "" {
		t.Errorf("record = %+v", f)
	}
	// An errored file is retried on the next scan, even though it's unchanged.
	os.Chmod(secret, 0o644)
	if s := run(t, st, Options{RootPath: root}); s.FilesHashed != 1 || s.FilesSkipped != 1 {
		t.Errorf("rescan stats = %+v", s)
	}
}

// A cancel during the walk (Ctrl-C) is an interruption, not a failure.
// (Before PR 3 it surfaced as the walk's own context.Canceled, so the CLI
// printed "error: context canceled" instead of the resume hint.)
func TestCancelledScanIsInterrupted(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)
	opts := Options{DriveID: "d1", RootPath: root}
	p, err := Prepare(st, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err := Run(ctx, st, p, opts)
	var in *Interrupted
	if !errors.As(err, &in) {
		t.Fatalf("err = %v, want *Interrupted", err)
	}
	if !s.DeletionsSkipped {
		t.Error("an interrupted scan must not detect deletions")
	}
}

func TestMissingPathIsInterrupted(t *testing.T) {
	st := open(t)
	_, err := Prepare(st, Options{DriveID: "d1", RootPath: filepath.Join(t.TempDir(), "unplugged")})
	var in *Interrupted
	if !errors.As(err, &in) {
		t.Fatalf("err = %v, want Interrupted", err)
	}
}

func TestSymlinksAreSkipped(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, testutil.Tree{"real": "r"})
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	st := open(t)
	if s := run(t, st, Options{RootPath: root}); s.FilesSeen != 1 {
		t.Errorf("stats = %+v", s)
	}
	if got := paths(t, st); !same(got, []string{"real"}) {
		t.Errorf("files = %v", got)
	}
}

// OnFlush is called after each committed batch of file records, once they
// are readable.
func TestOnFlushAfterEachBatch(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)
	var flushes, visible []int
	opts := Options{RootPath: root, BatchSize: 2}
	opts.OnFlush = func() {
		ps, _ := st.ListFileRelativePaths("d1")
		flushes = append(flushes, len(flushes)+1)
		visible = append(visible, len(ps))
	}
	run(t, st, opts)
	if len(flushes) != 3 {
		t.Errorf("%d flushes for 6 files in batches of 2", len(flushes))
	}
	for i, n := range visible {
		if n < 2*(i+1) {
			t.Errorf("flush %d: only %d rows visible", i+1, n)
		}
	}
}

func md5Of(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

func TestScanRecordsMD5(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)
	run(t, st, Options{RootPath: root})

	f, err := st.GetFile("d1", "docs/b.txt")
	if err != nil || f == nil || f.MD5 != md5Of("bravo") || f.ContentHash != hashOf("bravo") {
		t.Fatalf("record = %+v, %v; want both hashes of bravo", f, err)
	}
}

func TestRescanAddsMissingMD5Once(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)
	run(t, st, Options{RootPath: root})

	// As a scan before 0.6.0 left them: hashed, with no MD5.
	files, err := st.ListFiles("d1", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := range files {
		files[i].MD5 = ""
	}
	if err := st.UpsertFiles(context.Background(), files); err != nil {
		t.Fatal(err)
	}

	// The next scan re-reads every file, though none changed...
	s := run(t, st, Options{RootPath: root})
	if s.FilesHashed != 6 || s.FilesSkipped != 0 {
		t.Errorf("stats = %+v; want every file re-read for its MD5", s)
	}
	if f, _ := st.GetFile("d1", "a.txt"); f == nil || f.MD5 != md5Of("alpha") {
		t.Errorf("a.txt = %+v, want its MD5", f)
	}
	// ...and the one after skips them again.
	if s := run(t, st, Options{RootPath: root}); s.FilesSkipped != 6 || s.FilesHashed != 0 {
		t.Errorf("third scan stats = %+v", s)
	}
}

// lockDB takes state.db's write lock from a connection of its own, as
// another driveagent process's long write transaction would (a big drive's
// end-of-scan listing sync), and returns its release.
func lockDB(t *testing.T, stateDir string) (release func()) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(stateDir, "state.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release = func() {
		once.Do(func() {
			conn.ExecContext(context.Background(), "ROLLBACK")
			conn.Close()
			db.Close()
		})
	}
	t.Cleanup(release)
	return release
}

// shortWaits makes state.db's lock waits and the batch retries short.
// store.BusyTimeout applies to stores opened afterwards.
func shortWaits(t *testing.T, retryFor time.Duration) {
	t.Helper()
	oldBusy, oldFor, oldPause := store.BusyTimeout, recordRetryFor, recordRetryPause
	store.BusyTimeout, recordRetryFor, recordRetryPause = 50*time.Millisecond, retryFor, 20*time.Millisecond
	t.Cleanup(func() { store.BusyTimeout, recordRetryFor, recordRetryPause = oldBusy, oldFor, oldPause })
}

func manyFiles(t *testing.T, n int) (root string, files testutil.Tree) {
	t.Helper()
	root = t.TempDir()
	files = testutil.Tree{}
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("d%02d/f%04d.txt", i%10, i)] = fmt.Sprintf("content %d", i)
	}
	testutil.WriteTree(t, root, files)
	return root, files
}

// While another process holds state.db's write lock for longer than a
// write waits, the scan retries its batch instead of dropping it: every
// file ends up recorded. (Before, the batch was dropped, the scan carried
// on, and its files were never uploaded.)
func TestABatchThatFindsTheDBLockedIsRetried(t *testing.T) {
	shortWaits(t, 30*time.Second)
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root, files := manyFiles(t, 60)

	var once sync.Once
	opts := Options{DriveID: "d1", RootPath: root, BatchSize: 5}
	opts.OnFlush = func() {
		once.Do(func() {
			release := lockDB(t, dir)
			time.AfterFunc(500*time.Millisecond, release)
		})
	}
	p, err := Prepare(st, opts)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := Run(context.Background(), st, p, opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := len(paths(t, st)); got != len(files) || stats.FilesHashed != int64(len(files)) {
		t.Errorf("recorded %d of %d files (hashed %d)", got, len(files), stats.FilesHashed)
	}
}

// A lock that outlasts the retries stops the scan with an error, rather
// than carrying on past files it couldn't record.
func TestABatchThatCantBeRecordedStopsTheScan(t *testing.T) {
	shortWaits(t, 200*time.Millisecond)
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	root, files := manyFiles(t, 400)

	var once sync.Once
	release := func() {}
	opts := Options{DriveID: "d1", RootPath: root, BatchSize: 5}
	opts.OnFlush = func() { once.Do(func() { release = lockDB(t, dir) }) }
	p, err := Prepare(st, opts)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = Run(context.Background(), st, p, opts)
	if err == nil || !strings.Contains(err.Error(), "writing checkpoint db, so the scan stopped") || !store.IsBusy(err) {
		t.Fatalf("Run = %v, want the batch's lock error", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("stopped after %v", d)
	}
	release()
	if got := len(paths(t, st)); got >= len(files) {
		t.Errorf("recorded all %d files, so nothing was refused", got)
	}
}
