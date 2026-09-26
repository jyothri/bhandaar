package compare

import (
	"sort"
	"strings"

	"github.com/jyothri/bhandaar/agent/client/internal/store"
)

// precedence gives the worst-status-wins ordering for folder rollups
// (worst first): diverged > relocated > missing > common.
var precedence = map[string]int{
	store.ComparisonDiverged:  3,
	store.ComparisonRelocated: 2,
	store.ComparisonMissing:   1,
	store.ComparisonCommon:    0,
}

// propagate recomputes folder_status for every ancestor (up to and
// including drive_root, "") of every path in touchedRelPaths, deepest
// folder first so a parent always sees its children's freshly-written
// status. Returns how many distinct folders were recomputed.
func propagate(st *store.Store, driveID string, touchedRelPaths []string) (int, error) {
	seen := map[string]bool{}
	var ancestors []string
	for _, p := range touchedRelPaths {
		cur := parentOf(p)
		for {
			if !seen[cur] {
				seen[cur] = true
				ancestors = append(ancestors, cur)
			}
			if cur == "" {
				break
			}
			cur = parentOf(cur)
		}
	}
	sort.Slice(ancestors, func(i, j int) bool { return depthOf(ancestors[i]) > depthOf(ancestors[j]) })

	for _, folder := range ancestors {
		if err := recomputeFolder(st, driveID, folder); err != nil {
			return 0, err
		}
	}
	return len(ancestors), nil
}

// recomputeFolder rolls up one folder's status/counts from its direct
// children only — each child folder already carries its own correct,
// up-to-date rollup, either from earlier in this same propagate pass
// (deepest-first) or from an earlier compare run if untouched now.
func recomputeFolder(st *store.Store, driveID, folder string) error {
	children, err := st.ListChildren(driveID, folder)
	if err != nil {
		return err
	}
	if len(children) == 0 {
		return nil // nothing known here yet; nothing to compute
	}

	counts := map[string]int{}
	unscannedChildren := 0
	problemChildren := 0

	for _, c := range children {
		childPath := joinRel(folder, c.Name)
		if c.IsDir {
			fs, err := st.GetFolderStatus(driveID, childPath)
			if err != nil {
				return err
			}
			status := store.FolderUnscanned
			var childCounts map[string]int
			if fs != nil {
				status = fs.Status
				childCounts = fs.Counts
			}
			if status == store.FolderUnscanned {
				unscannedChildren++
			}
			if status == store.FolderUnscanned || status == store.FolderPartial {
				problemChildren++
			}
			for k, v := range childCounts {
				counts[k] += v
			}
		} else {
			cs, err := st.FileComparisonStatus(driveID, childPath)
			if err != nil {
				return err
			}
			if cs == "" {
				unscannedChildren++
				problemChildren++
			} else {
				counts[cs]++
			}
		}
	}

	var overall string
	switch {
	case unscannedChildren == len(children):
		overall = store.FolderUnscanned
	case problemChildren > 0:
		overall = store.FolderPartial
	default:
		overall = worstOf(counts)
	}

	return st.UpsertFolderStatus(driveID, folder, parentOf(folder), overall, counts)
}

func worstOf(counts map[string]int) string {
	worst := store.ComparisonCommon
	for status, n := range counts {
		if n > 0 && precedence[status] > precedence[worst] {
			worst = status
		}
	}
	return worst
}

func joinRel(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

func parentOf(relPath string) string {
	idx := strings.LastIndex(relPath, "/")
	if idx < 0 {
		return ""
	}
	return relPath[:idx]
}

func depthOf(relPath string) int {
	if relPath == "" {
		return 0
	}
	return strings.Count(relPath, "/") + 1
}
