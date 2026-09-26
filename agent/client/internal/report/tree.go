package report

import (
	"sort"
	"strconv"
	"strings"

	"github.com/jyothri/bhandaar/agent/client/internal/store"
)

// TreeNode is one row of the recursive folder view rendered in the HTML
// report — a pure read of already-computed data (store.FolderStatus /
// store.FileRecord.ComparisonStatus); report performs no rollup
// computation of its own.
type TreeNode struct {
	Name     string
	IsFile   bool
	Category string // folder: its persisted FolderStatus.Status. file: its ComparisonStatus ("unscanned" if never compared).
	Counts   map[string]int
	Children []*TreeNode

	// Size is the file's own size (IsFile) or, for a folder, the sum of
	// every descendant file's recorded size. Descendants that have never
	// been scanned contribute nothing (their size isn't known), so a
	// folder's Size is a lower bound whenever SizePartial is true.
	Size        int64
	SizePartial bool // folder only: true if Category is unscanned/partial, meaning Size excludes not-yet-scanned descendants.

	// Leaf detail (IsFile only).
	ComparedAgainstDriveID  string
	CounterpartRelativePath string // only meaningful when Category == relocated
}

// CountsText renders the per-category breakdown shown next to a non-common
// folder's badge, e.g. "2 diverged, 1 missing, 47 common".
func (n *TreeNode) CountsText() string {
	if n.IsFile || n.Category == store.ComparisonCommon || len(n.Counts) == 0 {
		return ""
	}
	order := []string{store.ComparisonDiverged, store.ComparisonRelocated, store.ComparisonMissing, store.ComparisonCommon}
	var parts []string
	for _, k := range order {
		if v := n.Counts[k]; v > 0 {
			parts = append(parts, strconv.Itoa(v)+" "+k)
		}
	}
	return strings.Join(parts, ", ")
}

// isMacMetadata reports whether name is a macOS-generated sidecar file
// (AppleDouble `._name` or Finder's `.DS_Store`) rather than real content.
func isMacMetadata(name string) bool {
	return strings.HasPrefix(name, "._") || name == ".DS_Store"
}

// buildTree reads the drive's whole folder/file structure starting at
// drive_root ("") and returns it as a tree, purely via lookups —
// dir_listings for structure, folder_status for folder rollups, and
// files.comparison_status for leaves. When includeMacMetadata is false,
// matching leaf entries are left out of Children (their contribution to
// an ancestor's persisted Counts/Category is not recomputed — this is a
// leaf-level-only filter, documented in docs/specs/drive-comparison-agent.md).
func buildTree(st *store.Store, driveID string, includeMacMetadata bool) (*TreeNode, error) {
	return buildNode(st, driveID, "", ".", includeMacMetadata)
}

func buildNode(st *store.Store, driveID, relPath, name string, includeMacMetadata bool) (*TreeNode, error) {
	fs, err := st.GetFolderStatus(driveID, relPath)
	if err != nil {
		return nil, err
	}
	node := &TreeNode{Name: name}
	if fs == nil {
		node.Category = store.FolderUnscanned
	} else {
		node.Category = fs.Status
		node.Counts = fs.Counts
	}

	children, err := st.ListChildren(driveID, relPath)
	if err != nil {
		return nil, err
	}
	for _, c := range children {
		if !includeMacMetadata && isMacMetadata(c.Name) {
			continue
		}
		childPath := c.Name
		if relPath != "" {
			childPath = relPath + "/" + c.Name
		}
		if c.IsDir {
			childNode, err := buildNode(st, driveID, childPath, c.Name, includeMacMetadata)
			if err != nil {
				return nil, err
			}
			node.Children = append(node.Children, childNode)
			continue
		}
		leaf, err := buildLeaf(st, driveID, childPath, c.Name)
		if err != nil {
			return nil, err
		}
		node.Children = append(node.Children, leaf)
	}
	sortChildren(node.Children)
	node.SizePartial = node.Category == store.FolderUnscanned || node.Category == store.FolderPartial
	for _, c := range node.Children {
		node.Size += c.Size
	}
	return node, nil
}

func buildLeaf(st *store.Store, driveID, relPath, name string) (*TreeNode, error) {
	leaf := &TreeNode{Name: name, IsFile: true, Category: store.FolderUnscanned}
	f, err := st.GetFile(driveID, relPath)
	if err != nil {
		return nil, err
	}
	if f == nil {
		return leaf, nil
	}
	leaf.Size = f.Size
	leaf.ComparedAgainstDriveID = f.ComparedAgainstDriveID
	leaf.CounterpartRelativePath = f.CounterpartRelativePath
	if f.ComparisonStatus != "" {
		leaf.Category = f.ComparisonStatus
	}
	return leaf, nil
}

func sortChildren(children []*TreeNode) {
	sort.Slice(children, func(i, j int) bool {
		a, b := children[i], children[j]
		if a.IsFile != b.IsFile {
			return !a.IsFile // folders before files
		}
		return a.Name < b.Name
	})
}
