package creds

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/wire"
)

const remoteURL = "https://sm.jkurapati.com"

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(dir, ".tmp-*"))
	if len(matches) > 0 {
		t.Errorf("temp files left: %v", matches)
	}
}

func TestAgentID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state") // doesn't exist yet
	id, err := AgentID(dir)
	if err != nil {
		t.Fatal(err)
	}
	u, err := uuid.Parse(id)
	if err != nil || u.Version() != 4 {
		t.Errorf("agent id %q is not a UUID v4", id)
	}
	if again, _ := AgentID(dir); again != id {
		t.Errorf("second call gave %q, want %q", again, id)
	}
	if m := mode(t, filepath.Join(dir, AgentFile)); m != 0o600 {
		t.Errorf("agent.json mode = %v", m)
	}
	noTempFiles(t, dir)
}

func TestAgentIDConcurrentCreation(t *testing.T) {
	dir := t.TempDir()
	ids := make([]string, 20)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			if ids[i], err = AgentID(dir); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("different ids: %v", ids)
		}
	}
	noTempFiles(t, dir)
}

func TestAgentIDCorrupt(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, AgentFile), []byte(`{"agent_id":"nope"}`), 0o600)
	if _, err := AgentID(dir); err == nil {
		t.Error("want an error for a non-UUID agent id")
	}
}

func TestSaveLoadDelete(t *testing.T) {
	dir := t.TempDir()
	if c, err := Load(dir); c != nil || err != nil {
		t.Fatalf("empty dir: %v, %v", c, err)
	}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	c := FromTokens(remoteURL, wire.TokenResponse{AccessToken: "at", AccessExpiresIn: 900, RefreshToken: "rt_1",
		RefreshExpiresIn: 2592000, User: "jyothri"}, now)
	if !c.AccessExpiresAt.Equal(now.Add(15*time.Minute)) || !c.RefreshExpiresAt.Equal(now.Add(720*time.Hour)) {
		t.Errorf("expiries = %v, %v", c.AccessExpiresAt, c.RefreshExpiresAt)
	}
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, filepath.Join(dir, CredentialsFile)); m != 0o600 {
		t.Errorf("credentials.json mode = %v", m)
	}
	got, err := Load(dir)
	if err != nil || *got != *c {
		t.Errorf("Load = %+v, %v", got, err)
	}
	// Overwriting keeps the mode and leaves no temp file.
	c.AccessToken = "at2"
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	if m := mode(t, filepath.Join(dir, CredentialsFile)); m != 0o600 {
		t.Errorf("mode after overwrite = %v", m)
	}
	noTempFiles(t, dir)
	if err := Delete(dir); err != nil {
		t.Fatal(err)
	}
	if err := Delete(dir); err != nil {
		t.Errorf("second delete: %v", err)
	}
	if c, _ := Load(dir); c != nil {
		t.Error("credentials survived Delete")
	}
}

// fakeRefresher counts calls and hands out numbered tokens.
type fakeRefresher struct {
	calls atomic.Int32
	delay time.Duration
	err   error
}

func (f *fakeRefresher) refresh(ctx context.Context, rt string) (wire.TokenResponse, error) {
	n := f.calls.Add(1)
	time.Sleep(f.delay)
	if f.err != nil {
		return wire.TokenResponse{}, f.err
	}
	return wire.TokenResponse{AccessToken: "at_new" + string(rune('0'+n)), AccessExpiresIn: 900,
		RefreshToken: "rt_new", RefreshExpiresIn: 2592000, User: "jyothri"}, nil
}

