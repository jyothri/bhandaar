package collect

import (
	"crypto/md5"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/jyothri/hdd/db"
)

func LocalDrive(localScan LocalScan) int {
	scanData := make(chan db.FileData, 10)
	scanId := db.LogStartScan("local")
	path := localScan.Path
	go db.SaveScanMetadata("dir="+path, "", scanId)
	go startCollectStats(scanId, path, scanData)
	go db.SaveStatToDb(scanId, scanData)
	return scanId
}

func startCollectStats(scanId int, parentDir string, scanData chan<- db.FileData) {
	lock.Lock()
	defer lock.Unlock()
	collectStats(parentDir, scanData)
	close(scanData)
}

// Gathers the info for the directory.
// Returns a tuple of (size of the directory, no. of files contained)
func collectStats(parentDir string, scanData chan<- db.FileData) (int64, int64) {
	var directorySize int64
	var fileCount int64 = 0
	err := filepath.Walk(parentDir, func(path string, info fs.FileInfo, err error) error {
		checkError(err)
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
			ds, fc := collectStats(path, scanData)
			directorySize += ds
			fileCount += fc
			fd.Size = uint(ds)
			fd.FileCount = uint(fc)
		} else {
			directorySize += info.Size()
			fileCount++
			fd.Size = uint(info.Size())
			fd.FileCount = 1
			fd.Md5Hash = getMd5ForFile(path)
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
	checkError(err)
	return directorySize, fileCount
}

func getMd5ForFile(filePath string) string {
	file, err := os.Open(filePath)
	checkError(err)
	defer file.Close()
	hash := md5.New()
	_, err = io.Copy(hash, file)
	checkError(err)
	return fmt.Sprintf("%x", hash.Sum(nil))
}

type LocalScan struct {
	Path string
}
