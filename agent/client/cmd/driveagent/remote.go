package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/jyothri/bhandaar/agent/client/internal/config"
	"github.com/jyothri/bhandaar/agent/client/internal/creds"
	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
	"github.com/jyothri/bhandaar/agent/wire"
)

// Exit codes (docs/specs/remote-sync-agent.md, "Exit codes"). PR 3 applies
// them to scan too.
const (
	exitLocal   = 1
	exitUsage   = 2
	exitRemote  = 3
	exitUpgrade = 4
)

// exitError makes main exit with code.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func usageErr(format string, a ...any) error {
	return &exitError{code: exitUsage, err: fmt.Errorf(format, a...)}
}

// remoteErr gives a remote failure its exit code: 4 for an upgrade, else 3.
func remoteErr(err error) error {
	if err == nil {
		return nil
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return err
	}
	if errors.Is(err, remote.ErrUpgrade) {
		return &exitError{code: exitUpgrade, err: err}
	}
	return &exitError{code: exitRemote, err: err}
}

// remoteFlags are the flags every remote command takes.
type remoteFlags struct {
	stateDir, remoteURL, lanAddr *string
}

func addRemoteFlags(fs *flag.FlagSet) remoteFlags {
	return remoteFlags{
		stateDir:  fs.String("state-dir", defaultStateDir(), "directory holding the checkpoint database and credentials"),
		remoteURL: fs.String("remote-url", "", "server URL (default: $"+config.EnvRemoteURL+", then config.json, then "+config.DefaultRemoteURL+")"),
		lanAddr:   fs.String("lan-addr", "", "host:port of the server on the LAN, tried before DNS (default: $"+config.EnvLANAddr+", then config.json)"),
	}
}

// remoteEnv is what a remote command works with.
type remoteEnv struct {
	stateDir string
	settings config.Settings
	agentID  string
	client   *remote.Client
}

// clientOptions lets tests point the client at a fake server.
var clientOptions = func(o remote.Options) remote.Options { return o }

func (f remoteFlags) open() (*remoteEnv, error) {
	s, err := config.Resolve(*f.stateDir, config.Flags{RemoteURL: *f.remoteURL, LANAddr: *f.lanAddr}, os.Getenv)
	if err != nil {
		return nil, usageErr("%v", err)
	}
	id, err := creds.AgentID(*f.stateDir)
	if err != nil {
		return nil, fmt.Errorf("agent identity: %w", err)
	}
	c, err := remote.New(clientOptions(remote.Options{RemoteURL: s.RemoteURL, AgentID: id, LANAddr: s.LANAddr}))
	if err != nil {
		return nil, usageErr("%v", err)
	}
	return &remoteEnv{stateDir: *f.stateDir, settings: s, agentID: id, client: c}, nil
}

func (e *remoteEnv) session() *creds.Session {
	return &creds.Session{StateDir: e.stateDir, RemoteURL: e.settings.RemoteURL, Refresh: e.client.Refresh}
}

// Health is retried twice, a second apart, with 5 s per attempt.
var (
	healthTimeout = 5 * time.Second
	healthRetries = 2
	healthPause   = time.Second
)

func health(ctx context.Context, c *remote.Client) (wire.HealthResponse, error) {
	var (
		h   wire.HealthResponse
		err error
	)
	for attempt := 0; attempt <= healthRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return h, ctx.Err()
			case <-time.After(healthPause):
			}
		}
		actx, cancel := context.WithTimeout(ctx, healthTimeout)
		h, err = c.Health(actx)
		cancel()
		if err == nil || ctx.Err() != nil {
			return h, err
		}
	}
	return h, err
}

// preflight is health, then handshake. Nothing that sends credentials runs
// before it succeeds.
func preflight(ctx context.Context, c *remote.Client, stderr io.Writer) (wire.HandshakeResponse, error) {
	if _, err := health(ctx, c); err != nil {
		return wire.HandshakeResponse{}, fmt.Errorf("%s is not reachable: %w", c.BaseURL(), err)
	}
	hs, err := c.Handshake(ctx)
	if err != nil {
		return hs, err
	}
	if hs.Decision == wire.DecisionUpgradeRecommended && hs.Message != "" {
		fmt.Fprintf(stderr, "note: %s (%s)\n", hs.Message, hs.DownloadURL)
	}
	return hs, nil
}

func runLogin(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	username := fs.String("username", "", "username (prompted for if omitted)")
	passwordStdin := fs.Bool("password-stdin", false, "read the password from standard input instead of prompting")
	if err := fs.Parse(args); err != nil {
		return usageErr("%v", err)
	}
	if fs.NArg() > 0 {
		return usageErr("login: unexpected arguments %v (the password is never taken from the command line; use --password-stdin)", fs.Args())
	}
	if *passwordStdin && *username == "" {
		return usageErr("login: --password-stdin needs --username")
	}
	env, err := rf.open()
	if err != nil {
		return err
	}
	if _, err := preflight(ctx, env.client, stderr); err != nil {
		return remoteErr(err)
	}

	in := bufio.NewReader(stdin)
	user := *username
	if user == "" {
		fmt.Fprint(stderr, "Username: ")
		line, err := in.ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("reading username: %w", err)
		}
		user = strings.TrimSpace(line)
		if user == "" {
			return usageErr("login: a username is required")
		}
	}
	password, err := readPassword(stdin, in, stderr, *passwordStdin)
	if err != nil {
		return err
	}

	hostname, _ := os.Hostname()
	tr, err := env.client.Login(ctx, user, password, hostname)
	if err != nil {
		var re *remote.Error
		if errors.As(err, &re) && re.Code == wire.CodeInvalidCredentials {
			err = errors.New("login failed: invalid username or password")
		}
		return remoteErr(err)
	}
	if err := creds.Save(env.stateDir, creds.FromTokens(env.settings.RemoteURL, tr, time.Now())); err != nil {
		return fmt.Errorf("saving credentials: %w", err)
	}
	fmt.Fprintf(stdout, "logged in to %s as %s (%s)\n", env.settings.RemoteURL, tr.User, env.client.Route())
	return nil
}

