// Command driveagent tracks and compares two drives' content for
// divergence: files that are common, diverged (same path, different
// content), relocated (same content, different path), or missing from one
// drive — plus, per folder, whether it's been fully scanned, partially
// scanned, or not scanned at all.
//
// It never writes to either drive. Three subcommands, each with a single
// responsibility (see docs/specs/drive-comparison-agent.md):
//
//	scan    walks and hashes one drive's files, uploading what it records.
//	compare computes and persists comparison status for scoped paths.
//	report  renders already-computed status — no drive access, no compute.
//
// And the remote ones (docs/specs/remote-sync-agent.md): login, logout,
// remote-status, and sync, which uploads the drives' scan data to
// agentserver.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/compare"
	"github.com/jyothri/bhandaar/agent/client/internal/identity"
	"github.com/jyothri/bhandaar/agent/client/internal/report"
	"github.com/jyothri/bhandaar/agent/client/internal/scan"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/client/internal/syncer"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
	"github.com/jyothri/bhandaar/agent/wire"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, drain, stop := withSignals(context.Background())

	var err error
	switch os.Args[1] {
	case "login":
		err = runLogin(ctx, os.Args[2:], os.Stdin, os.Stdout, os.Stderr)
	case "logout":
		err = runLogout(ctx, os.Args[2:], os.Stdout, os.Stderr)
	case "sync":
		err = runSync(ctx, os.Args[2:], os.Stdout, os.Stderr)
	case "remote-status":
		err = runRemoteStatus(ctx, os.Args[2:], os.Stdout, os.Stderr)
	case "scan":
		err = runScan(ctx, drain, os.Args[2:], os.Stdout, os.Stderr)
	case "compare":
		err = runCompare(os.Args[2:])
	case "report":
		err = runReport(os.Args[2:])
	case "version", "--version":
		fmt.Println(version.String())
		stop()
		return
	case "-h", "--help", "help":
		usage()
		stop()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", os.Args[1])
		usage()
		stop()
		os.Exit(exitUsage)
	}
	code := exitCode(ctx, err)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	} else if cause := context.Cause(ctx); cause != nil && code != exitOK {
		fmt.Fprintf(os.Stderr, "%v\n", cause)
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprint(os.Stderr, `driveagent — track and compare two drives for divergence (no writes to either drive)

Usage:
  driveagent scan    --drive-id <id> --path <folder> [--drive-root <dir>] [--backup-root <rel-path>] [--state-dir <dir>] [--workers N] [--replace-root] [--accept-identity-change]
                     [--remote-timeout 2m] [--remote-url <url>] [--lan-addr <host:port>]
  driveagent compare --drive-a <id> --drive-b <id> [--drive-a-paths <rel,rel,...>] [--drive-b-paths <rel,rel,...>] [--state-dir <dir>]
  driveagent report  --drives <id,id,...> [--type text,json,html] [--report-out <dir>] [--include-mac-metadata] [--state-dir <dir>]
  driveagent login         [--username <name>] [--password-stdin] [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
  driveagent logout        [--state-dir <dir>]
  driveagent sync          [--drive-id <id,id,...>] [--remote-timeout 2m] [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
  driveagent remote-status [--remote-url <url>] [--lan-addr <host:port>] [--state-dir <dir>]
  driveagent version

Remote settings: flag > $DRIVEAGENT_REMOTE_URL / $DRIVEAGENT_LAN_ADDR > <state-dir>/config.json > https://sm.jkurapati.com.
Every scan uploads what it writes; log in first ("driveagent login"), and run "driveagent sync" for older data.
Exit codes: 0 ok, 1 local error, 2 usage, 3 remote unavailable, login needed or upload failed, 4 driveagent upgrade required,
130 interrupted (Ctrl-C; a second Ctrl-C stops the upload too), 143 SIGTERM.

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

// runScan is "driveagent scan". Every scan uploads what it writes, and the
// upload is part of its success (docs/specs/remote-sync-agent.md, "Remote
// is required"), in this order:
//
//  1. take the drive's upload lock, waiting for a sync of it to finish:
//     the only thing keeping two scans of one drive apart, and keeping
//     --replace-root's ClearDrive away from a sync of the drive;
//  2. preflight (health, handshake, token), before the drive or state.db
//     is touched: exit 3 or 4 on failure;
//  3. scan.Prepare (the drive-root check; --replace-root clears the drive
//     here, so it gets a new stream before anything is uploaded);
//  4. the wrong-drive guard;
//  5. read S, the clock: everything of the drive above it is this scan's;
//  6. open the drive and reconcile;
//  7. the session's from (ScanFrom), and the history hint;
//  8. start the uploader;
//  9. scan.Run;
//  10. drain: upload the rest under --remote-timeout.
//
// drain is cancelled by a second Ctrl-C, which aborts step 10.
func runScan(ctx, drain context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	driveID := fs.String("drive-id", "", "label for this drive (required)")
	path := fs.String("path", "", "the specific folder to walk and hash this invocation (required)")
	driveRoot := fs.String("drive-root", "", "stable anchor for this drive's relative paths, e.g. its mount point (defaults to --path)")
	backupRoot := fs.String("backup-root", "", "where mirrored backup content starts, relative to --drive-root (only needed once per drive; omit to leave unset/unchanged)")
	rf := addRemoteFlags(fs)
	workers := fs.Int("workers", 2, "concurrent hashing workers (keep low for spinning USB drives)")
	replaceRoot := fs.Bool("replace-root", false, "allow --drive-id to be repointed at a different --drive-root than it was last scanned at, discarding that drive-id's old checkpoint data first")
	acceptIdentity := fs.Bool("accept-identity-change", false, "scan even though the drive at --drive-root has a different filesystem ID than --drive-id was last scanned on (e.g. it was reformatted); keeps the checkpoint data")
	remoteTimeout := fs.Duration("remote-timeout", syncer.DefaultRemoteTimeout, "stop the scan once the remote has failed for this long")
	if err := fs.Parse(args); err != nil {
		return usageErr("%v", err)
	}
	if *driveID == "" || *path == "" {
		fs.Usage()
		return usageErr("--drive-id and --path are required")
	}
	if *remoteTimeout <= 0 {
		return usageErr("scan: --remote-timeout must be positive")
	}
	stateDir := *rf.stateDir

	// 1. The upload lock.
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}
	lock, err := syncer.WaitLock(ctx, stateDir, *driveID, func() {
		fmt.Fprintf(stderr, "waiting for \"driveagent sync\" to finish uploading %s\n", *driveID)
	})
	if err != nil {
		return err
	}
	defer lock.Unlock()

	// 2. Preflight.
	env, err := rf.open()
	if err != nil {
		return err
	}
	hs, err := preflight(ctx, env.client, stderr)
	if err != nil {
		return remoteErr(err)
	}
	sess := env.session()
	if _, err := sess.AccessToken(ctx); err != nil {
		return remoteErr(err)
	}

	st, err := store.Open(stateDir)
	if err != nil {
		return err
	}
	defer st.Close()

	fmt.Fprintf(stdout, "scanning %q as drive %q (state: %s, remote: %s)\n", *path, *driveID, stateDir, env.settings.RemoteURL)
	opts := scan.Options{
		DriveID:     *driveID,
		RootPath:    *path,
		DriveRoot:   *driveRoot,
		BackupRoot:  *backupRoot,
		Workers:     *workers,
		ReplaceRoot: *replaceRoot,
	}

	// 3, 4. Drive checks, before anything is walked: the drive-root check
	// (with --replace-root, its old data is cleared here), recording the
	// drive, and the wrong-drive guard.
	prepared, err := scan.Prepare(st, opts)
	if err != nil {
		return err
	}
	if prepared.Replaced {
		fmt.Fprintf(stdout, "discarded drive %q's checkpoint data from its previous drive-root (--replace-root)\n", *driveID)
	}
	if err := checkDriveIdentity(st, prepared, *acceptIdentity, stderr); err != nil {
		return err
	}

	// 5. S.
	S, err := st.Clock()
	if err != nil {
		return err
	}

	// 6, 7. Open the drive; where the session starts; the history hint.
	feed, err := syncer.OpenFeed(stateDir)
	if err != nil {
		return err
	}
	defer feed.Close()
	u := &syncer.Uploader{
		Store: st, Feed: feed, Client: env.client, Tokens: sess, AgentID: env.agentID,
		Limits: hs.Limits, RemoteTimeout: *remoteTimeout, Log: stderr,
	}
	var res syncer.Result
	d, err := u.Open(ctx, *driveID, &res)
	if err != nil {
		return syncErr(err)
	}
	if res.NewStream != "" {
		fmt.Fprintf(stdout, "note: drive %q started over on a new stream; run \"driveagent sync\" to re-upload its history\n", *driveID)
	}
	from, err := u.ScanFrom(ctx, d, S)
	if err != nil {
		return err
	}
	if n, err := u.DrivesWithHistory(ctx, S); err == nil && n > 0 {
		fmt.Fprintf(stdout, "note: %d drive(s) have history not yet uploaded; run \"driveagent sync\"\n", n)
	}

	// 8. The uploader, beside the scan. A remote failure stops the scan
	// with errRemoteFailed as the cause.
	scanCtx, stopScan := context.WithCancelCause(ctx)
	defer stopScan(nil)
	wake, done := make(chan struct{}, 1), make(chan struct{})
	upErr := make(chan error, 1)
	go func() {
		err := u.Stream(drain, d, from, wake, done, &res)
		if err != nil {
			stopScan(errRemoteFailed)
		}
		upErr <- err
	}()
	opts.OnFlush = func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	opts.Progress = func(s scan.Stats) {
		fmt.Fprintf(stdout, "  ...seen=%d skipped=%d hashed=%d errored=%d bytes_hashed=%d %s\n",
			s.FilesSeen, s.FilesSkipped, s.FilesHashed, s.FilesErrored, s.BytesHashed, uploadProgress(ctx, u, feed, *driveID))
	}

	// 9. Scan.
	stats, scanErr := scan.Run(scanCtx, st, prepared, opts)
	fmt.Fprintf(stdout, "done in %s: seen=%d skipped=%d hashed=%d errored=%d bytes_hashed=%d deleted=%d\n",
		stats.Elapsed.Round(time.Second), stats.FilesSeen, stats.FilesSkipped, stats.FilesHashed, stats.FilesErrored, stats.BytesHashed, stats.FilesDeleted)
	if stats.DeletionsSkipped {
		fmt.Fprintln(stdout, "note: skipped deletion detection because this walk hit an unreadable file/directory — a clean rescan (no warnings above) is needed to detect files removed from disk.")
	}

	// 10. Drain.
	close(done)
	if context.Cause(scanCtx) != errRemoteFailed {
		fmt.Fprintln(stdout, "uploading what the scan recorded…")
	}
	uploadErr := <-upErr
	if uploadErr == nil {
		fmt.Fprintf(stdout, "uploaded %s changes to %s\n", count(int64(res.Uploaded)), env.settings.RemoteURL)
	}
	if res.Rejected > 0 {
		fmt.Fprintf(stdout, "note: the server rejected %s entries; see \"driveagent remote-status\"\n", count(int64(res.Rejected)))
	}

	var interrupted *scan.Interrupted
	switch {
	case ctx.Err() != nil:
		// Ctrl-C or SIGTERM: main exits 130 or 143.
		fmt.Fprintln(stdout, "scan stopped early: interrupted\nre-run the same command to resume — already-hashed files will be skipped.")
		if uploadErr != nil {
			fmt.Fprintln(stdout, `the upload didn't finish: run "driveagent sync" to upload what this scan recorded.`)
		}
		return context.Cause(ctx)
	case uploadErr != nil:
		what := "scan stopped"
		if scanErr == nil {
			what = "the scan finished, but its upload didn't"
		}
		return syncErr(fmt.Errorf("%w\n%s. Local checkpoint is intact — re-run to resume, and run \"driveagent sync\" to upload what this scan already recorded", uploadErr, what))
	case errors.As(scanErr, &interrupted):
		// The drive went away mid-scan: incomplete, so not a success.
		fmt.Fprintf(stdout, "scan stopped early: %s\nre-run the same command to resume — already-hashed files will be skipped.\n", interrupted.Reason)
		return &exitError{code: exitLocal, err: scanErr}
	}
	return scanErr
}

