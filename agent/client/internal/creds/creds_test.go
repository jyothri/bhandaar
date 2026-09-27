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

// onMachine makes MachineID return id for the test.
func onMachine(t *testing.T, id string) {
	t.Helper()
	old := MachineID
	MachineID = func() string { return id }
	t.Cleanup(func() { MachineID = old })
}

func readAgent(t *testing.T, dir string) agentFile {
	t.Helper()
	f, err := readAgentFile(filepath.Join(dir, AgentFile))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAgentIDRecordsTheMachine(t *testing.T) {
	dir := t.TempDir()
	onMachine(t, "m1")
	id, err := AgentID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if f := readAgent(t, dir); f.MachineID != "m1" || f.AgentID != id {
		t.Errorf("agent.json = %+v", f)
	}
}

// An agent.json from before 0.5.0 adopts the machine it's used on, keeping
// its agent id.
func TestAgentIDAdoptsTheMachine(t *testing.T) {
	dir := t.TempDir()
	const old = "3f2c1d7e-0000-4000-8000-000000000001"
	os.WriteFile(filepath.Join(dir, AgentFile), []byte(`{"agent_id":"`+old+`"}`), 0o600)
	onMachine(t, "m1")
	if id, err := AgentID(dir); err != nil || id != old {
		t.Fatalf("AgentID = %q, %v", id, err)
	}
	if f := readAgent(t, dir); f.MachineID != "m1" || f.AgentID != old {
		t.Errorf("agent.json = %+v", f)
	}
	if m := mode(t, filepath.Join(dir, AgentFile)); m != 0o600 {
		t.Errorf("agent.json mode = %v", m)
	}
	noTempFiles(t, dir)
}

func TestAgentIDOnAnotherMachine(t *testing.T) {
	dir := t.TempDir()
	onMachine(t, "0123456789abcdef-linux-box")
	id, err := AgentID(dir)
	if err != nil {
		t.Fatal(err)
	}
	onMachine(t, "fedcba9876543210-mac")
	_, err = AgentID(dir)
	var mm *MachineMismatchError
	if !errors.As(err, &mm) || mm.AgentID != id || mm.Stored != "0123456789abcdef-linux-box" {
		t.Fatalf("on another machine: %v", err)
	}
	if !strings.Contains(err.Error(), "driveagent login --new-agent") || !strings.Contains(err.Error(), "machine 01234567…") {
		t.Errorf("message: %v", err)
	}
	// A machine whose id can't be read isn't a mismatch.
	onMachine(t, "")
	if got, err := AgentID(dir); err != nil || got != id {
		t.Errorf("unknown machine: %q, %v", got, err)
	}
}

func TestNewAgent(t *testing.T) {
	dir := t.TempDir()
	onMachine(t, "m1")
	old, err := AgentID(dir)
	if err != nil {
		t.Fatal(err)
	}
	onMachine(t, "m2")
	id, err := NewAgent(dir)
	if err != nil || id == old {
		t.Fatalf("NewAgent = %q, %v (old %q)", id, err, old)
	}
	if got, err := AgentID(dir); err != nil || got != id {
		t.Errorf("AgentID after NewAgent = %q, %v", got, err)
	}
	if f := readAgent(t, dir); f.MachineID != "m2" {
		t.Errorf("agent.json = %+v", f)
	}
	noTempFiles(t, dir)
}

func TestParseIOPlatformUUID(t *testing.T) {
	out := []byte(`+-o J316sAP  <class IOPlatformExpertDevice, id 0x100000224, registered, matched, active, busy 0 (37 ms), retain 34>
    {
      "IOPlatformSerialNumber" = "C02XXXXXXX"
      "IOPlatformUUID" = "4C4C4544-0000-1000-8000-B1C04F4E3332"
      "model" = <"MacBookPro18,1">
    }
`)
	if got := parseIOPlatformUUID(out); got != "4c4c4544-0000-1000-8000-b1c04f4e3332" {
		t.Errorf("got %q", got)
	}
	if got := parseIOPlatformUUID([]byte("nothing")); got != "" {
		t.Errorf("got %q", got)
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
