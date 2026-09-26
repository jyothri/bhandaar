package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/creds"
	"github.com/jyothri/bhandaar/agent/client/internal/remote/remotetest"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
	"github.com/jyothri/bhandaar/agent/wire"
)

func init() {
	healthPause = 10 * time.Millisecond
}

type result struct {
	err            error
	stdout, stderr string
}

func (r result) code() int {
	if r.err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(r.err, &ee) {
		return ee.code
	}
	return exitLocal
}

func login(t *testing.T, stateDir, url, stdin string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	args = append([]string{"--state-dir", stateDir, "--remote-url", url}, args...)
	err := runLogin(context.Background(), args, strings.NewReader(stdin), &out, &errOut)
	return result{err, out.String(), errOut.String()}
}

func status(t *testing.T, stateDir, url string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	err := runRemoteStatus(context.Background(), []string{"--state-dir", stateDir, "--remote-url", url}, &out, &errOut)
	return result{err, out.String(), errOut.String()}
}

func TestLoginWithPasswordStdin(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()
	r := login(t, dir, srv.URL, "correct horse battery\n", "--username", "jyothri", "--password-stdin")
	if r.err != nil {
		t.Fatalf("login: %v\n%s", r.err, r.stderr)
	}
	if !strings.Contains(r.stdout, "logged in to "+srv.URL+" as jyothri (via direct") {
		t.Errorf("stdout = %q", r.stdout)
	}
	if got := strings.Join(srv.Paths(), " "); got != "/agent/health /agent/v1/handshake /agent/v1/auth/login" {
		t.Errorf("requests = %s", got)
	}

	id, err := creds.AgentID(dir)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := srv.Last("/agent/v1/auth/login")
	var body wire.LoginRequest
	json.Unmarshal(req.Body, &body)
	host, _ := os.Hostname()
	if body.Username != "jyothri" || body.Password != "correct horse battery" || body.AgentID != id || body.Hostname != host || body.OS == "" {
		t.Errorf("login body = %+v", body)
	}
	if req.Header.Get(wire.HeaderAgentID) != id || req.Header.Get(wire.HeaderAgentVersion) != version.Version ||
		req.Header.Get(wire.HeaderAgentProtocol) != "1" {
		t.Errorf("login headers = %v", req.Header)
	}

	c, _ := creds.Load(dir)
	if c == nil || c.RemoteURL != srv.URL || c.Username != "jyothri" || !strings.HasPrefix(c.RefreshToken, "rt_") {
		t.Fatalf("credentials = %+v", c)
	}
	fi, _ := os.Stat(filepath.Join(dir, creds.CredentialsFile))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("credentials.json mode = %v", fi.Mode().Perm())
	}
}

func TestLoginFailures(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()

	r := login(t, dir, srv.URL, "wrong password\n", "--username", "jyothri", "--password-stdin")
	if r.code() != exitRemote || !strings.Contains(r.err.Error(), "invalid username or password") {
		t.Errorf("wrong password: code %d, %v", r.code(), r.err)
	}
	if c, _ := creds.Load(dir); c != nil {
		t.Error("credentials saved after a failed login")
	}

	for name, c := range map[string]struct {
		stdin string
		args  []string
	}{
		"password-stdin without username": {"pw\n", []string{"--password-stdin"}},
		"password as an argument":         {"", []string{"--username", "jyothri", "hunter2hunter2"}},
		"no terminal, no password-stdin":  {"", []string{"--username", "jyothri"}},
		"empty password on stdin":         {"\n", []string{"--username", "jyothri", "--password-stdin"}},
	} {
		if r := login(t, dir, srv.URL, c.stdin, c.args...); r.code() != exitUsage {
			t.Errorf("%s: code %d, %v", name, r.code(), r.err)
		}
	}
	if strings.Contains(strings.Join(srv.Paths(), " "), "hunter2") {
		t.Error("an argument reached the server")
	}
}

func TestLoginUpgradeRequiredSendsNoCredentials(t *testing.T) {
	srv := remotetest.New(t)
	srv.Decision = wire.DecisionUpgradeRequired
	r := login(t, t.TempDir(), srv.URL, "correct horse battery\n", "--username", "jyothri", "--password-stdin")
	if r.code() != exitUpgrade || !strings.Contains(r.err.Error(), "please upgrade") {
		t.Errorf("code %d, %v", r.code(), r.err)
	}
	if _, sent := srv.Last("/agent/v1/auth/login"); sent {
		t.Error("login was sent despite upgrade_required")
	}
}

func TestLoginServerDown(t *testing.T) {
	srv := remotetest.New(t)
	srv.Down = true
	r := login(t, t.TempDir(), srv.URL, "x\n", "--username", "jyothri", "--password-stdin")
	if r.code() != exitRemote {
		t.Errorf("code %d, %v", r.code(), r.err)
	}
	if n := len(srv.Paths()); n != 1+healthRetries {
		t.Errorf("%d health attempts, want %d", n, 1+healthRetries)
	}
}

