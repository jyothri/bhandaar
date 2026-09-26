package api

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jyothri/bhandaar/agentsync/wire"
)

// DefaultMaxBody is the body limit for every endpoint except /changes.
const DefaultMaxBody = 16 << 10

type ctxKey int

const ctxClientIP ctxKey = 0

// clientIP returns the request's client address (see withClientIP).
func clientIP(r *http.Request) netip.Addr {
	ip, _ := r.Context().Value(ctxClientIP).(netip.Addr)
	return ip
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
