// Package api is agentsync's HTTP API under /agent/.
package api

import (
	"context"
	"net/http"
	"time"

	"github.com/jyothri/bhandaar/agentsync/internal/auth"
	"github.com/jyothri/bhandaar/agentsync/internal/config"
	"github.com/jyothri/bhandaar/agentsync/internal/store"
	"github.com/jyothri/bhandaar/agentsync/wire"
)

// ServerVersion is agentsync's own version, reported by health.
const ServerVersion = "0.1.0"

// Pinger checks the database for health.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Server serves the API.
type Server struct {
	cfg    config.Config
	store  *store.Store
	tokens *auth.Tokens
	db     Pinger
	mux    *http.ServeMux
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// New builds the server. db defaults to st.
func New(cfg config.Config, st *store.Store, tokens *auth.Tokens, db Pinger) *Server {
	if db == nil && st != nil {
		db = st
	}
	s := &Server{cfg: cfg, store: st, tokens: tokens, db: db, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Handler is the server's root handler.
func (s *Server) Handler() http.Handler {
	return s.withClientIP(logRequests(s.mux))
}

// route says which checks an endpoint runs, besides the body limit.
type route struct {
	// anyVersion skips the X-Agent-Version and X-Agent-Protocol checks, so
	// agents of every version can reach the endpoint (health, handshake).
	anyVersion bool
	// authenticated requires a bearer access token.
	authenticated bool
	// idempotent requires an Idempotency-Key.
	idempotent bool
	// maxBody overrides DefaultMaxBody.
	maxBody int64
}

// handle registers h behind the middleware, applied in this order: body
// limit, X-Agent-Version, X-Agent-Protocol, bearer auth, Idempotency-Key.
func (s *Server) handle(pattern string, h http.HandlerFunc, o route) {
	var next http.Handler = h
	if o.idempotent {
		next = requireIdempotencyKey(next)
	}
	if o.authenticated {
		next = s.requireAuth(next)
	}
	if !o.anyVersion {
		next = checkProtocol(next)
		next = s.checkVersion(next)
	}
	limit := o.maxBody
	if limit == 0 {
		limit = DefaultMaxBody
	}
	s.mux.Handle(pattern, limitBody(limit, next))
}

func (s *Server) routes() {
	s.handle("GET /agent/health", s.health, route{anyVersion: true})
	s.handle("POST /agent/v1/handshake", s.handshake, route{anyVersion: true})
	s.handle("POST /agent/v1/auth/login", s.login, route{})
	s.handle("POST /agent/v1/auth/refresh", s.refresh, route{})
	s.handle("POST /agent/v1/auth/logout", s.logout, route{})
	s.mux.HandleFunc("/agent/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, wire.CodeNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path, nil)
	})
}
