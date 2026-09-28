package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/auth"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
)

// Web users are agentserver's users (see be/auth). Logging in sets an
// HttpOnly session cookie; every other /api and /sse route needs it.

const sessionCookie = "bhandaar_session"

// maxUsernameLen bounds usernames in login requests, as agentserver does.
const maxUsernameLen = 128

// publicPaths are the routes that work without a session.
var publicPaths = map[string]bool{
	"/api/health":      true,
	"/api/auth/login":  true,
	"/api/auth/logout": true,
}

type ctxKey int

const ctxUser ctxKey = iota

// currentUser returns the logged-in user; authenticate puts it in the
// request's context.
func currentUser(r *http.Request) db.User {
	u, _ := r.Context().Value(ctxUser).(db.User)
	return u
}

func withUser(r *http.Request, u db.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxUser, u))
}

// sessionUser looks up a session token's user; tests replace it.
var sessionUser = db.SessionUser

func authRoutes(r *mux.Router) {
	r.HandleFunc("/api/auth/login", LoginHandler).Methods("POST")
	r.HandleFunc("/api/auth/logout", LogoutHandler).Methods("POST")
	r.HandleFunc("/api/auth/me", MeHandler).Methods("GET")
}

// authenticate lets a request through only with a live session, except on
// publicPaths. It also refuses requests that change state from an origin
// other than -frontend_url's: the session cookie is SameSite=Lax, which
// still lets sibling subdomains send it.
func authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) && !trustedOrigin(r.Header.Get("Origin")) {
			writeError(w, http.StatusForbidden, "FORBIDDEN_ORIGIN", "request origin is not allowed", nil)
			return
		}
		if publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "log in first", nil)
			return
		}
		user, err := sessionUser(cookie.Value)
		if errors.Is(err, db.ErrNoSuchUser) {
			writeError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "session expired; log in again", nil)
			return
		}
		if err != nil {
			slog.Error("Failed to check session", "error", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL", "failed to check session", nil)
			return
		}
		next.ServeHTTP(w, withUser(r, user))
	})
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// trustedOrigin allows a missing Origin (not a browser) and -frontend_url's.
func trustedOrigin(origin string) bool {
	if origin == "" {
		return true
	}
	for _, o := range constants.FrontendOrigins() {
		if strings.EqualFold(origin, o) {
			return true
		}
	}
	return false
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	Username string `json:"username"`
}

// LoginHandler is POST /api/auth/login. It answers every bad username or
// password the same way, in the same time, and shares agentserver's lockout.
func LoginHandler(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if handleMaxBytesError(w, r, err, DefaultMaxBodySize) {
		return
	}
	if err != nil || req.Username == "" || len(req.Username) > maxUsernameLen || req.Password == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "username and password are required", nil)
		return
	}
	ip := clientIP(r)

	retry, err := db.LoginLockedFor(req.Username, ip)
	if err != nil {
		slog.Error("Login failed", "error", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "login failed", nil)
		return
	}
	if retry > 0 {
		secs := int64(retry.Seconds() + 0.999)
		w.Header().Set("Retry-After", strconv.FormatInt(secs, 10))
		writeError(w, http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS",
			"too many failed logins; try again later", map[string]interface{}{"retry_after_seconds": secs})
		return
	}

	user, err := db.UserByName(req.Username)
	ok := false
	switch {
	case errors.Is(err, db.ErrNoSuchUser):
		auth.VerifyDummy(req.Password)
	case err != nil:
		slog.Error("Login failed", "error", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "login failed", nil)
		return
	default:
		ok, err = auth.VerifyPassword(user.PasswordHash, req.Password)
		if err != nil {
			slog.Error("Login failed: bad password hash", "user", user.Username, "error", err)
			writeError(w, http.StatusInternalServerError, "INTERNAL", "login failed", nil)
			return
		}
		ok = ok && !user.Disabled()
	}
	if !ok {
		if err := db.RecordLoginFailure(req.Username, ip); err != nil {
			slog.Error("Failed to record login failure", "error", err)
		}
		slog.Warn("Failed web login", "username", req.Username, "client_ip", ip)
		writeError(w, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password", nil)
		return
	}
	if err := db.ClearLoginFailures(req.Username, ip); err != nil {
		slog.Error("Failed to clear login failures", "error", err)
	}

	token, err := newSessionToken()
	if err == nil {
		err = db.CreateSession(user.ID, token)
	}
	if err != nil {
		slog.Error("Login failed", "error", err)
		writeError(w, http.StatusInternalServerError, "INTERNAL", "login failed", nil)
		return
	}
	slog.Info("Web login", "username", user.Username, "client_ip", ip)
	setSessionCookie(w, token, db.SessionTTL)
	writeJSONResponse(w, userResponse{Username: user.Username}, http.StatusOK)
}

// LogoutHandler is POST /api/auth/logout. It always answers 204.
func LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		if err := db.DeleteSession(cookie.Value); err != nil {
			slog.Error("Failed to delete session", "error", err)
		}
	}
	setSessionCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// MeHandler is GET /api/auth/me: who is logged in. The UI calls it on load,
// which also renews the cookie to match the session's sliding expiry.
func MeHandler(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		setSessionCookie(w, cookie.Value, db.SessionTTL)
	}
	writeJSONResponse(w, userResponse{Username: currentUser(r).Username}, http.StatusOK)
}

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// setSessionCookie sets the session cookie, or deletes it when maxAge < 0.
// It's Secure whenever the UI is served over HTTPS; a plain-HTTP local UI
// (http://localhost:5173) needs it without.
func setSessionCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	seconds := int(maxAge.Seconds())
	if maxAge < 0 {
		seconds = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   seconds,
		HttpOnly: true,
		Secure:   secureCookies(),
		SameSite: http.SameSiteLaxMode,
	})
}

func secureCookies() bool {
	for _, o := range constants.FrontendOrigins() {
		if strings.HasPrefix(strings.ToLower(o), "https://") {
			return true
		}
	}
	return false
}

// clientIP is the peer's address, or X-Real-IP when the peer is a proxy on
// a loopback or private address (nginx sets it to $remote_addr).
// X-Forwarded-For is ignored, because its first entry can be spoofed.
func clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, _ := netip.ParseAddr(host)
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsPrivate() {
		if real, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("X-Real-IP"))); err == nil {
			return real.Unmap()
		}
	}
	return ip
}

// writeError writes be's JSON error shape.
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]interface{}) {
	writeErrorResponse(w, ErrorResponse{Error: ErrorDetail{
		Code:      code,
		Message:   message,
		Details:   details,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}}, status)
}
