// Package scan walks a drive's content root, hashing files that are new or
// changed since the last recorded scan, and skipping files that already
// match their checkpointed (size, mtime). It also records, cheaply, the
// real directory structure it observes (dir_listings) so that folders
// never explicitly scanned can still be reported as "unscanned" rather
// than being invisible, and — when the walk completes without any
// unreadable entries — removes checkpoint rows for files/directory
// entries that no longer exist on disk. See specs/drive-comparison-agent.md.
package scan

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jyothri/bhandaar/agent/linux/internal/store"
	"lukechampine.com/blake3"
)

const HashAlgo = "blake3"

// Options configures a scan run.
type Options struct {
	DriveID string
	// RootPath is the specific folder to walk and hash in this invocation.
	RootPath string
	// DriveRoot is the stable anchor all of this drive_id's relative paths
	// are computed against (in practice, its mount point). Defaults to
	// RootPath if empty, so a single-scan workflow needs no extra flags.
	// Must equal (or resolve to) whatever DriveRoot was used previously
	// for this drive_id — see RootConflict.
	DriveRoot string
	// BackupRoot, if non-empty, sets/updates this drive's backup_root
	// (relative to DriveRoot) — see specs/drive-comparison-agent.md.
	// Only takes effect when explicitly given; omitted on later scans, the
	// previously-set value (if any) is left alone.
	BackupRoot string
	Workers    int
	// BatchSize controls how many upserts are buffered before a DB flush.
	BatchSize int
	// Progress, if set, is called periodically with running totals.
	Progress func(Stats)
	// ReplaceRoot, if true, allows DriveID to be repointed at a different
	// DriveRoot than what's already checkpointed for it, discarding all
	// of that drive_id's checkpoint data first. Without it, Run refuses
	// with RootConflict — see that type's doc comment.
	ReplaceRoot bool
}

// RootConflict is returned by Run when DriveID was already scanned under a
// different DriveRoot and Options.ReplaceRoot wasn't set. Every relative
// path for a drive_id is computed against its DriveRoot, so silently
// repointing it would shift the meaning of every existing row.
type RootConflict struct {
	DriveID string
	OldRoot string
	NewRoot string
}

func (e *RootConflict) Error() string {
	return fmt.Sprintf(
		"drive %q was previously scanned with drive-root %q; refusing to rescan it at a different drive-root %q.\n"+
			"Either pass --replace-root to discard %q's existing checkpoint data and start fresh,\n"+
			"or use a different --drive-id to keep both scans independent.",
		e.DriveID, e.OldRoot, e.NewRoot, e.DriveID)
}

// Stats tracks running totals for a scan, reported via Options.Progress and
// returned at the end of Run.
type Stats struct {
	FilesSeen    int64
	FilesSkipped int64
	FilesHashed  int64
	FilesErrored int64
	BytesHashed  int64
	// FilesDeleted counts checkpoint rows removed because the file no
	// longer exists on disk. Only ever non-zero when the walk completed
	// with zero unreadable entries — see DeletionsSkipped.
	FilesDeleted int64
	// DeletionsSkipped is true when the walk hit at least one unreadable
	// file/directory, making it unsafe to conclude that anything merely
	// absent from this walk was actually deleted (it might just be behind
	// the permission error). No deletion detection ran this time.
	DeletionsSkipped bool
	Elapsed          time.Duration
}

// Interrupted is returned by Run when the scan stopped early because the
// content root disappeared (drive unmounted/disconnected) or the context
// was cancelled (e.g. Ctrl-C).
type Interrupted struct {
	Reason string
}

func (e *Interrupted) Error() string { return "scan interrupted: " + e.Reason }

type job struct {
	relPath string
	absPath string
	size    int64
	mtime   time.Time
	mode    fs.FileMode
}

// walkState carries the mutable, walk-scoped bookkeeping that
// walkAndDispatch accumulates alongside Stats: which regular files were
// actually seen (for deletion detection) and whether any entry was
// unreadable (which disables deletion detection for this run — see
// Stats.DeletionsSkipped).
type walkState struct {
	mu            sync.Mutex
	dirChildren   map[string][]store.DirChild
	observedFiles map[string]bool
	sawSoftError  bool
}

