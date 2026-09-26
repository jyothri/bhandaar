package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/creds"
	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/store"
	"github.com/jyothri/bhandaar/agent/client/internal/syncer"
	"github.com/jyothri/bhandaar/agent/wire"
)

// progressEvery is how often sync prints a drive's running total.
var progressEvery = 5 * time.Second

// runSync is "driveagent sync": upload every drive's pending history, with
// no scan and no drive access (docs/specs/remote-sync-agent.md,
// "driveagent sync").
func runSync(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	driveIDs := fs.String("drive-id", "", "comma-separated drive IDs to upload (default: every drive in state.db)")
	remoteTimeout := fs.Duration("remote-timeout", syncer.DefaultRemoteTimeout, "give up once the remote has failed for this long")
	if err := fs.Parse(args); err != nil {
		return usageErr("%v", err)
	}
	if fs.NArg() > 0 {
		return usageErr("sync: unexpected arguments %v", fs.Args())
	}
	if *remoteTimeout <= 0 {
		return usageErr("sync: --remote-timeout must be positive")
	}
	env, err := rf.open()
	if err != nil {
		return err
	}
	if !stateDBExists(env.stateDir) {
		fmt.Fprintf(stdout, "nothing to upload: no state.db in %s\n", env.stateDir)
		return nil
	}
	st, err := store.Open(env.stateDir)
	if err != nil {
		return err
	}
	defer st.Close()
	all, err := st.DriveIDs()
	if err != nil {
		return err
	}
	ids := all
	if want := splitList(*driveIDs); want != nil {
		known := map[string]bool{}
		for _, id := range all {
			known[id] = true
		}
		for _, id := range want {
			if !known[id] {
				return usageErr("sync: no drive %q in %s", id, env.stateDir)
			}
		}
		ids = want
	}

	// Nothing is uploaded before the handshake and the token.
	hs, err := preflight(ctx, env.client, stderr)
	if err != nil {
		return remoteErr(err)
	}
	sess := env.session()
	if _, err := sess.AccessToken(ctx); err != nil {
		return remoteErr(err)
	}

	feed, err := syncer.OpenFeed(env.stateDir)
	if err != nil {
		return err
	}
	defer feed.Close()
	lastProgress := time.Now()
	u := &syncer.Uploader{
		Store: st, Feed: feed, Client: env.client, Tokens: sess, AgentID: env.agentID,
		Limits: hs.Limits, RemoteTimeout: *remoteTimeout, Log: stderr,
		Progress: func(driveID string, uploaded int) {
			if time.Since(lastProgress) >= progressEvery {
				lastProgress = time.Now()
				fmt.Fprintf(stdout, "  %s: uploaded %s changes so far\n", driveID, count(int64(uploaded)))
			}
		},
	}

	for i, id := range ids {
		lock, ok, err := syncer.TryLock(env.stateDir, id)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintf(stdout, "%s: skipped, another driveagent is uploading it (a scan); the next sync picks it up\n", id)
			continue
		}
		res, err := u.SyncDrive(ctx, id)
		lock.Unlock()
		fmt.Fprintln(stdout, syncLine(res, err))
		if err != nil {
			if rest := ids[i+1:]; len(rest) > 0 && ctx.Err() == nil {
				fmt.Fprintf(stdout, "not attempted: %s\n", strings.Join(rest, ", "))
			}
			return syncErr(err)
		}
	}
	return nil
}

// syncLine is sync's report line for one drive.
func syncLine(res syncer.Result, err error) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: uploaded %s changes", res.DriveID, count(int64(res.Uploaded)))
	if res.Rejected > 0 {
		fmt.Fprintf(&b, " (%s rejected by the server; see \"driveagent remote-status\")", count(int64(res.Rejected)))
	}
	switch {
	case res.Pending == 0 && err == nil:
		b.WriteString(", fully synced")
	case res.Pending > 0:
		fmt.Fprintf(&b, ", %s still pending", count(res.Pending))
	}
	if res.NewStream != "" {
		b.WriteString("; started over on a new stream")
	}
	if err != nil {
		fmt.Fprintf(&b, "; stopped: %v", err)
	}
	return b.String()
}

// syncErr gives an upload failure its exit code: 3 or 4 for the remote
// (or a missing login), 1 for anything local.
func syncErr(err error) error {
	for _, kind := range []error{remote.ErrTransient, remote.ErrAuth, remote.ErrUpgrade, remote.ErrPermanent, creds.ErrNotLoggedIn} {
		if errors.Is(err, kind) {
			return remoteErr(err)
		}
	}
	return err
}

func stateDBExists(stateDir string) bool {
	_, err := os.Stat(filepath.Join(stateDir, "state.db"))
	return err == nil
}

// count formats n with thousands separators: 18,532.
func count(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}

