// Command driveagent tracks and compares two drives' content for
// divergence: files that are common, diverged (same path, different
// content), relocated (same content, different path), or missing from one
// drive — plus, per folder, whether it's been fully scanned, partially
// scanned, or not scanned at all.
//
// It never writes to either drive. Three subcommands, each with a single
// responsibility (see docs/specs/drive-comparison-agent.md):
//
//	scan    walks and hashes one drive's files.
//	compare computes and persists comparison status for scoped paths.
//	report  renders already-computed status — no drive access, no compute.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/compare"
	"github.com/jyothri/bhandaar/agent/client/internal/report"
	"github.com/jyothri/bhandaar/agent/client/internal/scan"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "login":
		err = runLogin(ctx, os.Args[2:], os.Stdin, os.Stdout, os.Stderr)
	case "logout":
		err = runLogout(ctx, os.Args[2:], os.Stdout, os.Stderr)
	case "remote-status":
		err = runRemoteStatus(ctx, os.Args[2:], os.Stdout, os.Stderr)
	case "scan":
		err = runScan(ctx, os.Args[2:])
	case "compare":
		err = runCompare(os.Args[2:])
	case "report":
		err = runReport(os.Args[2:])
	case "version", "--version":
		fmt.Println(version.String())
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		code := exitLocal
		var ee *exitError
		if errors.As(err, &ee) {
			code = ee.code
		}
		os.Exit(code)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `driveagent — track and compare two drives for divergence (no writes to either drive)

Usage:
  driveagent scan    --drive-id <id> --path <folder> [--drive-root <dir>] [--backup-root <rel-path>] [--state-dir <dir>] [--workers N] [--replace-root]
  driveagent compare --drive-a <id> --drive-b <id> [--drive-a-paths <rel,rel,...>] [--drive-b-paths <rel,rel,...>] [--state-dir <dir>]
  driveagent report  --drives <id,id,...> [--type text,json,html] [--report-out <dir>] [--include-mac-metadata] [--state-dir <dir>]
  driveagent login         [--username <name>] [--password-stdin] [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
  driveagent logout        [--state-dir <dir>]
  driveagent remote-status [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
  driveagent version

Remote settings: flag > $DRIVEAGENT_REMOTE_URL / $DRIVEAGENT_LAN_ADDR > <state-dir>/config.json > https://sm.jkurapati.com.
Exit codes: 0 ok, 1 local error, 2 usage, 3 remote unavailable or login needed, 4 driveagent upgrade required.

Examples:
  driveagent scan    --drive-id seagate2 --drive-root /mnt/seagate2 --backup-root Jyo/Backup --path "/mnt/seagate2/Jyo/Backup/interview"
  driveagent scan    --drive-id seagate1 --drive-root /media/jyothri/Seagate1 --backup-root Jyo --path "/media/jyothri/Seagate1/Jyo/interview"
  driveagent compare --drive-a seagate2 --drive-b seagate1 --drive-a-paths interview --drive-b-paths interview
  driveagent report  --drives seagate1,seagate2 --report-out ./report
`)
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".driveagent"
	}
	return filepath.Join(home, ".driveagent")
}

