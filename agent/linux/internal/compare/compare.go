// Package compare computes and persists comparison status between two
// drives: for a scoped set of paths, it classifies files as common,
// diverged, or missing by exact (backup-root-aligned) path match; runs a
// global relocated-detection pass over every currently-missing file on
// both drives; and incrementally maintains a folder-level status rollup
// by walking each touched file's ancestor chain up to drive_root. See
// docs/specs/drive-comparison-agent.md for the full design.
package compare

import (
	"context"
	"sort"
	"strings"

	"github.com/jyothri/bhandaar/agent/linux/internal/store"
)

// Options scopes a compare run. PathsA/PathsB are backup-root-relative
// paths to (re)check; empty means "the whole drive." Matching itself
// always uses complete knowledge of both drives (a file's status can only
// be determined by looking at both sides), but PathsA/PathsB determine
// which backup-relative paths are considered "in play" this run — applied
// as the union of the two lists, since in practice they name the same
// folders on both sides (the asymmetric case, checking driveA's "X"
// against driveB's "Y", isn't supported by this simplification).
type Options struct {
	DriveA, DriveB string
	PathsA, PathsB []string
}

// Summary reports what a compare run actually did, for the console.
type Summary struct {
	DriveA, DriveB                       string
	Common, Diverged, Missing, Relocated int
	FoldersUpdated                       int
}

// backupRelative strips backupRoot from a drive-root-relative path,
// returning (path, true), or ("", false) if driveRootRel falls outside
// backupRoot entirely (e.g. "buda/x.txt" when backupRoot is "Jyo") — such
// files are outside the mirrored backup content and are never compared.
func backupRelative(driveRootRel, backupRoot string) (string, bool) {
	if backupRoot == "" {
		return driveRootRel, true
	}
	if driveRootRel == backupRoot {
		return "", true
	}
	prefix := backupRoot + "/"
	if strings.HasPrefix(driveRootRel, prefix) {
		return driveRootRel[len(prefix):], true
	}
	return "", false
}

// inScope reports whether backupRelPath is under one of paths (or paths is
// empty, meaning "everything is in scope").
func inScope(backupRelPath string, paths []string) bool {
	if len(paths) == 0 {
		return true
	}
	for _, p := range paths {
		if backupRelPath == p || strings.HasPrefix(backupRelPath, p+"/") {
			return true
		}
	}
	return false
}

// Run computes comparison status for the scoped paths, runs the global
// relocated pass, persists everything, and propagates folder_status up to
// each drive's root for every file actually touched.
func Run(st *store.Store, opts Options) (Summary, error) {
	sum := Summary{DriveA: opts.DriveA, DriveB: opts.DriveB}

	backupRootA, err := st.BackupRoot(opts.DriveA)
	if err != nil {
		return sum, err
	}
	backupRootB, err := st.BackupRoot(opts.DriveB)
	if err != nil {
		return sum, err
	}

	filesA, err := st.ListFiles(opts.DriveA, store.StatusHashed)
	if err != nil {
		return sum, err
	}
	filesB, err := st.ListFiles(opts.DriveB, store.StatusHashed)
	if err != nil {
		return sum, err
	}

	mapA := indexByBackupPath(filesA, backupRootA)
	mapB := indexByBackupPath(filesB, backupRootB)

	inScopeUnion := unionKeys(mapA, mapB, opts.PathsA, opts.PathsB)

	var updatesA, updatesB []store.ComparisonUpdate
	touchedA := map[string]bool{}
	touchedB := map[string]bool{}

	for backupPath := range inScopeUnion {
		a, okA := mapA[backupPath]
		b, okB := mapB[backupPath]
		switch {
		case okA && okB:
			status := store.ComparisonCommon
			if a.ContentHash != b.ContentHash {
				status = store.ComparisonDiverged
			}
			updatesA = append(updatesA, store.ComparisonUpdate{DriveID: opts.DriveA, RelPath: a.RelPath, ComparisonStatus: status, ComparedAgainstDriveID: opts.DriveB})
			updatesB = append(updatesB, store.ComparisonUpdate{DriveID: opts.DriveB, RelPath: b.RelPath, ComparisonStatus: status, ComparedAgainstDriveID: opts.DriveA})
			touchedA[a.RelPath] = true
			touchedB[b.RelPath] = true
			if status == store.ComparisonCommon {
				sum.Common++
			} else {
				sum.Diverged++
			}
		case okA && !okB:
			updatesA = append(updatesA, store.ComparisonUpdate{DriveID: opts.DriveA, RelPath: a.RelPath, ComparisonStatus: store.ComparisonMissing, ComparedAgainstDriveID: opts.DriveB})
			touchedA[a.RelPath] = true
			sum.Missing++
		case okB && !okA:
			updatesB = append(updatesB, store.ComparisonUpdate{DriveID: opts.DriveB, RelPath: b.RelPath, ComparisonStatus: store.ComparisonMissing, ComparedAgainstDriveID: opts.DriveA})
			touchedB[b.RelPath] = true
			sum.Missing++
		}
	}

	if err := st.UpdateComparisonStatuses(context.Background(), updatesA); err != nil {
		return sum, err
	}
	if err := st.UpdateComparisonStatuses(context.Background(), updatesB); err != nil {
		return sum, err
	}

	relocatedA, relocatedB, err := runGlobalRelocatedPass(st, opts.DriveA, opts.DriveB)
	if err != nil {
		return sum, err
	}
	for _, u := range relocatedA {
		touchedA[u.RelPath] = true
	}
	for _, u := range relocatedB {
		touchedB[u.RelPath] = true
	}
	sum.Relocated = len(relocatedA)
	// A relocated match reclassifies a file that was counted as `missing`
	// above (on the side that no longer has an exact-path match); undo
	// that double count for a clean summary.
	sum.Missing -= len(relocatedA) + len(relocatedB)

	updated, err := propagate(st, opts.DriveA, keys(touchedA))
	if err != nil {
		return sum, err
	}
	sum.FoldersUpdated += updated
	updated, err = propagate(st, opts.DriveB, keys(touchedB))
	if err != nil {
		return sum, err
	}
	sum.FoldersUpdated += updated

	return sum, nil
}

