package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

func TestSettingsRoutes(t *testing.T) {
	saved := map[int64]db.Settings{}
	originalGet, originalSave := getSettings, saveSettings
	getSettings = func(user int64) (db.Settings, error) { return saved[user], nil }
	saveSettings = func(user int64, s db.Settings) error { saved[user] = s; return nil }
	t.Cleanup(func() { getSettings, saveSettings = originalGet, originalSave })

	call := func(method, body string) *httptest.ResponseRecorder {
		r := mux.NewRouter()
		settingsRoutes(r.PathPrefix("/api/").Subrouter())
		req := withUser(httptest.NewRequest(method, "/api/settings", strings.NewReader(body)), db.User{ID: 7, Username: "alice"})
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
	if rec := call("GET", ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"show_gcs":false}` {
		t.Errorf("defaults: %d %s", rec.Code, rec.Body)
	}
	if rec := call("PUT", `{"show_gcs":true}`); rec.Code != http.StatusOK || !saved[7].ShowGcs {
		t.Errorf("saving: %d %s, saved %+v", rec.Code, rec.Body, saved)
	}
	if rec := call("GET", ""); strings.TrimSpace(rec.Body.String()) != `{"show_gcs":true}` {
		t.Errorf("after saving: %s", rec.Body)
	}
	for _, body := range []string{"", "nope", `{"show_gcs":"yes"}`, `{"theme":"dark"}`} {
		if rec := call("PUT", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: %d, want 400", body, rec.Code)
		}
	}
}
