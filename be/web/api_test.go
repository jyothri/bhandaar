package web

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
)

// fakeAccounts makes accountFor know alice's (user 7) accounts: "gmail-only"
// and "both".
func fakeAccounts(t *testing.T) {
	t.Helper()
	original := accountFor
	accountFor = func(userID int64, clientKey string) (db.PrivateToken, error) {
		scopes := map[string]string{
			"gmail-only": "openid https://www.googleapis.com/auth/gmail.readonly",
			"both": "https://www.googleapis.com/auth/gmail.readonly " +
				"https://www.googleapis.com/auth/drive.metadata.readonly",
		}
		if scope, ok := scopes[clientKey]; ok && userID == 7 {
			return db.PrivateToken{Client_key: clientKey, Scope: scope}, nil
		}
		return db.PrivateToken{}, fmt.Errorf("failed to get OAuth token for client %s: %w", clientKey, sql.ErrNoRows)
	}
	t.Cleanup(func() { accountFor = original })
}

func TestCheckScanRequest(t *testing.T) {
	fakeAccounts(t)
	gmail := func(key, filter string) DoScanRequest {
		return DoScanRequest{ScanType: "GMail", GMailScan: collect.GMailScan{ClientKey: key, Filter: filter}}
	}
	drive := func(key, query string) DoScanRequest {
		return DoScanRequest{ScanType: "GDrive", GDriveScan: collect.GDriveScan{ClientKey: key, QueryString: query}}
	}
	long := strings.Repeat("x", maxQueryLength+1)

	tests := []struct {
		name    string
		req     DoScanRequest
		status  int
		message string
	}{
		{"gmail on a gmail account", gmail("gmail-only", "is:unread"), http.StatusOK, ""},
		{"drive on a gmail-only account", drive("gmail-only", ""), http.StatusBadRequest, `hasn't granted Google Drive access. Use "Grant Drive access"`},
		{"drive on an account with drive", drive("both", "trashed = false"), http.StatusOK, ""},
		{"another user's account", gmail("bobs", "is:unread"), http.StatusBadRequest, "isn't linked"},
		{"gmail filter too long", gmail("gmail-only", long), http.StatusBadRequest, "too long"},
		{"drive query too long", drive("both", long), http.StatusBadRequest, "too long"},
		{"longest query allowed", drive("both", long[1:]), http.StatusOK, ""},
		{"local scans aren't checked", DoScanRequest{ScanType: "Local"}, http.StatusOK, ""},
	}
	for _, tt := range tests {
		status, message := checkScanRequest(tt.req, 7)
		if status != tt.status || !strings.Contains(message, tt.message) {
			t.Errorf("%s: got %d %q, want %d containing %q", tt.name, status, message, tt.status, tt.message)
		}
	}
}

func TestCheckScanRequestRejectsABadFolderId(t *testing.T) {
	fakeAccounts(t)
	for id, ok := range map[string]bool{
		"":                                  true,
		"1AbCdEfGhIjKlMnOpQrStUvWxYz012345": true,
		"short":                             false,
		"x' or name != 'y":                  false,
	} {
		req := DoScanRequest{ScanType: "GDrive", GDriveScan: collect.GDriveScan{ClientKey: "both", FolderId: id}}
		status, message := checkScanRequest(req, 7)
		if (status == http.StatusOK) != ok {
			t.Errorf("folder %q: got %d %q, want ok=%v", id, status, message, ok)
		}
	}
}

// The Photos Library API's scans and routes are gone; see
// docs/specs/photos-picker.md.
func TestPhotosLibraryAPIIsGone(t *testing.T) {
	fakeSessions(t)
	r := mux.NewRouter()
	r.Use(authenticate)
	api(r)

	req := httptest.NewRequest(http.MethodPost, "/api/scans", strings.NewReader(`{"ScanType": "GPhotos"}`))
	rec := serve(r, withSession(req, "good"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /api/scans GPhotos: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	// "albums" isn't a scan ID to /api/photos/{scan_id}.
	rec = serve(r, withSession(httptest.NewRequest(http.MethodGet, "/api/photos/albums?refresh_token=x", nil), "good"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("GET /api/photos/albums: status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}