// readPassword reads the password: all of stdin with --password-stdin,
// otherwise a no-echo prompt on the terminal. It is never taken from argv or
// the environment.
func readPassword(stdin io.Reader, buffered *bufio.Reader, stderr io.Writer, fromStdin bool) (string, error) {
	if fromStdin {
		b, err := io.ReadAll(io.LimitReader(buffered, 4096))
		if err != nil {
			return "", fmt.Errorf("reading password from stdin: %w", err)
		}
		pw := strings.TrimRight(string(b), "\r\n")
		if pw == "" {
			return "", usageErr("login: no password on stdin")
		}
		return pw, nil
	}
	f, ok := stdin.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return "", usageErr("login: stdin is not a terminal; pipe the password in with --password-stdin")
	}
	fmt.Fprint(stderr, "Password: ")
	b, err := term.ReadPassword(int(f.Fd()))
	fmt.Fprintln(stderr)
	if err != nil {
		return "", fmt.Errorf("reading password: %w", err)
	}
	if len(b) == 0 {
		return "", usageErr("login: a password is required")
	}
	return string(b), nil
}

func runLogout(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return usageErr("%v", err)
	}
	c, err := creds.Load(*rf.stateDir)
	if err != nil {
		return err
	}
	if c == nil {
		fmt.Fprintln(stdout, "not logged in")
		return nil
	}
	// Revoke on the server the remote the tokens came from; best effort.
	id, err := creds.AgentID(*rf.stateDir)
	if err != nil {
		return fmt.Errorf("agent identity: %w", err)
	}
	client, err := remote.New(clientOptions(remote.Options{RemoteURL: c.RemoteURL, AgentID: id, LANAddr: lanAddrFor(rf)}))
	if err == nil {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err = client.Logout(lctx, c.RefreshToken)
		cancel()
	}
	if err != nil {
		fmt.Fprintf(stderr, "warning: couldn't revoke the login on %s (%v); removing it locally anyway\n", c.RemoteURL, err)
	}
	if err := creds.Delete(*rf.stateDir); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "logged out of %s\n", c.RemoteURL)
	return nil
}

// lanAddrFor resolves just the LAN address, ignoring a bad remote URL.
func lanAddrFor(rf remoteFlags) string {
	s, err := config.Resolve(*rf.stateDir, config.Flags{LANAddr: *rf.lanAddr}, os.Getenv)
	if err != nil {
		return ""
	}
	return s.LANAddr
}

func runRemoteStatus(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("remote-status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	rf := addRemoteFlags(fs)
	if err := fs.Parse(args); err != nil {
		return usageErr("%v", err)
	}
	env, err := rf.open()
	if err != nil {
		return err
	}
	line := func(k, format string, a ...any) { fmt.Fprintf(stdout, "%-10s %s\n", k, fmt.Sprintf(format, a...)) }

	lan := "none"
	if env.settings.LANAddr != "" {
		lan = env.settings.LANAddr
	}
	line("remote", "%s (lan_addr: %s)", env.settings.RemoteURL, lan)
	line("agent", "%s, %s", env.agentID, version.String())

	var problem error
	h, err := health(ctx, env.client)
	if err != nil {
		line("server", "unreachable: %v", err)
		problem = remoteErr(err)
	} else {
		line("server", "reachable %s, %s %s", env.client.Route(), h.Service, h.ServerVersion)
		hs, err := env.client.Handshake(ctx)
		switch {
		case err != nil:
			line("handshake", "%v", err)
			problem = remoteErr(err)
		case hs.Decision == wire.DecisionUpgradeRecommended:
			line("handshake", "%s, protocol %d: %s (%s)", hs.Decision, hs.Protocol, hs.Message, hs.DownloadURL)
		default:
			line("handshake", "%s, protocol %d", hs.Decision, hs.Protocol)
		}
	}

	sess := env.session()
	c, err := sess.Credentials()
	switch {
	case errors.Is(err, creds.ErrNotLoggedIn):
		if other, _ := creds.Load(env.stateDir); other != nil {
			line("login", "not logged in to this remote (the stored login is for %s)", other.RemoteURL)
		} else {
			line("login", `not logged in: run "driveagent login"`)
		}
		problem = firstErr(problem, &exitError{code: exitRemote, err: creds.ErrNotLoggedIn})
	case err != nil:
		return err
	case problem != nil:
		line("login", "%s (not checked: the server isn't usable)", c.Username)
	default:
		// Checking the login refreshes the access token if it's due.
		if _, err := sess.AccessToken(ctx); err != nil {
			line("login", "%s, but the login no longer works: %v", c.Username, err)
			problem = &exitError{code: exitRemote, err: fmt.Errorf(`the login no longer works: run "driveagent login" (%w)`, err)}
			break
		}
		c, _ = sess.Credentials()
		line("login", "%s, session valid until %s", c.Username, c.RefreshExpiresAt.Local().Format("2006-01-02 15:04"))
	}
	line("drives", "per-drive sync status comes with \"driveagent sync\"")
	return problem
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
