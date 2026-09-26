package scan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	stats, err := Run(context.Background(), st, opts)
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
	if _, err := Run(context.Background(), st, Options{DriveID: "d1", RootPath: other, DriveRoot: root}); err == nil {
		t.Error("a --path outside --drive-root should fail")
	}
}

func TestRootConflict(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	testutil.WriteTree(t, rootA, testutil.Tree{"a": "from A"})
	testutil.WriteTree(t, rootB, testutil.Tree{"b": "from B"})
	st := open(t)
	run(t, st, Options{RootPath: rootA})

	_, err := Run(context.Background(), st, Options{DriveID: "d1", RootPath: rootB})
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

// A cancel during the walk (Ctrl-C) currently surfaces as the walk's own
// context.Canceled, not *Interrupted, so main prints "error: context
// canceled" and exits 1 instead of the resume hint. Only a cancel after the
// walk gives *Interrupted. PR 3 replaces this with exit code 130; this test
// pins today's behaviour until then.
func TestCancelledScan(t *testing.T) {
	root := t.TempDir()
	testutil.WriteTree(t, root, tree)
	st := open(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s, err := Run(ctx, st, Options{DriveID: "d1", RootPath: root})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !s.DeletionsSkipped {
		t.Error("an interrupted scan must not detect deletions")
	}
}

func TestMissingPathIsInterrupted(t *testing.T) {
	st := open(t)
	_, err := Run(context.Background(), st, Options{DriveID: "d1", RootPath: filepath.Join(t.TempDir(), "unplugged")})
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