// printDrives is remote-status's per-drive section: for each drive in
// state.db, its identity, synced marker, pending entries and rejected
// entries, and (when the server is usable) the server's view and the other
// machines' copies of the same physical drive. A drive is reconciled (PUT
// and the reconcile rules) only if its upload lock is free; for one being
// uploaded, the server's list stands in. Nothing is uploaded.
func printDrives(ctx context.Context, env *remoteEnv, sess *creds.Session, usable bool, stdout io.Writer) error {
	if !stateDBExists(env.stateDir) {
		fmt.Fprintf(stdout, "%-10s none (no state.db in %s)\n", "drives", env.stateDir)
		return nil
	}
	st, err := store.Open(env.stateDir)
	if err != nil {
		return err
	}
	defer st.Close()
	ids, err := st.DriveIDs()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Fprintf(stdout, "%-10s none\n", "drives")
		return nil
	}

	server := map[string]wire.Drive{}
	var problem error
	if usable {
		token, err := sess.AccessToken(ctx)
		var list []wire.Drive
		if err == nil {
			list, err = env.client.ListDrives(ctx, token)
		}
		if err != nil {
			fmt.Fprintf(stdout, "%-10s couldn't list the server's drives: %v\n", "drives", err)
			problem, usable = remoteErr(err), false
		}
		for _, d := range list {
			server[d.DriveID] = d
		}
	}
	u := &syncer.Uploader{Store: st, Client: env.client, Tokens: sess, AgentID: env.agentID, RemoteTimeout: 10 * time.Second}

	for _, id := range ids {
		fmt.Fprintf(stdout, "\ndrive %s\n", id)
		line := func(k, format string, a ...any) { fmt.Fprintf(stdout, "  %-9s %s\n", k, fmt.Sprintf(format, a...)) }
		d, err := st.SyncDrive(id)
		if err != nil {
			return err
		}

		var linked *wire.PhysicalDrive
		serverLine := ""
		switch sd, onServer := server[id]; {
		case !usable:
			serverLine = "not checked"
		case d.Marker.StreamID == "":
			serverLine = `not uploaded yet: run "driveagent sync"`
		default:
			lock, ok, lerr := syncer.TryLock(env.stateDir, id)
			if lerr != nil {
				return lerr
			}
			if !ok {
				serverLine = "being uploaded by another driveagent; showing the server's list"
				if onServer {
					linked = sd.PhysicalDrive
					serverLine += fmt.Sprintf(", acked %v", sd.AckedRanges)
				}
				break
			}
			var res syncer.Result
			_, oerr := u.Open(ctx, id, &res)
			lock.Unlock()
			if oerr != nil {
				serverLine = fmt.Sprintf("couldn't open the drive: %v", oerr)
				problem = firstErr(problem, syncErr(oerr))
				break
			}
			linked = res.Drive.PhysicalDrive
			serverLine = fmt.Sprintf("acked %v", res.Drive.AckedRanges)
			if res.NewStream != "" {
				serverLine += "; started over on a new stream (" + res.NewStream + ")"
			}
			if d, err = st.SyncDrive(id); err != nil {
				return err
			}
		}

		line("root", "%s (backup root %q)", d.DriveRoot, d.BackupRoot)
		line("identity", "%s", identityLine(d.Identity))
		m := d.Marker
		synced := "never"
		if !m.SyncedAt.IsZero() {
			synced = m.SyncedAt.Local().Format("2006-01-02 15:04")
		}
		line("synced", "watermark %s, %d range(s) above it, last synced %s", count(m.Watermark), len(m.Ranges), synced)
		pending, err := st.PendingCount(id)
		if err != nil {
			return err
		}
		line("pending", "%s", count(pending))
		line("server", "%s", serverLine)
		if linked != nil {
			for _, l := range linked.Linked {
				when := "never synced"
				if l.LastSyncedAt != nil {
					when = "last synced " + l.LastSyncedAt.Local().Format("2006-01-02")
				}
				line("linked", "also scanned by: %s as %s, %s", l.Hostname, l.DriveID, when)
			}
			if linked.CloneOf != nil {
				line("linked", "a clone of physical drive %d", *linked.CloneOf)
			}
		}
		rejected, err := st.Rejected(id)
		if err != nil {
			return err
		}
		for i, r := range rejected {
			if i == 10 {
				line("rejected", "… and %d more", len(rejected)-i)
				break
			}
			name := r.Path
			if r.Child != "" {
				name += "/" + r.Child
			}
			line("rejected", "%s %s (version %d): %s", r.Kind, strconv.Quote(name), r.V, r.Reason)
		}
	}

	var extra []string
	for id := range server {
		if !slices.Contains(ids, id) {
			extra = append(extra, id)
		}
	}
	if len(extra) > 0 {
		fmt.Fprintf(stdout, "\nthe server also has drives not in this state.db: %s\n", strings.Join(slices.Sorted(slices.Values(extra)), ", "))
	}
	return problem
}

func identityLine(id store.DriveIdentity) string {
	if id.FSUUID == "" && id.HWSerial == "" {
		return "unknown (can't be linked to other machines' copies)"
	}
	s := fmt.Sprintf("filesystem %s", orNone(id.FSUUID))
	if id.FSType != "" {
		s += " (" + id.FSType + ", " + orNone(id.FSUUIDSource) + ")"
	}
	return s + ", serial " + orNone(id.HWSerial)
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}
