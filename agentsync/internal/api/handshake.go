package api

import (
	"net/http"

	"github.com/jyothri/bhandaar/agentsync/internal/config"
	"github.com/jyothri/bhandaar/agentsync/internal/semver"
	"github.com/jyothri/bhandaar/agentsync/wire"
)

// Batch limits sent in the handshake.
const (
	MaxChangesPerBatch = 1000
	MaxBatchBytes      = 1 << 20
)

// handshake is POST /agent/v1/handshake. It runs before login, so an agent
// that needs an upgrade never sends its credentials.
func (s *Server) handshake(w http.ResponseWriter, r *http.Request) {
	var req wire.HandshakeRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	v, err := semver.Parse(req.AgentVersion)
	if err != nil {
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "agent_version: "+err.Error(), nil)
		return
	}
	if len(req.Protocols) == 0 {
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "protocols: at least one is required", nil)
		return
	}
	writeJSON(w, http.StatusOK, decide(s.cfg, v, req.Protocols))
}

// decide makes the handshake decision.
func decide(cfg config.Config, v semver.Version, agentProtocols []int) wire.HandshakeResponse {
	resp := wire.HandshakeResponse{
		MinAgentVersion:    cfg.MinAgentVersion.String(),
		LatestAgentVersion: cfg.LatestAgentVersion.String(),
		Limits:             wire.Limits{MaxChangesPerBatch: MaxChangesPerBatch, MaxBatchBytes: MaxBatchBytes},
	}
	// The highest protocol both sides speak.
	for _, p := range agentProtocols {
		if supportsProtocol(p) && p > resp.Protocol {
			resp.Protocol = p
		}
	}
	upgrade := func(decision, msg string) wire.HandshakeResponse {
		resp.Decision, resp.Message, resp.DownloadURL = decision, msg, cfg.AgentDownloadURL
		return resp
	}

	switch {
	case v.Less(cfg.MinAgentVersion):
		return upgrade(wire.DecisionUpgradeRequired,
			"driveagent "+v.String()+" is no longer supported; upgrade to "+cfg.LatestAgentVersion.String())
	case resp.Protocol == 0 && maxInt(agentProtocols) > maxInt(SupportedProtocols):
		resp.Decision = wire.DecisionUnsupportedProtocol
		resp.Message = "this driveagent is newer than the server, which doesn't speak any of its protocols; the server needs an upgrade"
		return resp
	case resp.Protocol == 0:
		return upgrade(wire.DecisionUpgradeRequired,
			"this driveagent's protocols are no longer supported; upgrade to "+cfg.LatestAgentVersion.String())
	case v.Less(cfg.LatestAgentVersion):
		return upgrade(wire.DecisionUpgradeRecommended,
			"driveagent "+cfg.LatestAgentVersion.String()+" is available (this is "+v.String()+")")
	}
	resp.Decision = wire.DecisionOK
	return resp
}

func maxInt(xs []int) int {
	m := 0
	for _, x := range xs {
		m = max(m, x)
	}
	return m
}
