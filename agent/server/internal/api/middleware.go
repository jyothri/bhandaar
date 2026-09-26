package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jyothri/bhandaar/agent/server/internal/auth"
	"github.com/jyothri/bhandaar/agent/server/internal/semver"
	"github.com/jyothri/bhandaar/agent/wire"
)

// DefaultMaxBody is the body limit for every endpoint except /changes.
const DefaultMaxBody = 16 << 10

// SupportedProtocols are the protocols this server speaks, in order.
var SupportedProtocols = []int{1}

type ctxKey int

const (
	ctxClientIP ctxKey = iota
	ctxPrincipal
)

// clientIP returns the request's client address (see withClientIP).
func clientIP(r *http.Request) netip.Addr {
	ip, _ := r.Context().Value(ctxClientIP).(netip.Addr)
	return ip
}

// principal returns who the access token was issued to (see requireAuth).
func principal(r *http.Request) auth.Principal {
	p, _ := r.Context().Value(ctxPrincipal).(auth.Principal)
	return p
}

// withClientIP works out the client address: the peer, or X-Real-IP when the
// peer is a trusted proxy (nginx sets it to $remote_addr). X-Forwarded-For is
// ignored, because its first entry can be spoofed.
func (s *Server) withClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := peerAddr(r.RemoteAddr)
		if s.trusted(ip) {
			if real, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
				ip = real.Unmap()
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxClientIP, ip)))
	})
}

func peerAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.IPv4Unspecified()
	}
	return ip.Unmap()
}

func (s *Server) trusted(ip netip.Addr) bool {
	for _, p := range s.cfg.TrustedProxies {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// logRequests logs one line per request. Headers and bodies are never logged:
// they carry passwords and tokens.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		slog.Log(r.Context(), level, "request",
			"method", r.Method, "path", r.URL.Path, "status", rec.status, "bytes", rec.bytes,
			"took", time.Since(start).Round(time.Millisecond), "client_ip", clientIP(r).String(),
			"agent_id", r.Header.Get(wire.HeaderAgentID), "agent_version", r.Header.Get(wire.HeaderAgentVersion))
	})
}

// limitBody caps the request body at n bytes.
func limitBody(n int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, n)
		next.ServeHTTP(w, r)
	})
}

// checkVersion answers 426 to agents below the minimum version.
func (s *Server) checkVersion(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get(wire.HeaderAgentVersion)
		details := map[string]any{
			"min_agent_version": s.cfg.MinAgentVersion.String(),
			"download_url":      s.cfg.AgentDownloadURL,
		}
		if h == "" {
			writeError(w, http.StatusUpgradeRequired, wire.CodeUpgradeRequired,
				wire.HeaderAgentVersion+" header is required", details)
			return
		}
		v, err := semver.Parse(h)
		if err != nil {
			writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest,
				wire.HeaderAgentVersion+": "+err.Error(), nil)
			return
		}
		if v.Less(s.cfg.MinAgentVersion) {
			writeError(w, http.StatusUpgradeRequired, wire.CodeUpgradeRequired,
				"driveagent "+v.String()+" is too old; upgrade to "+s.cfg.MinAgentVersion.String()+" or later", details)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkProtocol answers 426 to a protocol this server doesn't speak. A missing
// header means protocol 1.
func checkProtocol(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get(wire.HeaderAgentProtocol); h != "" {
			p, err := strconv.Atoi(h)
			if err != nil || !supportsProtocol(p) {
				writeError(w, http.StatusUpgradeRequired, wire.CodeUpgradeRequired,
					"unsupported protocol "+strconv.Quote(h), map[string]any{"protocols": SupportedProtocols})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func supportsProtocol(p int) bool {
	for _, q := range SupportedProtocols {
		if p == q {
			return true
		}
	}
	return false
}

// requireAuth checks the bearer access token, and that X-Agent-Id matches its
// agent.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			writeError(w, http.StatusUnauthorized, wire.CodeInvalidToken, "a bearer access token is required", nil)
			return
		}
		p, err := auth.VerifyAccessToken(s.cfg.JWTSecret, token, s.now())
		switch {
		case errors.Is(err, auth.ErrTokenExpired):
			writeError(w, http.StatusUnauthorized, wire.CodeTokenExpired, "access token expired", nil)
			return
		case err != nil:
			writeError(w, http.StatusUnauthorized, wire.CodeInvalidToken, "invalid access token", nil)
			return
		}
		if !strings.EqualFold(r.Header.Get(wire.HeaderAgentID), p.AgentID) {
			writeError(w, http.StatusForbidden, wire.CodeAgentMismatch,
				wire.HeaderAgentID+" does not match the access token's agent", nil)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxPrincipal, p)))
	})
}

// requireIdempotencyKey checks an Idempotency-Key is present. Storing and
// replaying responses is per endpoint.
func requireIdempotencyKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get(wire.HeaderIdempotencyKey)
		if k == "" || len(k) > wire.MaxIdempotencyKeyLen {
			writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest,
				wire.HeaderIdempotencyKey+" header is required (at most "+strconv.Itoa(wire.MaxIdempotencyKeyLen)+" characters)", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}
