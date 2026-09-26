package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agentsync/internal/config"
	"github.com/jyothri/bhandaar/agentsync/wire"
)

func testConfig(t *testing.T, env map[string]string) config.Config {
	t.Helper()
	all := map[string]string{"AGENTSYNC_JWT_SECRET": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))}
	for k, v := range env {
		all[k] = v
	}
	cfg, err := config.Load(func(k string) string { return all[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

// client sends agent requests to a test server.
type client struct {
	t       *testing.T
	srv     *Server
	agentID string
	version string
	remote  string // RemoteAddr
	headers map[string]string
}

func newClient(t *testing.T, srv *Server) *client {
	return &client{t: t, srv: srv, agentID: uuid.NewString(), version: "0.1.0", remote: "203.0.113.9:40000"}
}

func (c *client) do(method, path string, body any, extra ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		j, err := json.Marshal(b)
		if err != nil {
			c.t.Fatal(err)
		}
		r = bytes.NewReader(j)
	}
	req := httptest.NewRequest(method, path, r)
	req.RemoteAddr = c.remote
	if c.version != "" {
		req.Header.Set(wire.HeaderAgentVersion, c.version)
	}
	if c.agentID != "" {
		req.Header.Set(wire.HeaderAgentID, c.agentID)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
	rec := httptest.NewRecorder()
	c.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var e wire.ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("not an error body (%d): %s", rec.Code, rec.Body)
	}
	return e.Error.Code
}

func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if code != "" {
		if got := errCode(t, rec); got != code {
			t.Fatalf("code = %s, want %s", got, code)
		}
	}
}

func TestHealth(t *testing.T) {
	cfg := testConfig(t, nil)
	for _, c := range []struct {
		db     error
		status int
		want   string
	}{{nil, 200, wire.HealthOK}, {errors.New("down"), 503, wire.HealthUnavailable}} {
		srv := New(cfg, nil, fakeDB{c.db})
		cl := newClient(t, srv)
		cl.version = "" // health needs no headers at all
		cl.agentID = ""
		rec := cl.do("GET", "/agent/health", nil)
		if rec.Code != c.status {
			t.Fatalf("status = %d, want %d", rec.Code, c.status)
		}
		var h wire.HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &h); err != nil {
			t.Fatal(err)
		}
		if h.Status != c.want || h.Service != "agentsync" || len(h.APIVersions) != 1 || h.APIVersions[0] != "v1" {
			t.Errorf("health = %+v", h)
		}
		if c.db == nil && h.ServerVersion != ServerVersion {
			t.Errorf("server_version = %q", h.ServerVersion)
		}
		if c.db != nil && h.Reason != "database" {
			t.Errorf("reason = %q", h.Reason)
		}
	}
}

func TestUnknownPath(t *testing.T) {
	srv := New(testConfig(t, nil), nil, fakeDB{})
	wantStatus(t, newClient(t, srv).do("GET", "/agent/v1/nope", nil), 404, wire.CodeNotFound)
}

func TestClientIP(t *testing.T) {
	srv := New(testConfig(t, map[string]string{"AGENTSYNC_TRUSTED_PROXIES": "192.168.1.118"}), nil, fakeDB{})
	var got string
	h := srv.withClientIP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = clientIP(r).String() }))
	for _, c := range []struct{ remote, realIP, want string }{
		{"192.168.1.118:5000", "198.51.100.7", "198.51.100.7"}, // from nginx: trusted
		{"203.0.113.9:5000", "198.51.100.7", "203.0.113.9"},    // not nginx: ignored
		{"192.168.1.118:5000", "", "192.168.1.118"},
		{"192.168.1.118:5000", "garbage", "192.168.1.118"},
		{"[::ffff:192.168.1.118]:5000", "198.51.100.7", "198.51.100.7"},
	} {
		req := httptest.NewRequest("GET", "/agent/health", nil)
		req.RemoteAddr = c.remote
		req.Header.Set("X-Forwarded-For", "10.9.9.9")
		if c.realIP != "" {
			req.Header.Set("X-Real-IP", c.realIP)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got != c.want {
			t.Errorf("remote %s, X-Real-IP %q: client ip %s, want %s", c.remote, c.realIP, got, c.want)
		}
	}
}
