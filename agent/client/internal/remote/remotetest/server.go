// Package remotetest is a fake agentserver for tests: health, handshake,
// login, refresh and logout, recording every request.
package remotetest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/jyothri/bhandaar/agent/wire"
)

// Request is a recorded request.
type Request struct {
	Method, Path string
	Header       http.Header
	Body         []byte
}

// Server is the fake. Change its fields under Lock/Unlock.
type Server struct {
	*httptest.Server
	sync.Mutex

	// Decision is the handshake decision (default ok).
	Decision string
	// Users maps username to password.
	Users map[string]string
	// AccessTTL is access_expires_in, in seconds (default 900).
	AccessTTL int64
	// Down makes health answer 503.
	Down bool

	Requests  []Request
	Refreshes int
	n         int
	refresh   map[string]string // live refresh token -> username
}

// New starts a plain-http fake on 127.0.0.1, closed when the test ends.
func New(t testing.TB) *Server {
	s := &Server{Users: map[string]string{"jyothri": "correct horse battery"}, refresh: map[string]string{}}
	s.Server = httptest.NewServer(s)
	t.Cleanup(s.Close)
	return s
}

// Paths returns the recorded request paths, in order.
func (s *Server) Paths() []string {
	s.Lock()
	defer s.Unlock()
	var out []string
	for _, r := range s.Requests {
		out = append(out, r.Path)
	}
	return out
}

// Last returns the last request to path.
func (s *Server) Last(path string) (Request, bool) {
	s.Lock()
	defer s.Unlock()
	for i := len(s.Requests) - 1; i >= 0; i-- {
		if s.Requests[i].Path == path {
			return s.Requests[i], true
		}
	}
	return Request{}, false
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.Lock()
	defer s.Unlock()
	s.Requests = append(s.Requests, Request{Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body})

	switch r.Method + " " + r.URL.Path {
	case "GET /agent/health":
		if s.Down {
			writeJSON(w, 503, wire.HealthResponse{Status: wire.HealthUnavailable, Service: "agentserver", Reason: "database"})
			return
		}
		writeJSON(w, 200, wire.HealthResponse{Status: wire.HealthOK, Service: "agentserver", ServerVersion: "0.1.0", APIVersions: []string{"v1"}})
	case "POST /agent/v1/handshake":
		d := s.Decision
		if d == "" {
			d = wire.DecisionOK
		}
		resp := wire.HandshakeResponse{Decision: d, Protocol: 1, MinAgentVersion: "0.1.0", LatestAgentVersion: "0.1.0",
			Limits: wire.Limits{MaxChangesPerBatch: 1000, MaxBatchBytes: 1 << 20}}
		if d != wire.DecisionOK {
			resp.Message, resp.DownloadURL = "please upgrade", "https://example.com/releases"
		}
		if d == wire.DecisionUnsupportedProtocol {
			resp.Protocol = 0
		}
		writeJSON(w, 200, resp)
	case "POST /agent/v1/auth/login":
		var req wire.LoginRequest
		json.Unmarshal(body, &req)
		if pw, ok := s.Users[req.Username]; !ok || pw != req.Password {
			writeError(w, 401, wire.CodeInvalidCredentials)
			return
		}
		writeJSON(w, 200, s.issue(req.Username))
	case "POST /agent/v1/auth/refresh":
		var req wire.RefreshRequest
		json.Unmarshal(body, &req)
		user, ok := s.refresh[req.RefreshToken]
		if !ok {
			writeError(w, 401, wire.CodeInvalidRefreshToken)
			return
		}
		delete(s.refresh, req.RefreshToken)
		s.Refreshes++
		writeJSON(w, 200, s.issue(user))
	case "POST /agent/v1/auth/logout":
		var req wire.RefreshRequest
		json.Unmarshal(body, &req)
		delete(s.refresh, req.RefreshToken)
		w.WriteHeader(204)
	default:
		writeError(w, 404, wire.CodeNotFound)
	}
}

func (s *Server) issue(user string) wire.TokenResponse {
	s.n++
	rt := "rt_" + strconv.Itoa(s.n)
	s.refresh[rt] = user
	ttl := s.AccessTTL
	if ttl == 0 {
		ttl = 900
	}
	return wire.TokenResponse{TokenType: "Bearer", AccessToken: "at_" + strconv.Itoa(s.n), AccessExpiresIn: ttl,
		RefreshToken: rt, RefreshExpiresIn: 2592000, User: user}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, wire.ErrorResponse{Error: wire.ErrorDetail{Code: code, Message: code}})
}