// Run walks opts.RootPath and hashes files that are new or changed relative
// to what's already recorded in st for opts.DriveID. It is safe to call
// repeatedly (including after a prior interrupted run) — already-hashed,
// unchanged files are skipped.
func Run(ctx context.Context, st *store.Store, opts Options) (Stats, error) {
	if opts.Workers <= 0 {
		opts.Workers = 2
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 200
	}

	scanPath, err := filepath.Abs(opts.RootPath)
	if err != nil {
		return Stats{}, fmt.Errorf("resolving path: %w", err)
	}
	if info, err := os.Stat(scanPath); err != nil {
		return Stats{}, &Interrupted{Reason: fmt.Sprintf("path %q is not accessible: %v", scanPath, err)}
	} else if !info.IsDir() {
		return Stats{}, fmt.Errorf("path %q is not a directory", scanPath)
	}

	driveRootInput := opts.DriveRoot
	if driveRootInput == "" {
		driveRootInput = opts.RootPath
	}
	driveRoot, err := filepath.Abs(driveRootInput)
	if err != nil {
		return Stats{}, fmt.Errorf("resolving drive-root: %w", err)
	}

	relScanPath, err := filepath.Rel(driveRoot, scanPath)
	if err != nil || relScanPath == ".." || strings.HasPrefix(relScanPath, "../") {
		return Stats{}, fmt.Errorf("--path %q is not drive-root %q or a descendant of it", scanPath, driveRoot)
	}
	if relScanPath == "." {
		relScanPath = ""
	}

	existingRoot, found, err := st.ExistingDriveRoot(opts.DriveID)
	if err != nil {
		return Stats{}, fmt.Errorf("checking existing drive record: %w", err)
	}
	if found && existingRoot != driveRoot {
		if !opts.ReplaceRoot {
			return Stats{}, &RootConflict{DriveID: opts.DriveID, OldRoot: existingRoot, NewRoot: driveRoot}
		}
		if err := st.ClearDrive(opts.DriveID); err != nil {
			return Stats{}, fmt.Errorf("clearing old checkpoint for drive %q: %w", opts.DriveID, err)
		}
	}

	if err := st.UpsertDrive(opts.DriveID, driveRoot, opts.BackupRoot); err != nil {
		return Stats{}, fmt.Errorf("recording drive: %w", err)
	}
	if opts.BackupRoot != "" {
		if err := st.SetBackupRoot(opts.DriveID, opts.BackupRoot); err != nil {
			return Stats{}, fmt.Errorf("recording backup root: %w", err)
		}
	}
	runID, err := st.StartScanRun(opts.DriveID)
	if err != nil {
		return Stats{}, fmt.Errorf("starting scan run: %w", err)
	}

	start := time.Now()
	stats := &Stats{}
	var statsMu sync.Mutex

	jobs := make(chan job, opts.Workers*4)
	results := make(chan store.FileRecord, opts.Workers*4)
	ws := &walkState{
		dirChildren:   make(map[string][]store.DirChild),
		observedFiles: make(map[string]bool),
	}
	// The scan path itself always gets a dir_listings key, even if it
	// turns out to be (or has become) completely empty — otherwise an
	// emptied directory's stale children would never be diffed away.
	scanKey := relScanPath
	ws.dirChildren[scanKey] = ws.dirChildren[scanKey]

	var wg sync.WaitGroup
	for i := 0; i < opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				rec := hashFile(opts.DriveID, j)
				statsMu.Lock()
				if rec.Status == store.StatusError {
					stats.FilesErrored++
				} else {
					stats.FilesHashed++
					stats.BytesHashed += j.size
				}
				statsMu.Unlock()
				select {
				case results <- rec:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Batches results into the checkpoint DB.
	var flushErr error
	batchDone := make(chan struct{})
	go func() {
		defer close(batchDone)
		batch := make([]store.FileRecord, 0, opts.BatchSize)
		flush := func() {
			if len(batch) == 0 {
				return
			}
			if err := st.UpsertFiles(context.Background(), batch); err != nil {
				flushErr = err
			}
			batch = batch[:0]
		}
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case rec, ok := <-results:
				if !ok {
					flush()
					return
				}
				batch = append(batch, rec)
				if len(batch) >= opts.BatchSize {
					flush()
				}
			case <-ticker.C:
				flush()
			}
		}
	}()

	walkErr := walkAndDispatch(ctx, st, opts, driveRoot, scanPath, jobs, stats, &statsMu, ws)
	close(jobs)
	wg.Wait()
	close(results)
	<-batchDone

	// Cheap shallow listing of every ancestor between drive_root and
	// scanPath's parent, so folders above scanPath (siblings of what got
	// walked) are known to exist even though nothing under them was
	// touched. See specs/drive-comparison-agent.md.
	listAncestors(driveRoot, relScanPath, ws)

	clean := walkErr == nil && ctx.Err() == nil && !ws.sawSoftError
	stats.DeletionsSkipped = !clean

	if err := st.SyncDirListings(context.Background(), opts.DriveID, ws.dirChildren, clean); err != nil {
		return *stats, fmt.Errorf("recording directory listings: %w", err)
	}

	if clean {
		deleted, err := deleteMissingFiles(context.Background(), st, opts.DriveID, relScanPath, ws.observedFiles)
		if err != nil {
			return *stats, fmt.Errorf("removing checkpoint rows for deleted files: %w", err)
		}
		stats.FilesDeleted = deleted
	}

	stats.Elapsed = time.Since(start)

	interrupted := walkErr != nil || ctx.Err() != nil
	if err := st.FinishScanRun(runID, opts.DriveID, stats.FilesSeen, stats.BytesHashed, interrupted); err != nil {
		return *stats, fmt.Errorf("recording scan run outcome: %w", err)
	}
	if flushErr != nil {
		return *stats, fmt.Errorf("writing checkpoint db: %w", flushErr)
	}
	if walkErr != nil {
		return *stats, walkErr
	}
	if ctx.Err() != nil {
		return *stats, &Interrupted{Reason: "cancelled"}
	}
	return *stats, nil
}

