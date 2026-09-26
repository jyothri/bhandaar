package api

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jyothri/bhandaar/agentsync/wire"
)

// APIVersions are the versioned API paths served.
var APIVersions = []string{"v1"}

const healthPingTimeout = time.Second

// health is GET /agent/health: a database ping, and nothing else.
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	resp := wire.HealthResponse{
		Service:     "agentsync",
		APIVersions: APIVersions,
		Time:        s.now().UTC().Truncate(time.Second),
	}
	ctx, cancel := context.WithTimeout(r.Context(), healthPingTimeout)
	defer cancel()
	if err := s.db.Ping(ctx); err != nil {
		slog.Warn("health: database unavailable", "error", err)
		resp.Status = wire.HealthUnavailable
		resp.Reason = "database"
		writeJSON(w, http.StatusServiceUnavailable, resp)
		return
	}
	resp.Status = wire.HealthOK
	resp.ServerVersion = ServerVersion
	writeJSON(w, http.StatusOK, resp)
}
