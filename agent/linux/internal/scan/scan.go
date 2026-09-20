// Package scan walks a drive's content root, hashing files that are new or
// changed since the last recorded scan, and skipping files that already
// match their checkpointed (size, mtime).
package scan

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jyothri/bhandaar/agent/linux/internal/store"
	"lukechampine.com/blake3"
)

const HashAlgo = "blake3"

// Options configures a scan run.
type Options struct {
	DriveID  string
	RootPath string
	Workers  int
	// BatchSize controls how many upserts are buffered before a DB flush.
	BatchSize int
	// Progress, if set, is called periodically with running totals.
	Progress func(Stats)
}

// Stats tracks running totals for a scan, reported via Options.Progress and
// returned at the end of Run.
type Stats struct {
	FilesSeen    int64
	FilesSkipped int64
	FilesHashed  int64
	FilesErrored int64
	BytesHashed  int64
	Elapsed      time.Duration
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

	root, err := filepath.Abs(opts.RootPath)
	if err != nil {
		return Stats{}, fmt.Errorf("resolving root path: %w", err)
	}
	if info, err := os.Stat(root); err != nil {
		return Stats{}, &Interrupted{Reason: fmt.Sprintf("scan root %q is not accessible: %v", root, err)}
	} else if !info.IsDir() {
		return Stats{}, fmt.Errorf("scan root %q is not a directory", root)
	}

	if err := st.UpsertDrive(opts.DriveID, root); err != nil {
		return Stats{}, fmt.Errorf("recording drive: %w", err)
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

	walkErr := walkAndDispatch(ctx, st, opts, root, jobs, stats, &statsMu)
	close(jobs)
	wg.Wait()
	close(results)
	<-batchDone

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

func walkAndDispatch(ctx context.Context, st *store.Store, opts Options, root string, jobs chan<- job, stats *Stats, statsMu *sync.Mutex) error {
	lastProgress := time.Now()
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if path == root {
				// The root itself vanished mid-scan: treat as a full
				// interruption rather than skipping one entry.
				return &Interrupted{Reason: fmt.Sprintf("scan root became inaccessible: %v", err)}
			}
			// A single unreadable directory/file: log and keep going.
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", path, err)
			return nil
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", path, err)
			return nil
		}
		// Symlinks, devices, sockets, FIFOs: skip and log, per spec.
		if !info.Mode().IsRegular() {
			fmt.Fprintf(os.Stderr, "info: skipping non-regular file %q (mode %v)\n", path, info.Mode())
			return nil
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: skipping %q: %v\n", path, err)
			return nil
		}

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