// deleteMissingFiles removes files rows under prefix (relative to
// drive_root; "" means the whole drive) that weren't observed during this
// walk — i.e. files that used to be checkpointed there but no longer
// exist on disk.
func deleteMissingFiles(ctx context.Context, st *store.Store, driveID, prefix string, observed map[string]bool) (int64, error) {
	existing, err := st.ListFileRelativePaths(driveID)
	if err != nil {
		return 0, err
	}
	var toDelete []string
	for _, p := range existing {
		if !underPrefix(p, prefix) || observed[p] {
			continue
		}
		toDelete = append(toDelete, p)
	}
	if len(toDelete) == 0 {
		return 0, nil
	}
	if err := st.DeleteFiles(ctx, driveID, toDelete); err != nil {
		return 0, err
	}
	return int64(len(toDelete)), nil
}

func underPrefix(relPath, prefix string) bool {
	if prefix == "" {
		return true
	}
	return relPath == prefix || strings.HasPrefix(relPath, prefix+"/")
}

// listAncestors walks upward from relScanPath's parent to drive_root
// (inclusive), doing one non-recursive directory read at each level and
// recording its children — this is what lets a folder like "root" be known
// to have children {A, B} even when only "root/B" was ever scanned. A
// level is only registered (and so only eligible for deletion-sync) when
// its ReadDir actually succeeds; on error, that level is left untouched
// rather than risk treating "couldn't check" as "nothing there."
func listAncestors(driveRoot, relScanPath string, ws *walkState) {
	if relScanPath == "" {
		return // scanPath == driveRoot: nothing above it to list
	}
	cur := filepath.Dir(relScanPath)
	for {
		key := cur
		if key == "." {
			key = ""
		}
		curAbs := driveRoot
		if key != "" {
			curAbs = filepath.Join(driveRoot, key)
		}
		if _, already := ws.dirChildren[key]; !already {
			entries, err := os.ReadDir(curAbs)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not list %q: %v\n", curAbs, err)
			} else {
				ws.dirChildren[key] = ws.dirChildren[key] // register even if entries is empty
				for _, e := range entries {
					ws.dirChildren[key] = append(ws.dirChildren[key], store.DirChild{Name: e.Name(), IsDir: e.IsDir()})
				}
			}
		}
		if cur == "." {
			return
		}
		cur = filepath.Dir(cur)
	}
}