func TestLoginUsernamePrompt(t *testing.T) {
	// Without --username the name is read from stdin; the password then
	// needs a terminal, which a test doesn't have.
	srv := remotetest.New(t)
	r := login(t, t.TempDir(), srv.URL, "jyothri\n")
	if r.code() != exitUsage || !strings.Contains(r.stderr, "Username:") {
		t.Errorf("code %d, %v, stderr %q", r.code(), r.err, r.stderr)
	}
}

func TestLogout(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()
	if r := login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin"); r.err != nil {
		t.Fatal(r.err)
	}
	c, _ := creds.Load(dir)
	var out, errOut bytes.Buffer
	if err := runLogout(context.Background(), []string{"--state-dir", dir}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	req, sent := srv.Last("/agent/v1/auth/logout")
	id, _ := creds.AgentID(dir)
	if !sent || !strings.Contains(string(req.Body), c.RefreshToken) || req.Header.Get(wire.HeaderAgentID) != id {
		t.Errorf("logout request = %+v", req)
	}
	if c, _ := creds.Load(dir); c != nil {
		t.Error("credentials survived logout")
	}
	out.Reset()
	if err := runLogout(context.Background(), []string{"--state-dir", dir}, &out, &errOut); err != nil || !strings.Contains(out.String(), "not logged in") {
		t.Errorf("second logout: %v, %q", err, out.String())
	}
}

func TestLogoutServerDownStillForgets(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()
	login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")
	srv.Close()
	var out, errOut bytes.Buffer
	if err := runLogout(context.Background(), []string{"--state-dir", dir}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errOut.String(), "warning") {
		t.Errorf("stderr = %q", errOut.String())
	}
	if c, _ := creds.Load(dir); c != nil {
		t.Error("credentials survived logout")
	}
}

func TestRemoteStatus(t *testing.T) {
	srv := remotetest.New(t)
	dir := t.TempDir()

	r := status(t, dir, srv.URL)
	if r.code() != exitRemote || !strings.Contains(r.stdout, `not logged in: run "driveagent login"`) ||
		!strings.Contains(r.stdout, "reachable via direct") || !strings.Contains(r.stdout, "handshake  ok, protocol 1") {
		t.Errorf("not logged in: code %d\n%s", r.code(), r.stdout)
	}

	login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")
	r = status(t, dir, srv.URL)
	if r.err != nil || !strings.Contains(r.stdout, "login      jyothri, session valid until") {
		t.Errorf("logged in: %v\n%s", r.err, r.stdout)
	}
	if srv.Refreshes != 0 {
		t.Errorf("a valid access token was refreshed")
	}

	// A login for another remote doesn't count for this one.
	other := remotetest.New(t)
	r = status(t, dir, other.URL)
	if r.code() != exitRemote || !strings.Contains(r.stdout, "the stored login is for "+srv.URL) {
		t.Errorf("other remote: code %d\n%s", r.code(), r.stdout)
	}
}

func TestRemoteStatusRefreshesAndDetectsDeadLogin(t *testing.T) {
	srv := remotetest.New(t)
	srv.AccessTTL = 30 // always inside the refresh margin
	dir := t.TempDir()
	login(t, dir, srv.URL, "correct horse battery", "--username", "jyothri", "--password-stdin")

	if r := status(t, dir, srv.URL); r.err != nil || srv.Refreshes != 1 {
		t.Fatalf("status: %v, refreshes %d\n%s", r.err, srv.Refreshes, r.stdout)
	}

	// The server forgets the refresh token (revoked): status says so.
	c, _ := creds.Load(dir)
	c.RefreshToken = "rt_revoked"
	creds.Save(dir, c)
	r := status(t, dir, srv.URL)
	if r.code() != exitRemote || !strings.Contains(r.stdout, "the login no longer works") {
		t.Errorf("code %d\n%s", r.code(), r.stdout)
	}
}

func TestRemoteStatusUnreachable(t *testing.T) {
	srv := remotetest.New(t)
	srv.Down = true
	r := status(t, t.TempDir(), srv.URL)
	if r.code() != exitRemote || !strings.Contains(r.stdout, "unreachable") {
		t.Errorf("code %d\n%s", r.code(), r.stdout)
	}
}

func TestRemoteStatusUpgradeRequired(t *testing.T) {
	srv := remotetest.New(t)
	srv.Decision = wire.DecisionUpgradeRequired
	r := status(t, t.TempDir(), srv.URL)
	if r.code() != exitUpgrade {
		t.Errorf("code %d\n%s", r.code(), r.stdout)
	}
}

func TestBadRemoteURLIsUsage(t *testing.T) {
	r := status(t, t.TempDir(), "http://sm.jkurapati.com")
	if r.code() != exitUsage {
		t.Errorf("code %d, %v", r.code(), r.err)
	}
}
