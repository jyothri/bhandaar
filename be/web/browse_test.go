package web

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

// fakeBrowseOwners makes alice (user 7) own account "mine" and agent drive 1.
func fakeBrowseOwners(t *testing.T) {
	t.Helper()
	google, agent := googleAccountOwnedBy, agentDriveOwnedBy
	googleAccountOwnedBy = func(userID int64, clientKey string) (bool, error) {
		return userID == 7 && clientKey == "mine", nil
	}
	agentDriveOwnedBy = func(userID int64, drivePk int64) (bool, error) {
		return userID == 7 && drivePk == 1, nil
	}
	t.Cleanup(func() { googleAccountOwnedBy, agentDriveOwnedBy = google, agent })
}

func TestBrowseChecksTheSourceIsTheUsers(t *testing.T) {
	fakeBrowseOwners(t)
	r := mux.NewRouter()
	browseRoutes(r.PathPrefix("/api/").Subrouter())
	// Routes that would reach the database answer 404 first.
	for _, path := range []string{
		"/api/browse/google/theirs/drive/children",
		"/api/browse/google/theirs/gmail/messages?sort=size",
		"/api/browse/agent/2/children?folder=a",
		"/api/browse/agent/2/status",
		"/api/browse/agent/x/status",
	} {
		req := withUser(httptest.NewRequest("GET", path, nil), db.User{ID: 7, Username: "alice"})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, rec.Code)
		}
	}
	// Bob can't see alice's.
	req := withUser(httptest.NewRequest("GET", "/api/browse/agent/1/status", nil), db.User{ID: 8, Username: "bob"})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("bob's GET of alice's drive = %d, want 404", rec.Code)
	}
}

func TestQueryPage(t *testing.T) {
	for query, want := range map[string]int{"": 1, "?page=3": 3, "?page=0": 1, "?page=-2": 1, "?page=x": 1} {
		if got := queryPage(httptest.NewRequest("GET", "/"+query, nil)); got != want {
			t.Errorf("queryPage(%q) = %d, want %d", query, got, want)
		}
	}
}
