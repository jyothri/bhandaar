// Package testutil holds helpers shared by the agent's tests.
package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Tree describes files to create, by slash-separated path relative to a root.
// A path ending in "/" is an empty directory; anything else is a file with
// the given content.
type Tree map[string]string

// WriteTree creates tree under root (creating root if needed). Every file
// gets the same fixed mtime, so tests don't depend on the clock.
func WriteTree(t testing.TB, root string, tree Tree) {
	t.Helper()
	for rel, content := range tree {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		WriteFile(t, p, content, BaseTime)
	}
}

// BaseTime is the mtime WriteTree gives every file.
var BaseTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// WriteFile writes one file, creating its parent directories, and sets its
// mtime.
func WriteFile(t testing.TB, path, content string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}
