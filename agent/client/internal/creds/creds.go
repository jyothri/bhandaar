// Package creds keeps the agent's identity and login in the state dir:
// agent.json (the agent id) and credentials.json (the tokens), both 0600.
// See docs/specs/remote-sync-agent.md, "Identity and credentials".
package creds

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/wire"
)

// File names in the state dir.
const (
	AgentFile       = "agent.json"
	CredentialsFile = "credentials.json"
	LockFile        = "credentials.lock"
)

// RefreshMargin: the access token is refreshed when it has less than this left.
const RefreshMargin = 60 * time.Second

// ErrNotLoggedIn means there are no usable credentials for the remote.
var ErrNotLoggedIn = errors.New(`not logged in: run "driveagent login"`)

type agentFile struct {
	AgentID string `json:"agent_id"`
}

// AgentID returns the state dir's agent id, creating it on first use. Two
// processes creating it at once end up with the same id.
func AgentID(stateDir string) (string, error) {
	path := filepath.Join(stateDir, AgentFile)
	if id, err := readAgentID(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return id, err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return "", err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(agentFile{AgentID: id.String()})
	tmp, err := writeTemp(stateDir, b)
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp)
	// Link, unlike rename, fails if agent.json appeared meanwhile, so a
	// racing process's id is never overwritten.
	if err := os.Link(tmp, path); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	return readAgentID(path)
}

func readAgentID(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var f agentFile
	if err := json.Unmarshal(b, &f); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if _, err := uuid.Parse(f.AgentID); err != nil {
		return "", fmt.Errorf("%s: agent_id %q is not a UUID", path, f.AgentID)
	}
	return f.AgentID, nil
}

// Credentials are a login's tokens, bound to the remote they came from.
type Credentials struct {
	RemoteURL        string    `json:"remote_url"`
	Username         string    `json:"username"`
	AccessToken      string    `json:"access_token"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshToken     string    `json:"refresh_token"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// FromTokens builds credentials from a login or refresh response.
func FromTokens(remoteURL string, t wire.TokenResponse, now time.Time) *Credentials {
	return &Credentials{
		RemoteURL:        remoteURL,
		Username:         t.User,
		AccessToken:      t.AccessToken,
		AccessExpiresAt:  now.Add(time.Duration(t.AccessExpiresIn) * time.Second).UTC(),
		RefreshToken:     t.RefreshToken,
		RefreshExpiresAt: now.Add(time.Duration(t.RefreshExpiresIn) * time.Second).UTC(),
	}
}

// Load reads credentials.json; it returns nil, nil when there is none.
func Load(stateDir string) (*Credentials, error) {
	path := filepath.Join(stateDir, CredentialsFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w (run \"driveagent login\" again)", path, err)
	}
	return &c, nil
}

// Save writes credentials.json atomically (temp file + rename), mode 0600.
func Save(stateDir string, c *Credentials) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := writeTemp(stateDir, b)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(stateDir, CredentialsFile)); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Delete removes credentials.json. A missing file is not an error.
func Delete(stateDir string) error {
	err := os.Remove(filepath.Join(stateDir, CredentialsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// writeTemp writes b to a new 0600 file in dir, synced to disk, and
// returns its path.
func writeTemp(dir string, b []byte) (string, error) {
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	name := f.Name()
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(name)
		return "", err
	}
	if err := f.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := f.Write(b); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	return name, nil
}

// Refresher exchanges a refresh token for new tokens (remote.Client.Refresh).
type Refresher func(ctx context.Context, refreshToken string) (wire.TokenResponse, error)

// Session hands out access tokens for one remote, refreshing them when
// needed. Several processes can share a state dir: a refresh happens under
// an exclusive lock on credentials.lock, after re-reading credentials.json,
// so a token another process just rotated is used rather than rotated again.
type Session struct {
	StateDir  string
	RemoteURL string
	Refresh   Refresher
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (s *Session) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Credentials returns the stored login for this remote, or ErrNotLoggedIn.
func (s *Session) Credentials() (*Credentials, error) {
	c, err := Load(s.StateDir)
	if err != nil {
		return nil, err
	}
	if c == nil || c.RemoteURL != s.RemoteURL || c.RefreshToken == "" {
		return nil, ErrNotLoggedIn
	}
	return c, nil
}

// AccessToken returns an access token with at least RefreshMargin left,
// refreshing it if needed.
func (s *Session) AccessToken(ctx context.Context) (string, error) {
	return s.token(ctx, "")
}

// Renew returns a new access token after the server rejected stale (401
// TOKEN_EXPIRED). If another process already replaced stale, its token is
// used.
func (s *Session) Renew(ctx context.Context, stale string) (string, error) {
	return s.token(ctx, stale)
}

func (s *Session) usable(c *Credentials, stale string) bool {
	return c.AccessToken != "" && c.AccessToken != stale && c.AccessExpiresAt.Sub(s.now()) > RefreshMargin
}

func (s *Session) token(ctx context.Context, stale string) (string, error) {
	c, err := s.Credentials()
	if err != nil {
		return "", err
	}
	if s.usable(c, stale) {
		return c.AccessToken, nil
	}

	lock := flock.New(filepath.Join(s.StateDir, LockFile))
	ok, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return "", fmt.Errorf("locking %s: %w", LockFile, err)
	}
	if !ok {
		return "", ctx.Err()
	}
	defer lock.Unlock()

	// Another process may have refreshed while we waited for the lock.
	if c, err = s.Credentials(); err != nil {
		return "", err
	}
	if s.usable(c, stale) {
		return c.AccessToken, nil
	}
	if !s.now().Before(c.RefreshExpiresAt) {
		return "", fmt.Errorf("the login has expired: %w", ErrNotLoggedIn)
	}
	t, err := s.Refresh(ctx, c.RefreshToken)
	if err != nil {
		return "", err
	}
	fresh := FromTokens(s.RemoteURL, t, s.now())
	if fresh.Username == "" {
		fresh.Username = c.Username
	}
	if err := Save(s.StateDir, fresh); err != nil {
		return "", fmt.Errorf("saving refreshed credentials: %w", err)
	}
	return fresh.AccessToken, nil
}
