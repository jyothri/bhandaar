package collect

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jyothri/hdd/db"
)

func LocalDrive(localScan LocalScan) (int, error) {
	// Phase 1: Create scan record (synchronous)
	scanId, err := db.LogStartScan("local")
	if err != nil {
		return 0, fmt.Errorf("failed to start local scan (path=%s): %w", localScan.Path, err)
	}

	path := localScan.Path

	// Save metadata in background
	go func() {
		if err := db.SaveScanMetadata("", "dir="+path, "", scanId); err != nil {
			slog.Error("Failed to save scan metadata",
				"scan_id", scanId,
				"path", path,
				"error", err)
		}
	}()

	// Phase 2: Start collection in background (asynchronous)
	scanData := make(chan db.FileData, 10)
	go func() {
		defer close(scanData)

		err := startCollectStats(scanId, path, scanData)
		if err != nil {
			slog.Error("Local scan collection failed",
				"scan_id", scanId,
				"path", path,
				"error", err)
			db.MarkScanFailed(scanId, err.Error())
			return
		}
	}()

	// Start processing file data in background
	go db.SaveStatToDb(scanId, scanData)

	return scanId, nil
}

func startCollectStats(scanId int, parentDir string, scanData chan<- db.FileData) error {
	lock.Lock()
	defer lock.Unlock()
	_, _, err := collectStats(parentDir, scanData)
	return err
}

// Gathers the info for the directory.
// Returns a tuple of (size of the directory, no. of files contained, error)
func collectStats(parentDir string, scanData chan<- db.FileData) (int64, int64, error) {
	var directorySize int64
	var fileCount int64 = 0
	err := filepath.Walk(parentDir, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			// Log and skip problematic files/directories
			slog.Warn("Failed to access path during walk, skipping",
				"path", path,
				"error", err)
			return nil // Continue walking
		}

		// filepath.Walk also traverses the parent directory.
		// As we call the same function recursively, we would
		// end up invoking with the same arg again which results
		// in an infinite loop. This check prevents traversing
		// the same directory again.
		if parentDir == path {
			return nil
		}

		// Skip hidden files and directories
		if runtime.GOOS != "windows" && info.Name()[0:1] == "." {
			// unix/linux file or directory that starts with . is hidden
			return nil
		}

		fd := db.FileData{
			FileName:  info.Name(),
			FilePath:  path,
			IsDir:     info.IsDir(),
			ModTime:   info.ModTime(),
			FileCount: 1,
		}
		if info.IsDir() {
			ds, fc, err := collectStats(path, scanData)
			if err != nil {
				slog.Error("Failed to collect stats for directory, skipping",
					"path", path,
					"error", err)
				return filepath.SkipDir
			}
			directorySize += ds
			fileCount += fc
			fd.Size = uint(ds)
			fd.FileCount = uint(fc)
		} else {
			directorySize += info.Size()
			fileCount++
			fd.Size = uint(info.Size())
			fd.FileCount = 1
			fd.Md5Hash = getMd5ForFile(path) // Returns "" on error
		}
		scanData <- fd
		// filepath.Walk works recursively. However our call to
		// collectStats also performs the traversal recursively.
		// Returns `filepath.SkipDir` limits to only the files and folders
		// in current directory to prevent multiple traversals.
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("failed to walk directory %s: %w", parentDir, err)
	}
	return directorySize, fileCount, nil
}

func getMd5ForFile(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		// Log but don't fail - MD5 is optional metadata
		slog.Warn("Failed to open file for MD5 calculation, skipping hash",
			"path", filePath,
			"error", err)
		return ""
	}
	defer file.Close()

	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		// Log but don't fail - MD5 is optional metadata
		slog.Warn("Failed to calculate MD5 hash, skipping",
			"path", filePath,
			"error", err)
		return ""
	}

	return hex.EncodeToString(hash.Sum(nil))
}

type LocalScan struct {
	Path string
}
