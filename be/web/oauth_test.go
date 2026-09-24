package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jyothri/hdd/constants"
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

// fakeTokenEndpoint points the token exchange at handler for this test.
func fakeTokenEndpoint(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	original := tokenEndpoint
	tokenEndpoint.TokenURL = server.URL + "/token"
	t.Cleanup(func() { tokenEndpoint = original })
}

const testRedirectURI = "http://localhost:5173/oauth/glink" // under the default -frontend_url

func linkQuery(code string) string {
	return url.Values{"code": {code}, "redirectUri": {testRedirectURI}}.Encode()
}

func TestLinkRejectsMissingCode(t *testing.T) {
	rec := linkRequest(t, url.Values{"redirectUri": {testRedirectURI}}.Encode())

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// 7.1: an unreachable token endpoint used to leave res nil and panic.
func TestLinkTokenEndpointUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	original := tokenEndpoint
	tokenEndpoint.TokenURL = server.URL + "/token"
	t.Cleanup(func() { tokenEndpoint = original })

	rec := linkRequest(t, linkQuery("abc"))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

// 7.1: an error status from Google must not be decoded as tokens.
func TestLinkTokenEndpointErrorStatus(t *testing.T) {
	fakeTokenEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"invalid_grant","error_description":"Bad Request"}`))
	})

	rec := linkRequest(t, linkQuery("abc"))

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

// 7.2: the exchange is a form-encoded POST body, with nothing in the URL.
func TestLinkSendsFormEncodedTokenRequest(t *testing.T) {
	originalID, originalSecret := constants.OauthClientId, constants.OauthClientSecret
	constants.OauthClientId, constants.OauthClientSecret = "client-id", "s3cr/et+&=x"
	t.Cleanup(func() { constants.OauthClientId, constants.OauthClientSecret = originalID, originalSecret })

	var got *http.Request
	var form url.Values
	fakeTokenEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		got = r
		r.ParseForm()
		form = r.PostForm
		// No refresh token: the handler stops before touching Google or the DB.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"access_token":"at","token_type":"Bearer","expires_in":3599}`))
	})

	rec := linkRequest(t, linkQuery("4/0A&b+c=d"))

	if got == nil {
		t.Fatal("token endpoint was not called")
	}
	if got.Method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.Method)
	}
	if got.URL.RawQuery != "" {
		t.Errorf("query = %q, want empty (no secrets in the URL)", got.URL.RawQuery)
	}
	if ct := got.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("Content-Type = %q, want form encoding", ct)
	}
	want := map[string]string{
		"grant_type":    "authorization_code",
		"code":          "4/0A&b+c=d",
		"redirect_uri":  testRedirectURI,
		"client_id":     "client-id",
		"client_secret": "s3cr/et+&=x",
	}
	for key, value := range want {
		if form.Get(key) != value {
			t.Errorf("form %s = %q, want %q", key, form.Get(key), value)
		}
	}
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "could not be obtained") {
		t.Errorf("got %d %q, want 400 for a response without a refresh token", rec.Code, rec.Body.String())
	}
}

// 7.3: only the -frontend_url origin may receive the user after linking.
func TestLinkReturnURL(t *testing.T) {
	original := constants.FrontendUrl
	// A list, as production uses: the public UI and the LAN one.
	constants.FrontendUrl = "https://sm.example.com/, http://192.168.1.118:5173"
	t.Cleanup(func() { constants.FrontendUrl = original })

	allowed := map[string]string{
		"https://sm.example.com/oauth/glink":    "https://sm.example.com/request",
		"https://SM.example.com/oauth/glink":    "https://sm.example.com/request",
		"https://sm.example.com/anything?x=1":   "https://sm.example.com/request",
		"http://192.168.1.118:5173/oauth/glink": "http://192.168.1.118:5173/request",
	}
	for uri, want := range allowed {
		got, err := linkReturnURL(uri)
		if err != nil || got != want {
			t.Errorf("linkReturnURL(%q) = %q, %v; want %q", uri, got, err, want)
		}
	}

	rejected := []string{
		"https://evil.example.com/oauth/glink",
		"http://sm.example.com/oauth/glink",           // scheme
		"https://sm.example.com:8443/oauth/glink",     // port
		"http://192.168.1.118:8080/oauth/glink",       // other port on a listed host
		"https://sm.example.com.evil.com/oauth/glink", // suffix
		"https://sm.example.com@evil.com/oauth/glink", // userinfo trick
		"https://user@sm.example.com/oauth/glink",     // any userinfo
		"//evil.com/oauth/glink",
		"/oauth/glink",
		"javascript:alert(1)",
		"%zz",
	}
	for _, uri := range rejected {
		if got, err := linkReturnURL(uri); err == nil {
			t.Errorf("linkReturnURL(%q) = %q, want an error", uri, got)
		}
	}
}

func TestLinkRejectsForeignRedirectBeforeExchange(t *testing.T) {
	called := false
	fakeTokenEndpoint(t, func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	rec := linkRequest(t, url.Values{
		"code":        {"abc"},
		"redirectUri": {"https://evil.example.com/oauth/glink"},
	}.Encode())

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if called {
		t.Error("the code was exchanged for a rejected redirectUri")
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want no redirect", loc)
	}
}
