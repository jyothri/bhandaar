package report

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jyothri/bhandaar/agent/linux/internal/compare"
)

// Per spec §7.1: folder status is the worst category found anywhere in its
// subtree, using this precedence (worst first).
const (
	catError    = "error"
	catDiverged = "diverged"
	catMissing  = "missing"
	catCommon   = "common"
)

var precedence = map[string]int{
	catError:    3,
	catDiverged: 2,
	catMissing:  1,
	catCommon:   0,
}

// TreeNode is one row of the recursive folder-rollup tree rendered in the
// HTML report. A leaf (IsFile) is a single file; an internal node is a
// folder whose Category/Counts are rolled up bottom-up from its children.
// Relocated entries are intentionally never inserted into this tree — see
// spec §7.1 ("Relocated files — kept out of the tree").
type TreeNode struct {
	Name     string
	IsFile   bool
	Category string // file: its own category. folder: rolled-up worst category of its subtree.
	Counts   map[string]int
	Children []*TreeNode

	// Leaf detail, populated according to Category.
	Size      int64
	SizeA     int64
	HashA     string
	SizeB     int64
	HashB     string
	PresentOn string
	Drive     string
	Message   string

	childIndex map[string]*TreeNode // build-time only, for de-duplicating folder segments
}

// CountsText renders the per-category breakdown shown next to a non-common
// folder's badge, e.g. "2 diverged, 1 missing, 47 common". Per spec, common
// folders don't show a breakdown (there's nothing to break down).
func (n *TreeNode) CountsText() string {
	if n.IsFile || n.Category == catCommon {
		return ""
	}
	order := []string{catError, catDiverged, catMissing, catCommon}
	var parts []string
	for _, k := range order {
		if v := n.Counts[k]; v > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", v, k))
		}
	}
	return strings.Join(parts, ", ")
}

// BuildTree groups every Common/Diverged/Missing/ScanErrors entry from res
// into a recursive folder tree keyed by relative-path segments, with each
// folder's Category/Counts rolled up bottom-up per spec §7.1.
func BuildTree(res compare.Result) *TreeNode {
	// Named "." (the scan root) rather than left empty, since the root
	// itself is rendered as the top-level rollup node — if everything
	// under it matches, the whole report collapses to one "." / common
	// line instead of listing every top-level folder individually.
	root := newFolderNode(".")

	for _, c := range res.Common {
		insertLeaf(root, c.RelPath, &TreeNode{Category: catCommon, Size: c.Size})
	}
	for _, d := range res.Diverged {
		insertLeaf(root, d.RelPath, &TreeNode{
			Category: catDiverged,
			SizeA:    d.SizeA, HashA: d.HashA,
			SizeB: d.SizeB, HashB: d.HashB,
		})
	}
	for _, m := range res.Missing {
		insertLeaf(root, m.RelPath, &TreeNode{Category: catMissing, Size: m.Size, PresentOn: m.PresentOn})
	}
	for _, e := range res.ScanErrors {
		insertLeaf(root, e.RelPath, &TreeNode{Category: catError, Drive: e.Drive, Message: e.Message})
	}

	rollup(root)
	sortChildren(root)
	return root
}

func newFolderNode(name string) *TreeNode {
	return &TreeNode{Name: name, childIndex: make(map[string]*TreeNode)}
}

// insertLeaf walks/creates the folder chain for relPath's directory
// segments and attaches leaf as the final path component.
func insertLeaf(root *TreeNode, relPath string, leaf *TreeNode) {
	parts := strings.Split(relPath, "/")
	cur := root
	for _, part := range parts[:len(parts)-1] {
		child, ok := cur.childIndex[part]
		if !ok {
			child = newFolderNode(part)
			cur.childIndex[part] = child
			cur.Children = append(cur.Children, child)
		}
		cur = child
	}
	leaf.Name = parts[len(parts)-1]
	leaf.IsFile = true
	cur.childIndex[leaf.Name] = leaf
	cur.Children = append(cur.Children, leaf)
}

// rollup computes each folder's Category (worst status in its subtree) and
// Counts (category -> count across the whole subtree), bottom-up.
func rollup(n *TreeNode) {
	if n.IsFile {
		return
	}
	counts := map[string]int{}
	worst := catCommon
	for _, c := range n.Children {
		rollup(c)
		if c.IsFile {
			counts[c.Category]++
		} else {
			for k, v := range c.Counts {
				counts[k] += v
			}
		}
		if precedence[c.Category] > precedence[worst] {
			worst = c.Category
		}
	}
	n.Counts = counts
	n.Category = worst
}

// sortChildren orders each folder's children deterministically: subfolders
// before files, alphabetically within each group.
func sortChildren(n *TreeNode) {
	if n.IsFile {
		return
	}
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsFile != b.IsFile {
			return !a.IsFile
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		sortChildren(c)
	}
}