// errRemoteFailed is the scan's cancel cause when its upload fails.
var errRemoteFailed = errors.New("the upload failed")

// uploadProgress is the upload's part of scan's progress line.
func uploadProgress(ctx context.Context, u *syncer.Uploader, feed *syncer.Feed, driveID string) string {
	s := u.Status()
	if s.RetryLeft > 0 {
		return fmt.Sprintf("remote: retrying (%s left)", s.RetryLeft.Round(time.Second))
	}
	pending, err := feed.Count(ctx, driveID, []wire.Range{{s.Cursor, math.MaxInt64}})
	if err != nil {
		return fmt.Sprintf("uploaded %d", s.Uploaded)
	}
	return fmt.Sprintf("uploaded %d / pending %d", s.Uploaded, pending)
}

// detectIdentity is identity.Detect; tests replace it.
var detectIdentity = identity.Detect

// checkDriveIdentity reads the identity of the drive at the drive root and
// compares it with what's recorded for the drive id: a different filesystem
// is refused; anything else is recorded, with a warning where it's notable.
// After --replace-root the drive starts over, so there's nothing to compare.
func checkDriveIdentity(st *store.Store, p scan.Prepared, accept bool, stderr io.Writer) error {
	found, err := detectIdentity(p.DriveRoot)
	if err != nil {
		fmt.Fprintf(stderr, "warning: couldn't read the identity of the drive at %s: %v\n", p.DriveRoot, err)
	}
	rec, err := st.GetDriveIdentity(p.DriveID)
	if err != nil {
		return err
	}
	stored := identity.Identity{FSUUID: rec.FSUUID, FSType: rec.FSType, Source: rec.FSUUIDSource, HWSerial: rec.HWSerial}
	if p.Replaced {
		stored = identity.Identity{}
	}
	d := identity.Check(p.DriveID, p.DriveRoot, stored, found, accept)
	if d.Refusal != "" {
		return errors.New(d.Refusal)
	}
	for _, w := range d.Warnings {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	if !d.Save && !rec.SeenAt.IsZero() {
		return nil
	}
	return st.SetDriveIdentity(p.DriveID, store.DriveIdentity{
		FSUUID: d.Record.FSUUID, FSType: d.Record.FSType, FSUUIDSource: d.Record.Source, HWSerial: d.Record.HWSerial,
		SeenAt: time.Now(),
	})
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
