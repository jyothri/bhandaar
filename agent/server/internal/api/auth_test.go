package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/server/internal/auth"
	"github.com/jyothri/bhandaar/agent/server/internal/config"
	"github.com/jyothri/bhandaar/agent/server/internal/store"
	"github.com/jyothri/bhandaar/agent/server/internal/testdb"
	"github.com/jyothri/bhandaar/agent/wire"
)

func TestMain(m *testing.M) {
	auth.DefaultParams = auth.Params{Memory: 64, Time: 1, Threads: 1}
	os.Exit(m.Run())
}

func TestVersionAndProtocolChecks(t *testing.T) {
	srv := New(testConfig(t, map[string]string{"AGENTSERVER_MIN_AGENT_VERSION": "0.2.0", "AGENTSERVER_LATEST_AGENT_VERSION": "0.2.0"}), nil, nil, fakeDB{})
	body := wire.RefreshRequest{RefreshToken: "rt_x"}
	for _, path := range []string{"/agent/v1/auth/login", "/agent/v1/auth/refresh", "/agent/v1/auth/logout"} {
		cl := newClient(t, srv)
		cl.version = "0.1.9"
		wantStatus(t, cl.do("POST", path, body), 426, wire.CodeUpgradeRequired)
		cl.version = ""
		wantStatus(t, cl.do("POST", path, body), 426, wire.CodeUpgradeRequired)
		cl.version = "latest"
		wantStatus(t, cl.do("POST", path, body), 400, wire.CodeInvalidRequest)
		cl.version = "0.2.0"
		wantStatus(t, cl.do("POST", path, body, wire.HeaderAgentProtocol, "2"), 426, wire.CodeUpgradeRequired)
		wantStatus(t, cl.do("POST", path, body, wire.HeaderAgentProtocol, "one"), 426, wire.CodeUpgradeRequired)
	}
	// Health stays reachable to any version.
	cl := newClient(t, srv)
	cl.version = "0.0.1"
	wantStatus(t, cl.do("GET", "/agent/health", nil, wire.HeaderAgentProtocol, "9"), 200, "")
}

// protectedServer adds a test-only endpoint behind the auth and
// idempotency middleware.
func protectedServer(t *testing.T) (*Server, config.Config) {
	cfg := testConfig(t, nil)
	srv := New(cfg, nil, nil, fakeDB{})
	srv.handle("POST /agent/v1/test", func(w http.ResponseWriter, r *http.Request) {
		p := principal(r)
		writeJSON(w, 200, map[string]any{"user": p.UserID, "agent": p.AgentID})
	}, route{authenticated: true, idempotent: true})
	return srv, cfg
}

func TestBearerAuth(t *testing.T) {
	srv, cfg := protectedServer(t)
	now := time.Now()
	srv.Now = func() time.Time { return now }
	cl := newClient(t, srv)
	tok, err := auth.IssueAccessToken(cfg.JWTSecret, auth.Principal{UserID: 5, AgentID: cl.agentID}, now, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	key := []string{wire.HeaderIdempotencyKey, "k1"}
	with := func(h ...string) []string { return append(append([]string{}, key...), h...) }

	wantStatus(t, cl.do("POST", "/agent/v1/test", "{}", key...), 401, wire.CodeInvalidToken)
	wantStatus(t, cl.do("POST", "/agent/v1/test", "{}", with("Authorization", "Basic abc")...), 401, wire.CodeInvalidToken)
	wantStatus(t, cl.do("POST", "/agent/v1/test", "{}", with("Authorization", "Bearer "+tok+"x")...), 401, wire.CodeInvalidToken)
	rec := cl.do("POST", "/agent/v1/test", "{}", with("Authorization", "Bearer "+tok)...)
	wantStatus(t, rec, 200, "")
	if !strings.Contains(rec.Body.String(), `"user":5`) {
		t.Errorf("body = %s", rec.Body)
	}

	// X-Agent-Id must match the token's agent.
	other := newClient(t, srv)
	wantStatus(t, other.do("POST", "/agent/v1/test", "{}", with("Authorization", "Bearer "+tok)...), 403, wire.CodeAgentMismatch)

	// Idempotency-Key is required, and bounded.
	wantStatus(t, cl.do("POST", "/agent/v1/test", "{}", "Authorization", "Bearer "+tok), 400, wire.CodeInvalidRequest)
	wantStatus(t, cl.do("POST", "/agent/v1/test", "{}", "Authorization", "Bearer "+tok,
		wire.HeaderIdempotencyKey, strings.Repeat("k", wire.MaxIdempotencyKeyLen+1)), 400, wire.CodeInvalidRequest)

	now = now.Add(16 * time.Minute)
	wantStatus(t, cl.do("POST", "/agent/v1/test", "{}", with("Authorization", "Bearer "+tok)...), 401, wire.CodeTokenExpired)
}

// dbServer is a server on a real test database, with one user.
func dbServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st := testdb.New(t)
	cfg := testConfig(t, nil)
	tokens := &auth.Tokens{Pool: st.Pool, Secret: cfg.JWTSecret, AccessTTL: cfg.AccessTTL, RefreshTTL: cfg.RefreshTTL}
	hash, err := auth.HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(context.Background(), "jyothri", hash); err != nil {
		t.Fatal(err)
	}
	return New(cfg, st, tokens, nil), st
}

