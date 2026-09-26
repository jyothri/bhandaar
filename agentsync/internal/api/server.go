// Package api is agentsync's HTTP API under /agent/.
package api

import (
	"context"
	"net/http"
	"time"

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
	cfg   config.Config
	store *store.Store
	db    Pinger
	mux   *http.ServeMux
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// New builds the server. db defaults to st.
func New(cfg config.Config, st *store.Store, db Pinger) *Server {
	if db == nil && st != nil {
		db = st
	}
	s := &Server{cfg: cfg, store: st, db: db, mux: http.NewServeMux()}
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

// route configures an endpoint's middleware.
type route struct {
	// maxBody overrides DefaultMaxBody.
	maxBody int64
}

// handle registers h behind the body limit.
func (s *Server) handle(pattern string, h http.HandlerFunc, o route) {
	limit := o.maxBody
	if limit == 0 {
		limit = DefaultMaxBody
	}
	s.mux.Handle(pattern, limitBody(limit, h))
}

func (s *Server) routes() {
	s.handle("GET /agent/health", s.health, route{})
	s.mux.HandleFunc("/agent/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, wire.CodeNotFound, "no such endpoint: "+r.Method+" "+r.URL.Path, nil)
	})
}
