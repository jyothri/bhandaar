package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/jyothri/bhandaar/agent/server/internal/auth"
	"github.com/jyothri/bhandaar/agent/server/internal/store"
	"github.com/jyothri/bhandaar/agent/wire"
)

// maxUsernameLen bounds usernames in login requests.
const maxUsernameLen = 128

func tokenResponse(p auth.Pair) wire.TokenResponse {
	return wire.TokenResponse{
		TokenType:        "Bearer",
		AccessToken:      p.AccessToken,
		AccessExpiresIn:  int64(p.AccessTTL.Seconds()),
		RefreshToken:     p.RefreshToken,
		RefreshExpiresIn: int64(p.RefreshTTL.Seconds()),
		User:             p.Username,
	}
}

// login is POST /agent/v1/auth/login.
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req wire.LoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	agentID, err := uuid.Parse(req.AgentID)
	switch {
	case req.Username == "" || len(req.Username) > maxUsernameLen || req.Password == "":
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "username and password are required", nil)
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "agent_id must be a UUID", nil)
		return
	case r.Header.Get(wire.HeaderAgentID) != "" && !strings.EqualFold(r.Header.Get(wire.HeaderAgentID), req.AgentID):
		writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "agent_id does not match "+wire.HeaderAgentID, nil)
		return
	}
	ctx, ip := r.Context(), clientIP(r)

	retry, err := s.store.LoginLockedFor(ctx, req.Username, ip)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	if retry > 0 {
		secs := int64(retry.Seconds() + 0.999)
		w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
		writeError(w, http.StatusTooManyRequests, wire.CodeTooManyAttempts,
			"too many failed logins; try again later", map[string]any{"retry_after_seconds": secs})
		return
	}

	// An unknown user costs the same time as a wrong password: it's checked
	// against a dummy hash. A disabled user's password is checked too.
	user, err := s.store.UserByName(ctx, req.Username)
	ok := false
	switch {
	case errors.Is(err, store.ErrNoSuchUser):
		auth.VerifyDummy(req.Password)
	case err != nil:
		writeInternal(w, r, err)
		return
	default:
		ok, err = auth.VerifyPassword(user.PasswordHash, req.Password)
		if err != nil {
			writeInternal(w, r, err)
			return
		}
		ok = ok && !user.Disabled()
	}
	if !ok {
		if err := s.store.RecordLoginFailure(ctx, req.Username, ip); err != nil {
			writeInternal(w, r, err)
			return
		}
		writeError(w, http.StatusUnauthorized, wire.CodeInvalidCredentials, "invalid username or password", nil)
		return
	}

	err = s.store.UpsertAgent(ctx, store.Agent{
		ID: agentID.String(), UserID: user.ID, Hostname: req.Hostname, OS: req.OS, Arch: req.Arch,
		Version: r.Header.Get(wire.HeaderAgentVersion),
	})
	if errors.Is(err, store.ErrAgentOwnedByAnother) {
		writeError(w, http.StatusForbidden, wire.CodeAgentOwnedByOtherUser,
			"this agent is registered to another user", nil)
		return
	}
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	if err := s.store.ClearLoginFailures(ctx, req.Username, ip); err != nil {
		writeInternal(w, r, err)
		return
	}
	pair, err := s.tokens.Login(ctx, user.ID, user.Username, agentID.String())
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse(pair))
}

// refresh is POST /agent/v1/auth/refresh.
func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var req wire.RefreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	pair, err := s.tokens.Refresh(r.Context(), req.RefreshToken,
		r.Header.Get(wire.HeaderAgentID), r.Header.Get(wire.HeaderAgentVersion))
	switch {
	case errors.Is(err, auth.ErrInvalidRefresh):
		writeError(w, http.StatusUnauthorized, wire.CodeInvalidRefreshToken,
			"refresh token is invalid, expired or revoked; log in again", nil)
	case errors.Is(err, auth.ErrRefreshReused):
		writeError(w, http.StatusUnauthorized, wire.CodeRefreshReused,
			"refresh token was already used; all tokens from this login are revoked; log in again", nil)
	case errors.Is(err, auth.ErrAgentMismatch):
		writeError(w, http.StatusForbidden, wire.CodeAgentMismatch,
			"refresh token belongs to another agent", nil)
	case err != nil:
		writeInternal(w, r, err)
	default:
		writeJSON(w, http.StatusOK, tokenResponse(pair))
	}
}

// logout is POST /agent/v1/auth/logout. It always answers 204.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	var req wire.RefreshRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.RefreshToken != "" {
		if err := s.tokens.Logout(r.Context(), req.RefreshToken); err != nil {
			writeInternal(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
