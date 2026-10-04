package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/runlock"
	"github.com/jyothri/bhandaar/agent/client/internal/update"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
	"github.com/jyothri/bhandaar/agent/wire"
)

// driveagent updates itself: on the handshake of scan, sync and login, to
// the server's latest_agent_version, then runs the command again on the
// new binary (docs/specs/agent-auto-update.md).

var (
	// reexec replaces the process with the new binary; tests record the
	// call instead, and it returns.
	reexec = syscall.Exec
	// commandLine is what the re-exec runs again: os.Args[1:], set by main.
	commandLine []string
	// Where releases come from, and the binary they replace; tests replace
	// both.
	updateSource = update.DefaultSource
	executable   = update.Executable
	// releaseBuild: only CI's builds, with the commit stamped in, update
	// themselves; a local go build never is replaced.
	releaseBuild = func() bool { return version.Commit != "unknown" }
	// A required update on a busy machine waits this long for the others
	// to finish, trying every requiredPoll.
	requiredWait = 10 * time.Minute
	requiredPoll = 30 * time.Second
)

// errReexeced is what selfUpdate returns where the process would have been
// replaced: only when reexec returns without an error, as in tests.
var errReexeced = errors.New("re-exec'd the updated driveagent")

// handshake is preflight without its note: health, then the handshake.
func handshake(ctx context.Context, c *remote.Client) (wire.HandshakeResponse, error) {
	if _, err := health(ctx, c); err != nil {
		return wire.HandshakeResponse{}, fmt.Errorf("%s is not reachable: %w", c.BaseURL(), err)
	}
	return c.Handshake(ctx)
}

// selfUpdate is preflight for the commands that update themselves (docs/
// specs/agent-auto-update.md, "When it updates"): it handshakes, and if
// the server names a newer version, installs it and re-runs the command on
// it, never returning. When it doesn't update, it says why and returns the
// handshake as preflight would: carrying on for upgrade_recommended,
// exiting 4 for upgrade_required. Run it before taking any other lock or
// touching state.db.
func selfUpdate(ctx context.Context, env *remoteEnv, inst *runlock.Membership, stderr io.Writer) (wire.HandshakeResponse, error) {
	hs, herr := handshake(ctx, env.client)
	if hs.Decision == "" {
		return hs, herr // no answer
	}
	latest := hs.LatestAgentVersion
	if !update.Newer(version.Version, latest) {
		noteUpgrade(hs, stderr)
		return hs, herr
	}
	// Required: upgrade_required, and also unsupported_protocol, on
	// purpose, when the server names a newer version: that's the fix. Any
	// other error isn't an upgrade, and stands.
	required := errors.Is(herr, remote.ErrUpgrade)
	if herr != nil && !required {
		return hs, herr
	}
	exe, why := canUpdate(latest)
	if why != "" {
		return hs, notUpdated(stderr, herr, latest, why)
	}

	// Alone, or (for a required version) wait to be.
	ok, err := inst.TryAlone(ctx)
	if err != nil {
		return hs, err
	}
	if !ok && required {
		fmt.Fprintf(stderr, "driveagent %s is required; waiting for the other driveagent to finish\n", latest)
		for deadline := time.Now().Add(requiredWait); !ok && time.Now().Before(deadline); {
			select {
			case <-ctx.Done():
				return hs, ctx.Err()
			case <-time.After(requiredPoll):
			}
			if ok, err = inst.TryAlone(ctx); err != nil {
				return hs, err
			}
		}
	}
	if !ok {
		if required {
			return hs, &exitError{code: exitUpgrade, err: fmt.Errorf("%v\ndriveagent %s is required, and will be installed by the next run once the other driveagent has finished", herr, latest)}
		}
		fmt.Fprintf(stderr, "note: driveagent %s is available (this is %s); it will be installed when no other driveagent is running\n", latest, version.Version)
		return hs, nil
	}

	fmt.Fprintf(stderr, "updating driveagent %s → %s\n", version.Version, latest)
	lockDir, _ := runlock.Dir()
	bin, err := updateSource().Fetch(ctx, latest)
	if err == nil {
		err = update.Install(ctx, exe, bin, latest)
	}
	if err != nil {
		if eerr := inst.EndUpdate(); eerr != nil {
			return hs, leftInstance(eerr)
		}
		if ctx.Err() != nil {
			// Interrupted (Ctrl-C, SIGTERM): not a failed release.
			return hs, ctx.Err()
		}
		if lockDir != "" {
			update.RecordFailure(lockDir, latest, err)
		}
		return hs, notUpdated(stderr, herr, latest, "couldn't update: "+err.Error())
	}
	if lockDir != "" {
		update.ClearFailure(lockDir)
	}
	fmt.Fprintf(stderr, "updated driveagent %s → %s\n", version.Version, latest)

	// Run the command again on the new binary, handing it the instance
	// lock (docs/specs/agent-auto-update.md, "Handing the lock to the new
	// binary").
	fd, err := inst.HandOver()
	if err == nil {
		argv := append([]string{exe}, commandLine...)
		envv := append(withoutUpdateEnv(os.Environ()),
			update.EnvUpdatedFrom+"="+version.Version, update.EnvInstanceFD+"="+strconv.Itoa(int(fd)))
		if err = reexec(exe, argv, envv); err == nil {
			return hs, errReexeced
		}
		if terr := inst.TakeBack(ctx); terr != nil {
			return hs, leftInstance(terr)
		}
	} else if eerr := inst.EndUpdate(); eerr != nil {
		return hs, leftInstance(eerr)
	}
	return hs, &exitError{code: exitLocal, err: fmt.Errorf("updated to driveagent %s, but couldn't run it: %v; run the command again", latest, err)}
}

