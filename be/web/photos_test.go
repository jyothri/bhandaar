package web

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
)

// photosRouter serves the API as alice (user 7), whose accounts are
// "photos" (granted Google Photos) and "gmail-only", and whose picks
// startPhotosPick, cancelPhotosPick and pickerSession fake.
func photosRouter(t *testing.T, start func(int64, string) (db.PickerSession, error)) *mux.Router {
	t.Helper()
	fakeSessions(t)
	originalAccount, originalStart, originalCancel, originalSession, originalActive, originalItems, originalOwner :=
		accountFor, startPhotosPick, cancelPhotosPick, pickerSession, activePick, pickedItems, scanOwnedBy
	t.Cleanup(func() {
		accountFor, startPhotosPick, cancelPhotosPick, pickerSession, activePick, pickedItems, scanOwnedBy =
			originalAccount, originalStart, originalCancel, originalSession, originalActive, originalItems, originalOwner
	})
	accountFor = func(userID int64, clientKey string) (db.PrivateToken, error) {
		scopes := map[string]string{
			"photos":     "openid https://www.googleapis.com/auth/photospicker.mediaitems.readonly",
			"gmail-only": "https://www.googleapis.com/auth/gmail.readonly",
		}
		if scope, ok := scopes[clientKey]; ok && userID == 7 {
			return db.PrivateToken{Client_key: clientKey, Scope: scope}, nil
		}
		return db.PrivateToken{}, fmt.Errorf("no account %s: %w", clientKey, sql.ErrNoRows)
	}
	startPhotosPick = start
	cancelPhotosPick = func(userID int64, key string) error {
		switch {
		case userID != 7 || key == "someone-elses":
			return db.ErrNotFound
		case key == "done":
			return collect.ErrPickNotWaiting
		}
		return nil
	}
	pickerSession = func(userID int64, key string) (db.PickerSession, error) {
		if userID != 7 || key != "k1" {
			return db.PickerSession{}, db.ErrNotFound
		}
		return db.PickerSession{SessionKey: "k1", PickerId: "secret-picker-id", PickerUri: "https://photos.google.com/p/1", State: db.PickScanning,
			ScanId: sql.NullInt64{Int64: 42, Valid: true}, PickBy: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}, nil
	}
	activePick = func(userID int64) (db.PickerSession, error) {
		return pickerSession(userID, "k1")
	}
	pickedItems = func(scanId int, page int) (db.PickedItemPage, error) {
		return db.PickedItemPage{Items: []db.PickedItem{{MediaItemId: "m1"}}, Page: page, Total: 1}, nil
	}
	scanOwnedBy = func(scanId int, userID int64) (bool, error) { return userID == 7 && scanId == 42, nil }

	r := mux.NewRouter()
	r.Use(authenticate)
	api(r)
	return r
}

func startPick(r *mux.Router, body string) *httptest.ResponseRecorder {
	return serve(r, withSession(httptest.NewRequest(http.MethodPost, "/api/photos/sessions", strings.NewReader(body)), "good"))
}

func TestStartPhotosPick(t *testing.T) {
	var started string
	r := photosRouter(t, func(userID int64, clientKey string) (db.PickerSession, error) {
		started = clientKey
		return db.PickerSession{SessionKey: "k1", PickerId: "secret-picker-id", PickerUri: "https://photos.google.com/p/1",
			PickBy: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}, nil
	})
	rec := startPick(r, `{"clientKey": "photos"}`)
	if rec.Code != http.StatusOK || started != "photos" {
		t.Fatalf("start: status %d, started %q: %s", rec.Code, started, rec.Body)
	}
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["sessionKey"] != "k1" || body["pickerUri"] != "https://photos.google.com/p/1" || body["pickBy"] != "2026-09-28T12:00:00Z" {
		t.Errorf("body = %v", body)
	}
	if strings.Contains(rec.Body.String(), "secret-picker-id") {
		t.Error("the response gives away Google's session ID")
	}
}

func TestStartPhotosPickChecksTheAccount(t *testing.T) {
	r := photosRouter(t, func(int64, string) (db.PickerSession, error) {
		t.Error("started a pick it should have refused")
		return db.PickerSession{}, nil
	})
	for body, want := range map[string]string{
		`{}`:                          "Pick an account",
		`{"clientKey": "gmail-only"}`: `hasn't granted Google Photos access. Use "Grant Photos access" first.`,
		`{"clientKey": "not-linked"}`: "isn't linked",
	} {
		if rec := startPick(r, body); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %q, want 400 with %q", body, rec.Code, rec.Body, want)
		}
	}
}

func TestStartPhotosPickOneAtATime(t *testing.T) {
	r := photosRouter(t, func(int64, string) (db.PickerSession, error) { return db.PickerSession{}, db.ErrPickActive })
	if rec := startPick(r, `{"clientKey": "photos"}`); rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusConflict)
	}
	r = photosRouter(t, func(int64, string) (db.PickerSession, error) { return db.PickerSession{}, fmt.Errorf("Google is down") })
	if rec := startPick(r, `{"clientKey": "photos"}`); rec.Code != http.StatusBadGateway {
		t.Errorf("Google failing: status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestPhotosPickState(t *testing.T) {
	r := photosRouter(t, nil)
	rec := serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/photos/sessions/k1", nil), "good"))
	want := `{"sessionKey":"k1","pickerUri":"https://photos.google.com/p/1","state":"scanning","scanId":42,"pickBy":"2026-09-28T12:00:00Z"}`
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("GET k1: %d %s, want %s", rec.Code, rec.Body, want)
	}
	rec = serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/photos/sessions", nil), "good"))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("GET the active pick: %d %s, want %s", rec.Code, rec.Body, want)
	}
	activePick = func(int64) (db.PickerSession, error) { return db.PickerSession{}, db.ErrNotFound }
	rec = serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/photos/sessions", nil), "good"))
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "null" {
		t.Errorf("GET the active pick, with none: %d %s, want null", rec.Code, rec.Body)
	}
	rec = serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/photos/sessions/other", nil), "good"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET another user's pick: status = %d, want 404", rec.Code)
	}
}

func TestCancelPhotosPick(t *testing.T) {
	r := photosRouter(t, nil)
	for key, want := range map[string]int{"k1": http.StatusNoContent, "someone-elses": http.StatusNotFound, "done": http.StatusConflict} {
		rec := serve(r, withSession(httptest.NewRequest(http.MethodDelete, "/api/photos/sessions/"+key, nil), "good"))
		if rec.Code != want {
			t.Errorf("DELETE %s: status = %d, want %d", key, rec.Code, want)
		}
	}
}

func TestPickedItems(t *testing.T) {
	r := photosRouter(t, nil)
	rec := serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/photos/42?page=2", nil), "good"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"media_item_id":"m1"`) || !strings.Contains(rec.Body.String(), `"page":2`) {
		t.Errorf("GET /api/photos/42: %d %s", rec.Code, rec.Body)
	}
	rec = serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/photos/43", nil), "good"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET another user's scan: status = %d, want 404", rec.Code)
	}
}