func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func runScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	driveID := fs.String("drive-id", "", "label for this drive (required)")
	path := fs.String("path", "", "the specific folder to walk and hash this invocation (required)")
	driveRoot := fs.String("drive-root", "", "stable anchor for this drive's relative paths, e.g. its mount point (defaults to --path)")
	backupRoot := fs.String("backup-root", "", "where mirrored backup content starts, relative to --drive-root (only needed once per drive; omit to leave unset/unchanged)")
	stateDir := fs.String("state-dir", defaultStateDir(), "directory holding the checkpoint database")
	workers := fs.Int("workers", 2, "concurrent hashing workers (keep low for spinning USB drives)")
	replaceRoot := fs.Bool("replace-root", false, "allow --drive-id to be repointed at a different --drive-root than it was last scanned at, discarding that drive-id's old checkpoint data first")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *driveID == "" || *path == "" {
		fs.Usage()
		return fmt.Errorf("--drive-id and --path are required")
	}

	st, err := store.Open(*stateDir)
	if err != nil {
		return err
	}
	defer st.Close()

	fmt.Printf("scanning %q as drive %q (state: %s)\n", *path, *driveID, *stateDir)

	stats, err := scan.Run(ctx, st, scan.Options{
		DriveID:     *driveID,
		RootPath:    *path,
		DriveRoot:   *driveRoot,
		BackupRoot:  *backupRoot,
		Workers:     *workers,
		ReplaceRoot: *replaceRoot,
		Progress: func(s scan.Stats) {
			fmt.Printf("  ...seen=%d skipped=%d hashed=%d errored=%d bytes_hashed=%d\n",
				s.FilesSeen, s.FilesSkipped, s.FilesHashed, s.FilesErrored, s.BytesHashed)
		},
	})

	if _, ok := err.(*scan.RootConflict); ok {
		return err
	}

	fmt.Printf("done in %s: seen=%d skipped=%d hashed=%d errored=%d bytes_hashed=%d deleted=%d\n",
		stats.Elapsed.Round(time.Second), stats.FilesSeen, stats.FilesSkipped, stats.FilesHashed, stats.FilesErrored, stats.BytesHashed, stats.FilesDeleted)
	if stats.DeletionsSkipped {
		fmt.Println("note: skipped deletion detection because this walk hit an unreadable file/directory — a clean rescan (no warnings above) is needed to detect files removed from disk.")
	}

	if interrupted, ok := err.(*scan.Interrupted); ok {
		fmt.Printf("scan stopped early: %s\nre-run the same command to resume — already-hashed files will be skipped.\n", interrupted.Reason)
		return nil
	}
	return err
}

func runCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ExitOnError)
	driveA := fs.String("drive-a", "", "first drive ID (required; must have been scanned already)")
	driveB := fs.String("drive-b", "", "second drive ID (required; must have been scanned already)")
	pathsA := fs.String("drive-a-paths", "", "comma-separated backup-root-relative paths to (re)check on drive A (default: the whole drive)")
	pathsB := fs.String("drive-b-paths", "", "comma-separated backup-root-relative paths to (re)check on drive B (default: the whole drive)")
	stateDir := fs.String("state-dir", defaultStateDir(), "directory holding the checkpoint database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *driveA == "" || *driveB == "" {
		fs.Usage()
		return fmt.Errorf("--drive-a and --drive-b are required")
	}

	st, err := store.Open(*stateDir)
	if err != nil {
		return err
	}
	defer st.Close()

	sum, err := compare.Run(st, compare.Options{
		DriveA: *driveA, DriveB: *driveB,
		PathsA: splitList(*pathsA), PathsB: splitList(*pathsB),
	})
	if err != nil {
		return err
	}

	fmt.Printf("compared %s vs %s: common=%d diverged=%d missing=%d relocated=%d (folders updated: %d)\n",
		sum.DriveA, sum.DriveB, sum.Common, sum.Diverged, sum.Missing, sum.Relocated, sum.FoldersUpdated)
	fmt.Println("run 'driveagent report' to render the updated status.")
	return nil
}

func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	drives := fs.String("drives", "", "comma-separated drive IDs to report on (required)")
	reportType := fs.String("type", "text,json,html", "comma-separated: text,json,html")
	reportOut := fs.String("report-out", "./report", "output directory for json/html reports")
	includeMacMetadata := fs.Bool("include-mac-metadata", false, "include AppleDouble (._*) and .DS_Store files in the report (excluded by default; they remain in the checkpoint DB either way)")
	stateDir := fs.String("state-dir", defaultStateDir(), "directory holding the checkpoint database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	driveList := splitList(*drives)
	if len(driveList) == 0 {
		fs.Usage()
		return fmt.Errorf("--drives is required")
	}

	st, err := store.Open(*stateDir)
	if err != nil {
		return err
	}
	defer st.Close()

	reports, err := report.Build(st, report.Options{Drives: driveList, IncludeMacMetadata: *includeMacMetadata})
	if err != nil {
		return err
	}

	for _, f := range splitList(*reportType) {
		switch f {
		case "text":
			report.WriteConsole(os.Stdout, reports)
		case "json":
			p := filepath.Join(*reportOut, "report.json")
			if err := report.WriteJSON(reports, p); err != nil {
				return fmt.Errorf("writing json report: %w", err)
			}
			fmt.Printf("wrote %s\n", p)
		case "html":
			p := filepath.Join(*reportOut, "report.html")
			if err := report.WriteHTML(reports, p); err != nil {
				return fmt.Errorf("writing html report: %w", err)
			}
			fmt.Printf("wrote %s\n", p)
		default:
			return fmt.Errorf("unknown --type %q", f)
		}
	}
	return nil
}
