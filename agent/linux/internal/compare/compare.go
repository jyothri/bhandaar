// Package compare classifies files from two already-scanned drives into
// common / diverged / relocated / missing, per the checkpoint database.
package compare

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/jyothri/bhandaar/agent/linux/internal/store"
)

// CommonFile is identical content at the same relative path on both drives.
type CommonFile struct {
	RelPath string
	Size    int64
	Hash    string
}

// DivergedFile is the same relative path on both drives with different
// content — a corruption/edit candidate for the user to review manually.
type DivergedFile struct {
	RelPath string
	SizeA   int64
	HashA   string
	SizeB   int64
	HashB   string
}

// RelocatedFile is identical content present on both drives but under
// different relative paths.
type RelocatedFile struct {
	PathA string
	PathB string
	Size  int64
	Hash  string
}

// MissingFile is present on exactly one drive, by both path and content.
type MissingFile struct {
	RelPath   string
	Size      int64
	Hash      string
	PresentOn string // drive ID this file exists on
}

// ScanErrorEntry surfaces a file that failed to hash during scanning, so
// it isn't silently absent from the report.
type ScanErrorEntry struct {
	Drive   string
	RelPath string
	Message string
}

// Result is the full classification of two drives' scanned file sets.
type Result struct {
	DriveA, DriveB string
	Common         []CommonFile
	Diverged       []DivergedFile
	Relocated      []RelocatedFile
	Missing        []MissingFile
	ScanErrors     []ScanErrorEntry
	// ExcludedCount is how many scanned files were left out of
	// classification (e.g. macOS metadata files), for the report to
	// note without cluttering the categories above.
	ExcludedCount int
}

// Options controls what Run excludes from classification. Excluded files
// remain untouched in the checkpoint DB — this only affects what's
// compared/reported.
type Options struct {
	// IncludeMacMetadata, if false (the default), drops AppleDouble
	// sidecar files (._*) and Finder's .DS_Store from classification —
	// they're an artifact of copying from a Mac, not real content
	// divergence, and otherwise dominate every report.
	IncludeMacMetadata bool
}

// Run loads the recorded scans for driveA and driveB from st and classifies
// every file per the spec's a/b/c/d categories.
func Run(st *store.Store, driveA, driveB string, opts Options) (Result, error) {
	filesA, err := st.ListFiles(driveA, store.StatusHashed)
	if err != nil {
		return Result{}, err
	}
	filesB, err := st.ListFiles(driveB, store.StatusHashed)
	if err != nil {
		return Result{}, err
	}

	res := Result{DriveA: driveA, DriveB: driveB}
	if !opts.IncludeMacMetadata {
		var excludedA, excludedB int
		filesA, excludedA = filterMacMetadata(filesA)
		filesB, excludedB = filterMacMetadata(filesB)
		res.ExcludedCount = excludedA + excludedB
	}

	mapA := make(map[string]store.FileRecord, len(filesA))
	for _, f := range filesA {
		mapA[f.RelPath] = f
	}
	mapB := make(map[string]store.FileRecord, len(filesB))
	for _, f := range filesB {
		mapB[f.RelPath] = f
	}

	var leftoverA, leftoverB []store.FileRecord
	for relPath, a := range mapA {
		b, ok := mapB[relPath]
		if !ok {
			leftoverA = append(leftoverA, a)
			continue
		}
		if a.ContentHash == b.ContentHash {
			res.Common = append(res.Common, CommonFile{RelPath: relPath, Size: a.Size, Hash: a.ContentHash})
		} else {
			res.Diverged = append(res.Diverged, DivergedFile{
				RelPath: relPath,
				SizeA:   a.Size, HashA: a.ContentHash,
				SizeB: b.Size, HashB: b.ContentHash,
			})
		}
	}
	for relPath, b := range mapB {
		if _, ok := mapA[relPath]; !ok {
			leftoverB = append(leftoverB, b)
		}
	}

	// Content-match the leftovers (present under a path unique to one
	// drive) to find relocations, treating same-hash groups as multisets
	// so duplicate content is paired up rather than cross-matched
	// arbitrarily.
	byHashA := groupByHash(leftoverA)
	byHashB := groupByHash(leftoverB)

	for hash, recsA := range byHashA {
		recsB := byHashB[hash]
		n := min(len(recsA), len(recsB))
		for i := 0; i < n; i++ {
			res.Relocated = append(res.Relocated, RelocatedFile{
				PathA: recsA[i].RelPath,
				PathB: recsB[i].RelPath,
				Size:  recsA[i].Size,
				Hash:  hash,
			})
		}
		for _, rec := range recsA[n:] {
			res.Missing = append(res.Missing, MissingFile{RelPath: rec.RelPath, Size: rec.Size, Hash: rec.ContentHash, PresentOn: driveA})
		}
	}
	for hash, recsB := range byHashB {
		recsA := byHashA[hash]
		if len(recsA) >= len(recsB) {
			continue // already fully paired (or over-paired) above
		}
		for _, rec := range recsB[len(recsA):] {
			res.Missing = append(res.Missing, MissingFile{RelPath: rec.RelPath, Size: rec.Size, Hash: rec.ContentHash, PresentOn: driveB})
		}
	}

	sortResult(&res)

	for _, drive := range []string{driveA, driveB} {
		errs, err := st.ListFiles(drive, store.StatusError)
		if err != nil {
			return Result{}, err
		}
		for _, e := range errs {
			res.ScanErrors = append(res.ScanErrors, ScanErrorEntry{Drive: drive, RelPath: e.RelPath, Message: e.ErrorMessage})
		}
	}

	return res, nil
}

// isMacMetadata reports whether relPath is a macOS-generated sidecar file
// rather than real content: an AppleDouble resource-fork file (._name) or
// a Finder folder-metadata file (.DS_Store).
func isMacMetadata(relPath string) bool {
	base := filepath.Base(relPath)
	return strings.HasPrefix(base, "._") || base == ".DS_Store"
}

// filterMacMetadata drops macOS sidecar files from recs, returning the
// filtered slice and how many were dropped. The underlying checkpoint DB
// rows are untouched — this only affects what gets classified/reported.
func filterMacMetadata(recs []store.FileRecord) ([]store.FileRecord, int) {
	kept := recs[:0:0]
	excluded := 0
	for _, r := range recs {
		if isMacMetadata(r.RelPath) {
			excluded++
			continue
		}
		kept = append(kept, r)
	}
	return kept, excluded
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

func sortResult(res *Result) {
	sort.Slice(res.Common, func(i, j int) bool { return res.Common[i].RelPath < res.Common[j].RelPath })
	sort.Slice(res.Diverged, func(i, j int) bool { return res.Diverged[i].RelPath < res.Diverged[j].RelPath })
	sort.Slice(res.Relocated, func(i, j int) bool { return res.Relocated[i].PathA < res.Relocated[j].PathA })
	sort.Slice(res.Missing, func(i, j int) bool { return res.Missing[i].RelPath < res.Missing[j].RelPath })
	sort.Slice(res.ScanErrors, func(i, j int) bool { return res.ScanErrors[i].RelPath < res.ScanErrors[j].RelPath })
}
