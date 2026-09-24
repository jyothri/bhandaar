package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// linkRequest calls the account-linking handler with the given query string.
func linkRequest(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/glink?"+query, nil)
	rec := httptest.NewRecorder()
	GoogleAccountLinkingHandler(rec, req)
	return rec
}

func TestLinkRejectsMissingRedirectURI(t *testing.T) {
	rec := linkRequest(t, "code=abc")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "redirectUri not found") {
		t.Errorf("body = %q, want it to explain the missing redirectUri", rec.Body.String())
	}
}