func (c *client) login(user, password string) *httptest.ResponseRecorder {
	return c.do("POST", "/agent/v1/auth/login", wire.LoginRequest{
		Username: user, Password: password, AgentID: c.agentID, Hostname: "optiplex7070", OS: "linux", Arch: "amd64"})
}

func decodeTokens(t *testing.T, rec *httptest.ResponseRecorder) wire.TokenResponse {
	t.Helper()
	var tr wire.TokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestLoginRefreshLogout(t *testing.T) {
	srv, st := dbServer(t)
	cl := newClient(t, srv)
	rec := cl.login("jyothri", "correct horse battery")
	wantStatus(t, rec, 200, "")
	tr := decodeTokens(t, rec)
	if tr.TokenType != "Bearer" || tr.User != "jyothri" || tr.AccessExpiresIn != 900 || tr.RefreshExpiresIn != 2592000 ||
		!strings.HasPrefix(tr.RefreshToken, "rt_") {
		t.Fatalf("tokens = %+v", tr)
	}
	p, err := auth.VerifyAccessToken(srv.cfg.JWTSecret, tr.AccessToken, time.Now())
	if err != nil || p.AgentID != cl.agentID {
		t.Fatalf("access token: %+v, %v", p, err)
	}

	var host, osName, version string
	if err := st.Pool.QueryRow(context.Background(),
		"SELECT hostname, os, last_version FROM agent_agents WHERE id = $1", cl.agentID).Scan(&host, &osName, &version); err != nil {
		t.Fatal(err)
	}
	if host != "optiplex7070" || osName != "linux" || version != "0.1.0" {
		t.Errorf("agent row = %s %s %s", host, osName, version)
	}

	rec = cl.do("POST", "/agent/v1/auth/refresh", wire.RefreshRequest{RefreshToken: tr.RefreshToken})
	wantStatus(t, rec, 200, "")
	tr2 := decodeTokens(t, rec)
	if tr2.RefreshToken == tr.RefreshToken || tr2.User != "jyothri" {
		t.Fatalf("refresh = %+v", tr2)
	}

	// Another agent can't use this agent's refresh token.
	wantStatus(t, newClient(t, srv).do("POST", "/agent/v1/auth/refresh", wire.RefreshRequest{RefreshToken: tr2.RefreshToken}),
		403, wire.CodeAgentMismatch)

	// A replay after the grace window revokes the family.
	if _, err := st.Pool.Exec(context.Background(),
		"UPDATE agent_refresh_tokens SET rotated_at = rotated_at - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, cl.do("POST", "/agent/v1/auth/refresh", wire.RefreshRequest{RefreshToken: tr.RefreshToken}), 401, wire.CodeRefreshReused)
	wantStatus(t, cl.do("POST", "/agent/v1/auth/refresh", wire.RefreshRequest{RefreshToken: tr2.RefreshToken}), 401, wire.CodeInvalidRefreshToken)

	// Logout always answers 204, and revokes the family.
	tr3 := decodeTokens(t, cl.login("jyothri", "correct horse battery"))
	wantStatus(t, cl.do("POST", "/agent/v1/auth/logout", wire.RefreshRequest{RefreshToken: tr3.RefreshToken}), 204, "")
	wantStatus(t, cl.do("POST", "/agent/v1/auth/logout", wire.RefreshRequest{RefreshToken: "rt_unknown"}), 204, "")
	wantStatus(t, cl.do("POST", "/agent/v1/auth/refresh", wire.RefreshRequest{RefreshToken: tr3.RefreshToken}), 401, wire.CodeInvalidRefreshToken)
}

func TestLoginFailures(t *testing.T) {
	srv, st := dbServer(t)
	cl := newClient(t, srv)
	wantStatus(t, cl.login("jyothri", "wrong password!!"), 401, wire.CodeInvalidCredentials)
	wrong := cl.login("jyothri", "wrong password!!").Body.String()
	unknown := cl.login("nobody", "wrong password!!").Body.String()
	strip := func(s string) string { return s[:strings.Index(s, `"timestamp"`)] }
	if strip(wrong) != strip(unknown) {
		t.Errorf("unknown user and wrong password answer differently:\n%s\n%s", wrong, unknown)
	}

	if _, err := st.DisableUser(context.Background(), "jyothri"); err != nil {
		t.Fatal(err)
	}
	wantStatus(t, cl.login("jyothri", "correct horse battery"), 401, wire.CodeInvalidCredentials)

	// Bad requests.
	wantStatus(t, cl.do("POST", "/agent/v1/auth/login", wire.LoginRequest{Username: "jyothri", Password: "x", AgentID: "not-a-uuid"}),
		400, wire.CodeInvalidRequest)
	wantStatus(t, cl.do("POST", "/agent/v1/auth/login", wire.LoginRequest{Username: "jyothri", AgentID: cl.agentID}),
		400, wire.CodeInvalidRequest)
	wantStatus(t, cl.do("POST", "/agent/v1/auth/login", wire.LoginRequest{Username: "jyothri", Password: "x", AgentID: uuid.NewString()}),
		400, wire.CodeInvalidRequest) // doesn't match X-Agent-Id
}

func TestAgentOwnedByOtherUser(t *testing.T) {
	srv, st := dbServer(t)
	hash, _ := auth.HashPassword("another good password")
	if _, err := st.CreateUser(context.Background(), "other", hash); err != nil {
		t.Fatal(err)
	}
	cl := newClient(t, srv)
	wantStatus(t, cl.login("jyothri", "correct horse battery"), 200, "")
	wantStatus(t, cl.login("other", "another good password"), 403, wire.CodeAgentOwnedByOtherUser)
}

func TestLoginLockoutPerUsernameAndIP(t *testing.T) {
	srv, _ := dbServer(t)
	srv.cfg.TrustedProxies = testConfig(t, map[string]string{"AGENTSERVER_TRUSTED_PROXIES": "127.0.0.1"}).TrustedProxies
	attacker := newClient(t, srv)
	attacker.remote = "127.0.0.1:1234"
	attacker.headers = map[string]string{"X-Real-IP": "198.51.100.66"}
	for i := 0; i < 10; i++ {
		wantStatus(t, attacker.login("jyothri", "guess number "+string(rune('a'+i))), 401, wire.CodeInvalidCredentials)
	}
	rec := attacker.login("jyothri", "correct horse battery")
	wantStatus(t, rec, 429, wire.CodeTooManyAttempts)
	if ra := rec.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q", ra)
	}

	// The owner, from another IP, isn't locked out.
	owner := newClient(t, srv)
	owner.remote = "127.0.0.1:1234"
	owner.headers = map[string]string{"X-Real-IP": "198.51.100.1"}
	wantStatus(t, owner.login("jyothri", "correct horse battery"), 200, "")
	// Nor is another username from the attacker's IP.
	wantStatus(t, attacker.login("someone-else", "x"), 401, wire.CodeInvalidCredentials)
}

func TestLoginSuccessClearsFailures(t *testing.T) {
	srv, _ := dbServer(t)
	cl := newClient(t, srv)
	for i := 0; i < 9; i++ {
		wantStatus(t, cl.login("jyothri", "wrong"), 401, wire.CodeInvalidCredentials)
	}
	wantStatus(t, cl.login("jyothri", "correct horse battery"), 200, "")
	for i := 0; i < 9; i++ {
		wantStatus(t, cl.login("jyothri", "wrong"), 401, wire.CodeInvalidCredentials)
	}
	wantStatus(t, cl.login("jyothri", "correct horse battery"), 200, "")
}
