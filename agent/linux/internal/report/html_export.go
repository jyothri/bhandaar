package report

import (
	"fmt"
	"strings"
)

// htmlChunkThreshold bounds how many nodes get inlined directly into a
// single JSON blob (the root document, or one lazily-loaded chunk). A
// report with millions of files would otherwise force the browser to
// parse and build DOM for the entire tree on load; instead, any subtree
// whose node count exceeds this is peeled off into its own <script>
// block and only parsed/rendered when the user actually expands into it.
const htmlChunkThreshold = 2000

// htmlNode is a compact, JSON-serializable mirror of TreeNode used only
// for the HTML report's embedded data. Field names are abbreviated since
// a large report can embed millions of these. ChunkID is set instead of
// Children when this node's subtree was too big to inline here.
type htmlNode struct {
	Name      string         `json:"n"`
	IsFile    bool           `json:"f,omitempty"`
	Category  string         `json:"c"`
	Counts    map[string]int `json:"cnt,omitempty"`
	Size      int64          `json:"s"`
	Partial   bool           `json:"p,omitempty"`
	CompDrive string         `json:"d,omitempty"`
	CompPath  string         `json:"r,omitempty"`
	Children  []*htmlNode    `json:"ch,omitempty"`
	ChunkID   string         `json:"x,omitempty"`
}

// htmlChunk is one deferred slice of the tree, embedded as its own
// <script type="application/json"> block, parsed client-side only on
// first expand.
type htmlChunk struct {
	ID   string
	Node *htmlNode
}

// exportHTMLTree converts root into the always-inline root node (its own
// identity fields are always populated; its Children are inlined only if
// small enough) plus whatever chunks were peeled off along the way.
func exportHTMLTree(driveID string, root *TreeNode) (*htmlNode, []htmlChunk) {
	var chunks []htmlChunk
	counter := 0

	var export func(n *TreeNode) (*htmlNode, int)
	export = func(n *TreeNode) (*htmlNode, int) {
		hn := &htmlNode{
			Name: n.Name, IsFile: n.IsFile, Category: n.Category, Counts: n.Counts,
			Size: n.Size, Partial: n.SizePartial,
			CompDrive: n.ComparedAgainstDriveID, CompPath: n.CounterpartRelativePath,
		}
		if n.IsFile || len(n.Children) == 0 {
			return hn, 1
		}
		children := make([]*htmlNode, 0, len(n.Children))
		weight := 1
		for _, c := range n.Children {
			cn, cw := export(c)
			children = append(children, cn)
			weight += cw
		}
		if weight > htmlChunkThreshold {
			counter++
			id := fmt.Sprintf("%s-%d", sanitizeForFilename(driveID), counter)
			chunks = append(chunks, htmlChunk{ID: id, Node: &htmlNode{
				Name: hn.Name, Category: hn.Category, Counts: hn.Counts,
				Size: hn.Size, Partial: hn.Partial, Children: children,
			}})
			hn.ChunkID = id
			return hn, 1
		}
		hn.Children = children
		return hn, weight
	}

	rootNode, _ := export(root)
	return rootNode, chunks
}

// sanitizeForFilename maps a drive ID (a user-chosen --drive-id value) to
// a string safe to use as a chunk ID and, in turn, a filename — letters,
// digits, '-' and '_' pass through; everything else becomes '_'.
func sanitizeForFilename(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('_')
		}
	}
	return sb.String()
}