// leftInstance is a failure to rejoin the instance after an update that
// didn't happen: the command can't carry on outside it.
func leftInstance(err error) error {
	return &exitError{code: exitLocal, err: fmt.Errorf("after the update: %w", err)}
}

// canUpdate is the binary an automatic update would replace, or why there
// won't be one.
func canUpdate(latest string) (exe, why string) {
	src := updateSource()
	switch {
	case !update.Published(src.GOOS, src.GOARCH):
		return "", "no driveagent builds are published for " + src.GOOS + "/" + src.GOARCH
	case !releaseBuild():
		return "", "this build isn't a release, so it doesn't update itself"
	case os.Getenv(update.EnvUpdatedFrom) != "":
		return "", "this run already updated driveagent from " + os.Getenv(update.EnvUpdatedFrom)
	case os.Getenv(update.EnvNoAutoUpdate) != "":
		return "", "automatic updates are off ($" + update.EnvNoAutoUpdate + "); run \"driveagent update\""
	}
	if dir, err := runlock.Dir(); err == nil {
		if f := update.LoadFailure(dir); f.Blocks(latest, time.Now()) {
			return "", fmt.Sprintf("updating to it failed at %s (%s); it's tried again an hour later, or run \"driveagent update\"",
				f.FailedAt.Local().Format("15:04"), f.Error)
		}
	}
	exe, err := executable()
	if err != nil {
		return "", "the running driveagent can't be found: " + err.Error()
	}
	if err := update.CheckWritable(exe); err != nil {
		return "", fmt.Sprintf("%s, so it can't update itself; install driveagent somewhere you can write, such as ~/.local/bin", err)
	}
	return exe, ""
}

// notUpdated reports an update that won't happen: a note, carrying on, for
// a recommended version; exit 4, with how to install it by hand, for a
// required one (herr is the handshake's ErrUpgrade).
func notUpdated(stderr io.Writer, herr error, latest, why string) error {
	if herr != nil {
		return &exitError{code: exitUpgrade, err: fmt.Errorf("%v\ndriveagent %s can't be installed automatically: %s.\nInstall it by hand: %s",
			herr, latest, why, update.ReleasePage(latest))}
	}
	fmt.Fprintf(stderr, "note: driveagent %s is available (this is %s), but %s\n", latest, version.Version, why)
	return nil
}

// withoutUpdateEnv drops what a previous re-exec handed on.
func withoutUpdateEnv(env []string) []string {
	var out []string
	for _, kv := range env {
		if !strings.HasPrefix(kv, update.EnvUpdatedFrom+"=") && !strings.HasPrefix(kv, update.EnvInstanceFD+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// updateStatus is remote-status's line about updates; it never updates.
func updateStatus(hs wire.HandshakeResponse) string {
	latest := hs.LatestAgentVersion
	if !update.Newer(version.Version, latest) {
		return "driveagent " + version.Version + " is current"
	}
	available := "driveagent " + latest + " is available"
	if _, why := canUpdate(latest); why != "" {
		return available + ", but " + why
	}
	return available + "; it will be installed by the next scan, sync or login"
}

// runUpdate is "driveagent update": update now, to the server's
// latest_agent_version (or --version), without re-running anything
// (docs/specs/agent-auto-update.md, "driveagent update").
func runUpdate(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	check := fs.Bool("check", false, "only say whether a newer version is available")
	want := fs.String("version", "", "install exactly this version, even an older one (a signed release)")
	if err := fs.Parse(args); err != nil {
		return usageErr("%v", err)
	}
	if fs.NArg() > 0 {
		return usageErr("update: unexpected arguments %v", fs.Args())
	}
	inst, err := joinInstance(ctx, *rf.stateDir, false)
	if err != nil {
		return err
	}
	defer inst.Release()

	target := *want
	if target != "" && !update.Valid(target) {
		return usageErr("update: --version %q isn't a version (MAJOR.MINOR.PATCH, such as 0.7.1)", target)
	}
	if target == "" {
		env, err := rf.open()
		if err != nil {
			return err
		}
		hs, herr := handshake(ctx, env.client)
		if hs.Decision == "" {
			return remoteErr(herr)
		}
		target = hs.LatestAgentVersion
		if !update.Newer(version.Version, target) {
			fmt.Fprintf(stdout, "driveagent %s is current\n", version.Version)
			return nil
		}
	}
	if target == version.Version {
		fmt.Fprintf(stdout, "driveagent %s is current\n", version.Version)
		return nil
	}
	if *check {
		fmt.Fprintf(stdout, "driveagent %s is available (this is %s)\n", target, version.Version)
		return nil
	}

	exe, err := executable()
	if err != nil {
		return err
	}
	if err := update.CheckWritable(exe); err != nil {
		return fmt.Errorf("%w; install driveagent somewhere you can write, such as ~/.local/bin", err)
	}
	ok, err := inst.TryAlone(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return &exitError{code: exitBusy, err: errors.New("another driveagent is running on this machine; update once it has finished")}
	}
	defer inst.EndUpdate()
	fmt.Fprintf(stderr, "downloading driveagent %s\n", target)
	bin, err := updateSource().Fetch(ctx, target)
	if err == nil {
		err = update.Install(ctx, exe, bin, target)
	}
	if err != nil {
		return fmt.Errorf("couldn't update to driveagent %s: %w", target, err)
	}
	if dir, err := runlock.Dir(); err == nil {
		update.ClearFailure(dir)
	}
	fmt.Fprintf(stdout, "updated driveagent %s → %s (the previous one is %s)\n", version.Version, target, filepath.Base(exe)+".prev")
	return nil
}
