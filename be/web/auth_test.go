package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
)

// fakeSessions makes sessionUser accept only token "good", as user alice.
func fakeSessions(t *testing.T) {
	t.Helper()
	original := sessionUser
	sessionUser = func(token string) (db.User, error) {
		switch token {
		case "good":
			return db.User{ID: 7, Username: "alice"}, nil
		case "broken":
			return db.User{}, errors.New("database down")
		}
		return db.User{}, db.ErrNoSuchUser
	}
	t.Cleanup(func() { sessionUser = original })
}

// authRouter serves a route that echoes the logged-in user, behind
// authenticate, plus a public route.
func authRouter() http.Handler {
	r := mux.NewRouter()
	r.Use(authenticate)
	r.HandleFunc("/api/whoami", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(currentUser(r).Username))
	}).Methods("GET", "POST")
	r.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	return r
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func withSession(req *http.Request, token string) *http.Request {
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	return req
}

func TestAuthenticateNeedsASession(t *testing.T) {
	fakeSessions(t)
	h := authRouter()

	cases := map[string]struct {
		token string
		want  int
	}{
		"no cookie":      {"", http.StatusUnauthorized},
		"unknown token":  {"expired", http.StatusUnauthorized},
		"database error": {"broken", http.StatusInternalServerError},
		"live session":   {"good", http.StatusOK},
	}
	for name, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
		if c.token != "" {
			withSession(req, c.token)
		}
		rec := serve(h, req)
		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, c.want)
		}
		if c.want == http.StatusOK && rec.Body.String() != "alice" {
			t.Errorf("%s: user = %q, want alice", name, rec.Body.String())
		}
	}
}

func TestAuthenticateLetsPublicPathsThrough(t *testing.T) {
	fakeSessions(t)
	rec := serve(authRouter(), httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

// A sibling subdomain can send the SameSite=Lax cookie, so requests that
// change state must come from the UI's origin.
func TestAuthenticateChecksOriginOfUnsafeRequests(t *testing.T) {
	fakeSessions(t)
	original := constants.FrontendUrl
	constants.FrontendUrl = "https://sm.example.com"
	t.Cleanup(func() { constants.FrontendUrl = original })
	h := authRouter()

	cases := map[string]struct {
		method, origin string
		want           int
	}{
		"POST from the UI":         {http.MethodPost, "https://sm.example.com", http.StatusOK},
		"POST without Origin":      {http.MethodPost, "", http.StatusOK},
		"POST from a sibling site": {http.MethodPost, "https://evil.example.com", http.StatusForbidden},
		"GET from a sibling site":  {http.MethodGet, "https://evil.example.com", http.StatusOK},
	}
	for name, c := range cases {
		req := withSession(httptest.NewRequest(c.method, "/api/whoami", nil), "good")
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		if rec := serve(h, req); rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", name, rec.Code, c.want)
		}
	}
}

func TestSessionCookieIsSecureForHTTPSFrontends(t *testing.T) {
	original := constants.FrontendUrl
	t.Cleanup(func() { constants.FrontendUrl = original })

	for frontend, want := range map[string]bool{
		"http://localhost:5173":                        false,
		"https://sm.example.com":                       true,
		"http://localhost:5173,https://sm.example.com": true,
	} {
		constants.FrontendUrl = frontend
		rec := httptest.NewRecorder()
		setSessionCookie(rec, "token", db.SessionTTL)
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("%s: got %d cookies, want 1", frontend, len(cookies))
		}
		c := cookies[0]
		if c.Secure != want || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
			t.Errorf("%s: cookie = %+v, want Secure=%v, HttpOnly, SameSite=Lax, Path=/", frontend, c, want)
		}
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		remote, realIP, want string
	}{
		{"203.0.113.9:5000", "", "203.0.113.9"},
		{"203.0.113.9:5000", "198.51.100.1", "203.0.113.9"}, // not a proxy: ignored
		{"172.23.0.1:5000", "198.51.100.1", "198.51.100.1"}, // nginx in Docker
		{"127.0.0.1:5000", "198.51.100.1", "198.51.100.1"},
		{"192.168.1.118:5000", "junk", "192.168.1.118"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.remote
		if c.realIP != "" {
			req.Header.Set("X-Real-IP", c.realIP)
		}
		if got := clientIP(req); got != netip.MustParseAddr(c.want) {
			t.Errorf("clientIP(%s, X-Real-IP %q) = %s, want %s", c.remote, c.realIP, got, c.want)
		}
	}
}

// Another user's scan answers 404, like a missing one.
func TestScanRoutesHideOtherUsersScans(t *testing.T) {
	fakeSessions(t)
	original := scanOwnedBy
	scanOwnedBy = func(scanId int, userID int64) (bool, error) { return userID == 7 && scanId == 1, nil }
	t.Cleanup(func() { scanOwnedBy = original })

	r := mux.NewRouter()
	r.Use(authenticate)
	api(r)
	for _, path := range []string{"/api/scans/2", "/api/scans/2/summary", "/api/gmaildata/2", "/api/photos/2"} {
		rec := serve(r, withSession(httptest.NewRequest(http.MethodGet, path, nil), "good"))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
	rec := serve(r, withSession(httptest.NewRequest(http.MethodDelete, "/api/scans/2", nil), "good"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("DELETE /api/scans/2: status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}