func walkAndDispatch(ctx context.Context, st *store.Store, opts Options, driveRoot, scanPath string, jobs chan<- job, stats *Stats, statsMu *sync.Mutex, ws *walkState) error {
	lastProgress := time.Now()
	return filepath.WalkDir(scanPath, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == scanPath {
				// The scan path itself vanished mid-scan: treat as a full
				// interruption rather than skipping one entry.
				return &Interrupted{Reason: fmt.Sprintf("scan path became inaccessible: %v", err)}
			}
			// A single unreadable directory/file: log, keep going, but
			// remember it — it disables deletion detection for this run
			// (see Stats.DeletionsSkipped).
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", path, err)
			ws.mu.Lock()
			ws.sawSoftError = true
			ws.mu.Unlock()
			return nil
		}

		relPath, err := filepath.Rel(driveRoot, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", path, err)
			ws.mu.Lock()
			ws.sawSoftError = true
			ws.mu.Unlock()
			return nil
		}

		// Record this entry as a child of its parent (free bookkeeping —
		// WalkDir already read the parent directory to find this entry).
		// The walk's own starting point isn't anyone's "child" here.
		if path != scanPath {
			parent := filepath.Dir(relPath)
			if parent == "." {
				parent = ""
			}
			ws.mu.Lock()
			ws.dirChildren[parent] = append(ws.dirChildren[parent], store.DirChild{Name: d.Name(), IsDir: d.IsDir()})
			ws.mu.Unlock()
		}

		if d.IsDir() {
			// Ensure this directory gets a dir_listings key even if it
			// turns out to have zero children — otherwise an emptied
			// directory's stale children would never be diffed away.
			// ("." — relPath when this directory is scanPath itself and
			// driveRoot == scanPath — is normalized to "" like every
			// other parent key.)
			key := relPath
			if key == "." {
				key = ""
			}
			ws.mu.Lock()
			ws.dirChildren[key] = ws.dirChildren[key]
			ws.mu.Unlock()
			return nil
		}
		info, err := d.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", path, err)
			ws.mu.Lock()
			ws.sawSoftError = true
			ws.mu.Unlock()
			return nil
		}
		// Symlinks, devices, sockets, FIFOs: skip and log, per spec. Not
		// tracked in observedFiles — we never create a files row for
		// these, so there's nothing to protect from deletion-detection.
		if !info.Mode().IsRegular() {
			fmt.Fprintf(os.Stderr, "info: skipping non-regular file %q (mode %v)\n", path, info.Mode())
			return nil
		}

		ws.mu.Lock()
		ws.observedFiles[relPath] = true
		ws.mu.Unlock()

		statsMu.Lock()
		stats.FilesSeen++
		seen := stats.FilesSeen
		statsMu.Unlock()

		existing, err := st.Existing(opts.DriveID, relPath)
		if err != nil {
			return fmt.Errorf("checking checkpoint for %q: %w", relPath, err)
		}
		if existing != nil && existing.Status == store.StatusHashed &&
			existing.Size == info.Size() && existing.MTimeUnix == info.ModTime().Unix() {
			statsMu.Lock()
			stats.FilesSkipped++
			statsMu.Unlock()
		} else {
			select {
			case jobs <- job{relPath: relPath, absPath: path, size: info.Size(), mtime: info.ModTime(), mode: info.Mode()}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		if opts.Progress != nil && (seen%500 == 0 || time.Since(lastProgress) > 3*time.Second) {
			lastProgress = time.Now()
			statsMu.Lock()
			snapshot := *stats
			statsMu.Unlock()
			opts.Progress(snapshot)
		}
		return nil
	})
}

func hashFile(driveID string, j job) store.FileRecord {
	rec := store.FileRecord{
		DriveID:   driveID,
		RelPath:   j.relPath,
		Size:      j.size,
		MTimeUnix: j.mtime.Unix(),
		Mode:      uint32(j.mode.Perm()),
		QuickSig:  fmt.Sprintf("%d:%d", j.size, j.mtime.Unix()),
		HashAlgo:  HashAlgo,
		ScannedAt: time.Now().UTC(),
	}

	f, err := os.Open(j.absPath)
	if err != nil {
		rec.Status = store.StatusError
		rec.ErrorMessage = err.Error()
		return rec
	}
	defer f.Close()

	h := blake3.New(32, nil)
	if _, err := io.Copy(h, f); err != nil {
		rec.Status = store.StatusError
		rec.ErrorMessage = err.Error()
		return rec
	}

	rec.ContentHash = fmt.Sprintf("%x", h.Sum(nil))
	rec.Status = store.StatusHashed
	return rec
}