func login(t *testing.T, dir string, accessLeft time.Duration) {
	t.Helper()
	now := time.Now()
	if err := Save(dir, &Credentials{RemoteURL: remoteURL, Username: "jyothri", AccessToken: "at_old",
		AccessExpiresAt: now.Add(accessLeft), RefreshToken: "rt_old", RefreshExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionNotLoggedIn(t *testing.T) {
	dir := t.TempDir()
	f := &fakeRefresher{}
	s := &Session{StateDir: dir, RemoteURL: remoteURL, Refresh: f.refresh}
	if _, err := s.AccessToken(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("no credentials: %v", err)
	}
	// Tokens for another remote don't count.
	login(t, dir, time.Hour)
	other := &Session{StateDir: dir, RemoteURL: "https://dev.sm.jkurapati.com", Refresh: f.refresh}
	if _, err := other.AccessToken(context.Background()); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("another remote: %v", err)
	}
	if f.calls.Load() != 0 {
		t.Error("refresh called")
	}
}

func TestSessionUsesValidToken(t *testing.T) {
	dir := t.TempDir()
	login(t, dir, 10*time.Minute)
	f := &fakeRefresher{}
	s := &Session{StateDir: dir, RemoteURL: remoteURL, Refresh: f.refresh}
	if tok, err := s.AccessToken(context.Background()); err != nil || tok != "at_old" || f.calls.Load() != 0 {
		t.Errorf("token = %q, %v, refreshes = %d", tok, err, f.calls.Load())
	}
}

func TestSessionRefreshesNearExpiry(t *testing.T) {
	dir := t.TempDir()
	login(t, dir, 30*time.Second) // under the 60 s margin
	f := &fakeRefresher{}
	s := &Session{StateDir: dir, RemoteURL: remoteURL, Refresh: f.refresh}
	tok, err := s.AccessToken(context.Background())
	if err != nil || tok != "at_new1" || f.calls.Load() != 1 {
		t.Fatalf("token = %q, %v, refreshes = %d", tok, err, f.calls.Load())
	}
	c, _ := Load(dir)
	if c.RefreshToken != "rt_new" || c.AccessToken != "at_new1" || c.Username != "jyothri" {
		t.Errorf("saved = %+v", c)
	}
}

func TestSessionRenewAfterTokenExpired(t *testing.T) {
	dir := t.TempDir()
	login(t, dir, 10*time.Minute)
	f := &fakeRefresher{}
	s := &Session{StateDir: dir, RemoteURL: remoteURL, Refresh: f.refresh}
	// The server said at_old expired (clock skew): refresh even though it looks valid.
	if tok, err := s.Renew(context.Background(), "at_old"); err != nil || tok != "at_new1" {
		t.Fatalf("renew = %q, %v", tok, err)
	}
	// Renewing a token that was already replaced uses the replacement.
	if tok, err := s.Renew(context.Background(), "at_old"); err != nil || tok != "at_new1" || f.calls.Load() != 1 {
		t.Errorf("second renew = %q, %v, refreshes = %d", tok, err, f.calls.Load())
	}
}

func TestSessionExpiredLogin(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	Save(dir, &Credentials{RemoteURL: remoteURL, AccessToken: "a", AccessExpiresAt: now.Add(-time.Hour),
		RefreshToken: "rt", RefreshExpiresAt: now.Add(-time.Minute)})
	f := &fakeRefresher{}
	s := &Session{StateDir: dir, RemoteURL: remoteURL, Refresh: f.refresh}
	if _, err := s.AccessToken(context.Background()); !errors.Is(err, ErrNotLoggedIn) || f.calls.Load() != 0 {
		t.Errorf("err = %v, refreshes = %d", err, f.calls.Load())
	}
}

func TestSessionRefreshFailureKeepsCredentials(t *testing.T) {
	dir := t.TempDir()
	login(t, dir, 0)
	boom := errors.New("server said no")
	s := &Session{StateDir: dir, RemoteURL: remoteURL, Refresh: (&fakeRefresher{err: boom}).refresh}
	if _, err := s.AccessToken(context.Background()); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	if c, _ := Load(dir); c == nil || c.RefreshToken != "rt_old" {
		t.Errorf("credentials changed: %+v", c)
	}
}

// Two processes (here: two sessions, each with its own lock handle) find the
// token due at the same moment: only one refreshes, and both use its token.
func TestConcurrentRefreshRotatesOnce(t *testing.T) {
	dir := t.TempDir()
	login(t, dir, 0)
	f := &fakeRefresher{delay: 100 * time.Millisecond}
	var wg sync.WaitGroup
	tokens := make([]string, 2)
	for i := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := &Session{StateDir: dir, RemoteURL: remoteURL, Refresh: f.refresh}
			var err error
			if tokens[i], err = s.AccessToken(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if f.calls.Load() != 1 {
		t.Errorf("%d refreshes, want 1", f.calls.Load())
	}
	if tokens[0] != tokens[1] || !strings.HasPrefix(tokens[0], "at_new") {
		t.Errorf("tokens = %v", tokens)
	}
}