func indexByBackupPath(files []store.FileRecord, backupRoot string) map[string]store.FileRecord {
	m := make(map[string]store.FileRecord, len(files))
	for _, f := range files {
		if bp, ok := backupRelative(f.RelPath, backupRoot); ok {
			m[bp] = f
		}
	}
	return m
}

// unionKeys returns every backup-relative path that's in scope per
// Options.PathsA/PathsB (a deliberate "union of both sides" simplification
// — see the Options doc comment), drawn from whichever of mapA/mapB
// actually has that key.
func unionKeys(mapA, mapB map[string]store.FileRecord, pathsA, pathsB []string) map[string]bool {
	out := map[string]bool{}
	for k := range mapA {
		if inScope(k, pathsA) {
			out[k] = true
		}
	}
	for k := range mapB {
		if inScope(k, pathsB) {
			out[k] = true
		}
	}
	return out
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// runGlobalRelocatedPass multiset-matches every currently-`missing` file
// on both drives by content hash, regardless of what was scoped into this
// run — see docs/specs/drive-comparison-agent.md on why this stays unscoped.
// (No backup_root handling needed here: comparison_status is only ever
// set on files that already passed backupRelative's in-scope check in
// Run, above.)
func runGlobalRelocatedPass(st *store.Store, driveA, driveB string) ([]store.ComparisonUpdate, []store.ComparisonUpdate, error) {
	missingA, err := st.ListFilesByComparisonStatus(driveA, store.ComparisonMissing)
	if err != nil {
		return nil, nil, err
	}
	missingB, err := st.ListFilesByComparisonStatus(driveB, store.ComparisonMissing)
	if err != nil {
		return nil, nil, err
	}

	byHashA := groupByHash(missingA)
	byHashB := groupByHash(missingB)

	var updatesA, updatesB []store.ComparisonUpdate
	for hash, recsA := range byHashA {
		recsB := byHashB[hash]
		n := len(recsA)
		if len(recsB) < n {
			n = len(recsB)
		}
		for i := 0; i < n; i++ {
			updatesA = append(updatesA, store.ComparisonUpdate{
				DriveID: driveA, RelPath: recsA[i].RelPath,
				ComparisonStatus: store.ComparisonRelocated, ComparedAgainstDriveID: driveB,
				CounterpartRelativePath: recsB[i].RelPath,
			})
			updatesB = append(updatesB, store.ComparisonUpdate{
				DriveID: driveB, RelPath: recsB[i].RelPath,
				ComparisonStatus: store.ComparisonRelocated, ComparedAgainstDriveID: driveA,
				CounterpartRelativePath: recsA[i].RelPath,
			})
		}
	}

	if err := st.UpdateComparisonStatuses(context.Background(), updatesA); err != nil {
		return nil, nil, err
	}
	if err := st.UpdateComparisonStatuses(context.Background(), updatesB); err != nil {
		return nil, nil, err
	}
	return updatesA, updatesB, nil
}

// groupByHash buckets records by content hash, each bucket sorted oldest
// (by mtime) first, then by path, for deterministic multiset pairing.
func groupByHash(recs []store.FileRecord) map[string][]store.FileRecord {
	out := make(map[string][]store.FileRecord)
	for _, r := range recs {
		out[r.ContentHash] = append(out[r.ContentHash], r)
	}
	for _, bucket := range out {
		sort.Slice(bucket, func(i, j int) bool {
			if bucket[i].MTimeUnix != bucket[j].MTimeUnix {
				return bucket[i].MTimeUnix < bucket[j].MTimeUnix
			}
			return bucket[i].RelPath < bucket[j].RelPath
		})
	}
	return out
}
