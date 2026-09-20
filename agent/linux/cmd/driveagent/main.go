// Command driveagent compares two drives' content for divergence: files
// that are common, diverged (same path, different content), relocated
// (same content, different path), or missing from one drive.
//
// It is a standalone, report-only tool — it never writes to either drive.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jyothri/bhandaar/agent/linux/internal/compare"
	"github.com/jyothri/bhandaar/agent/linux/internal/report"
	"github.com/jyothri/bhandaar/agent/linux/internal/scan"
	"github.com/jyothri/bhandaar/agent/linux/internal/store"
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
	case "scan":
		err = runScan(ctx, os.Args[2:])
	case "compare":
		err = runCompare(os.Args[2:])
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
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `driveagent — compare two drives for divergence (report-only, no writes)

Usage:
  driveagent scan    --drive-id <id> --path <content-root> [--state-dir <dir>] [--workers N]
  driveagent compare --drive-a <id> --drive-b <id> [--state-dir <dir>] [--report-out <dir>] [--format text,json,html]

Examples:
  driveagent scan    --drive-id seagate2 --path /mnt/seagate2/Jyo/Backup
  driveagent scan    --drive-id seagate1 --path /media/jyothri/Seagate1/Jyo
  driveagent compare --drive-a seagate2 --drive-b seagate1 --report-out ./report
`)
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".driveagent"
	}
	return filepath.Join(home, ".driveagent")
}

func runScan(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	driveID := fs.String("drive-id", "", "label for this drive (required)")
	path := fs.String("path", "", "content root to scan (required; independent of the mount point)")
	stateDir := fs.String("state-dir", defaultStateDir(), "directory holding the checkpoint database")
	workers := fs.Int("workers", 2, "concurrent hashing workers (keep low for spinning USB drives)")
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
		DriveID:  *driveID,
		RootPath: *path,
		Workers:  *workers,
		Progress: func(s scan.Stats) {
			fmt.Printf("  ...seen=%d skipped=%d hashed=%d errored=%d bytes_hashed=%d\n",
				s.FilesSeen, s.FilesSkipped, s.FilesHashed, s.FilesErrored, s.BytesHashed)
		},
	})

	fmt.Printf("done in %s: seen=%d skipped=%d hashed=%d errored=%d bytes_hashed=%d\n",
		stats.Elapsed.Round(time.Second), stats.FilesSeen, stats.FilesSkipped, stats.FilesHashed, stats.FilesErrored, stats.BytesHashed)

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
	stateDir := fs.String("state-dir", defaultStateDir(), "directory holding the checkpoint database")
	reportOut := fs.String("report-out", "./report", "output directory for json/html reports")
	format := fs.String("format", "text,json,html", "comma-separated: text,json,html")
	includeMacMetadata := fs.Bool("include-mac-metadata", false, "include AppleDouble (._*) and .DS_Store files in the report (excluded by default; they remain in the checkpoint DB either way)")
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

	res, err := compare.Run(st, *driveA, *driveB, compare.Options{IncludeMacMetadata: *includeMacMetadata})
	if err != nil {
		return err
	}

	formats := strings.Split(*format, ",")
	for _, f := range formats {
		switch strings.TrimSpace(f) {
		case "text":
			report.WriteConsole(os.Stdout, res)
		case "json":
			p := filepath.Join(*reportOut, "report.json")
			if err := report.WriteJSON(res, p); err != nil {
				return fmt.Errorf("writing json report: %w", err)
			}
			fmt.Printf("wrote %s\n", p)
		case "html":
			p := filepath.Join(*reportOut, "report.html")
			if err := report.WriteHTML(res, p); err != nil {
				return fmt.Errorf("writing html report: %w", err)
			}
			fmt.Printf("wrote %s\n", p)
		case "":
			// ignore stray empty entries from trailing commas
		default:
			return fmt.Errorf("unknown format %q", f)
		}
	}
	return nil
}
