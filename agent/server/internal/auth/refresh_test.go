package auth_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/server/internal/auth"
	"github.com/jyothri/bhandaar/agent/server/internal/store"
	"github.com/jyothri/bhandaar/agent/server/internal/testdb"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fixture struct {
	st     *store.Store
	tokens *auth.Tokens
	clock  *clock
	userID int64
	agent  string
}

func setup(t *testing.T) fixture {
	t.Helper()
	st := testdb.New(t)
	ctx := context.Background()
	uid, err := st.CreateUser(ctx, "alice", "unused-hash")
	if err != nil {
		t.Fatal(err)
	}
	agent := uuid.NewString()
	if err := st.UpsertAgent(ctx, store.Agent{ID: agent, UserID: uid}); err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Now().UTC()}
	return fixture{
		st: st, clock: c, userID: uid, agent: agent,
		tokens: &auth.Tokens{Pool: st.Pool, Secret: []byte("0123456789abcdef0123456789abcdef"),
			AccessTTL: 15 * time.Minute, RefreshTTL: 720 * time.Hour, Now: c.Now},
	}
}

func (f fixture) login(t *testing.T) auth.Pair {
	t.Helper()
	p, err := f.tokens.Login(context.Background(), f.userID, "alice", f.agent)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f fixture) refresh(t *testing.T, token string) (auth.Pair, error) {
	t.Helper()
	return f.tokens.Refresh(context.Background(), token, f.agent, "0.1.0")
}

func mustRefresh(t *testing.T, f fixture, token string) auth.Pair {
	t.Helper()
	p, err := f.refresh(t, token)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return p
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestRotation(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	if p0.Username != "alice" || p0.AccessToken == "" || p0.RefreshToken == "" {
		t.Fatalf("login pair = %+v", p0)
	}
	p1 := mustRefresh(t, f, p0.RefreshToken)
	if p1.RefreshToken == p0.RefreshToken {
		t.Fatal("refresh returned the same token")
	}
	f.clock.Add(time.Hour)
	p2 := mustRefresh(t, f, p1.RefreshToken)
	mustRefresh(t, f, p2.RefreshToken)
}

func TestGraceReplayGetsFreshPairAndRevokesSuccessor(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	p1 := mustRefresh(t, f, p0.RefreshToken) // the response the agent "lost"
	f.clock.Add(10 * time.Second)
	p2 := mustRefresh(t, f, p0.RefreshToken) // replay within the window
	if p2.RefreshToken == p1.RefreshToken {
		t.Fatal("grace replay returned the orphaned successor")
	}
	// The new pair works on.
	p3 := mustRefresh(t, f, p2.RefreshToken)
	mustRefresh(t, f, p3.RefreshToken)
}

func TestSecondGraceReplayRevokesFamily(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	mustRefresh(t, f, p0.RefreshToken)
	p2 := mustRefresh(t, f, p0.RefreshToken)
	_, err := f.refresh(t, p0.RefreshToken)
	wantErr(t, err, auth.ErrRefreshReused)
	_, err = f.refresh(t, p2.RefreshToken)
	wantErr(t, err, auth.ErrInvalidRefresh)
}

func TestReplayAfterWindowRevokesFamily(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	p1 := mustRefresh(t, f, p0.RefreshToken)
	f.clock.Add(auth.RefreshGrace + time.Second)
	_, err := f.refresh(t, p0.RefreshToken)
	wantErr(t, err, auth.ErrRefreshReused)
	_, err = f.refresh(t, p1.RefreshToken)
	wantErr(t, err, auth.ErrInvalidRefresh)
}

func TestReplayAfterSuccessorUsedIsReuse(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	p1 := mustRefresh(t, f, p0.RefreshToken)
	p2 := mustRefresh(t, f, p1.RefreshToken) // the agent did receive p1
	_, err := f.refresh(t, p0.RefreshToken)  // still within 30 s of p0's rotation
	wantErr(t, err, auth.ErrRefreshReused)
	_, err = f.refresh(t, p2.RefreshToken)
	wantErr(t, err, auth.ErrInvalidRefresh)
}

// A thief replays a stolen token within the window and gets a fresh pair;
// the owner's next refresh presents the grace-revoked successor, which
// revokes the thief's pair too.
func TestGraceRevokedSuccessorRevokesFamily(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	owner := mustRefresh(t, f, p0.RefreshToken)
	thief := mustRefresh(t, f, p0.RefreshToken)
	_, err := f.refresh(t, owner.RefreshToken)
	wantErr(t, err, auth.ErrRefreshReused)
	_, err = f.refresh(t, thief.RefreshToken)
	wantErr(t, err, auth.ErrInvalidRefresh)
}

func TestConcurrentRefreshesAreSerialised(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	var (
		wg    sync.WaitGroup
		pairs [2]auth.Pair
		errs  [2]error
	)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pairs[i], errs[i] = f.tokens.Refresh(context.Background(), p0.RefreshToken, f.agent, "")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
	}
	if pairs[0].RefreshToken == pairs[1].RefreshToken {
		t.Fatal("both refreshes returned the same token")
	}
	// The second one in lock order took the grace path: it revoked the first
	// one's successor. So the family has exactly one live token left.
	var live int
	if err := f.st.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_refresh_tokens WHERE revoked_at IS NULL AND rotated_at IS NULL`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("%d live refresh tokens, want 1", live)
	}
}

func TestExpiredUnknownAndMismatchedTokens(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)

	_, err := f.refresh(t, "rt_not-a-real-token")
	wantErr(t, err, auth.ErrInvalidRefresh)
	_, err = f.refresh(t, "no prefix")
	wantErr(t, err, auth.ErrInvalidRefresh)

	_, err = f.tokens.Refresh(context.Background(), p0.RefreshToken, uuid.NewString(), "")
	wantErr(t, err, auth.ErrAgentMismatch)

	f.clock.Add(721 * time.Hour)
	_, err = f.refresh(t, p0.RefreshToken)
	wantErr(t, err, auth.ErrInvalidRefresh)
}

func TestLogoutRevokesFamily(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	p1 := mustRefresh(t, f, p0.RefreshToken)
	if err := f.tokens.Logout(context.Background(), p0.RefreshToken); err != nil {
		t.Fatal(err)
	}
	_, err := f.refresh(t, p1.RefreshToken)
	wantErr(t, err, auth.ErrInvalidRefresh)
	if err := f.tokens.Logout(context.Background(), "rt_unknown"); err != nil {
		t.Fatalf("logout of an unknown token: %v", err)
	}
}

func TestDisabledUserCannotRefresh(t *testing.T) {
	f := setup(t)
	p0 := f.login(t)
	if n, err := f.st.DisableUser(context.Background(), "alice"); err != nil || n != 1 {
		t.Fatalf("disable: %d, %v", n, err)
	}
	_, err := f.refresh(t, p0.RefreshToken)
	wantErr(t, err, auth.ErrInvalidRefresh)
}
