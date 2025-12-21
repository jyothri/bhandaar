package web

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
)

func oauth(r *mux.Router) {
	// OAuth routes with smaller body limit (16 KB)
	oauthRouter := r.PathPrefix("/api/").Subrouter()
	oauthRouter.Use(RequestSizeLimitMiddleware(OAuthCallbackMaxBodySize))
	oauthRouter.HandleFunc("/glink", GoogleAccountLinkingHandler).Methods("GET")
}

func GoogleAccountLinkingHandler(w http.ResponseWriter, r *http.Request) {
	const googleTokenUrl = "https://oauth2.googleapis.com/token"
	const grantType = "authorization_code"
	var redirectUri = r.FormValue("redirectUri")

	if redirectUri == "" {
		w.Write([]byte("redirectUri not found in request"))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var clientId = constants.OauthClientId
	var clientSecret = constants.OauthClientSecret

	// Retrieve authZ code from query params.
	err := r.ParseForm()
	if handleMaxBytesError(w, r, err, OAuthCallbackMaxBodySize) {
		return
	}

	if err != nil {
		slog.Error("Failed to parse OAuth form", "error", err)
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")

	// Exchange authZ for refresh token.
	reqURL := fmt.Sprintf("%s?client_id=%s&client_secret=%s&code=%s&grant_type=%s&redirect_uri=%s", googleTokenUrl, clientId, clientSecret, code, grantType, redirectUri)
	req, err := http.NewRequest(http.MethodPost, reqURL, nil)
	if err != nil {
		slog.Error("Failed to create HTTP request", "error", err)
		http.Error(w, "Failed to create OAuth request", http.StatusBadRequest)
		return
	}
	// We set this header since we want the response
	// as JSON
	req.Header.Set("accept", "application/json")

	// We will be using `httpClient` to make external HTTP requests later in our code
	httpClient := http.Client{}

	// Send out the HTTP request
	res, err := httpClient.Do(req)
	if err != nil {
		slog.Warn(fmt.Sprintf("could not send HTTP request: %v", err))
		w.WriteHeader(http.StatusInternalServerError)
	}
	defer res.Body.Close()

	// Parse the request body into the `OAuthAccessResponse` struct
	var t OAuthAccessResponse
	if err := json.NewDecoder(res.Body).Decode(&t); err != nil {
		slog.Warn(fmt.Sprintf("could not parse JSON response: %v", err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if t.AccessToken == "" || t.RefreshToken == "" {
		slog.Warn(fmt.Sprintf("Access or Refresh token could not be obtained. JSON resp: %v raw resp:%v.\n", t, res.Body))
		http.Error(w, "Access or Refresh token could not be obtained", http.StatusBadRequest)
		return
	}

	client_key := generateRandomString(12)

	email, err := collect.GetIdentity(t.RefreshToken)
	if err != nil {
		slog.Error("Failed to get user identity",
			"error", err)
		http.Error(w, "Failed to verify account", http.StatusInternalServerError)
		return
	}

	display_name := getDisplayName(email, client_key)

	err = db.SaveOAuthToken(t.AccessToken, t.RefreshToken, display_name, client_key, t.Scope, t.ExpiresIn, t.TokenType)
	if err != nil {
		slog.Error("Failed to save OAuth token",
			"client_key", client_key,
			"error", err)
		http.Error(w, "Failed to save account information", http.StatusInternalServerError)
		return
	}

	u, err := url.Parse(redirectUri)
	if err != nil {
		slog.Error("Failed to parse redirect URI",
			"redirect_uri", redirectUri,
			"error", err)
		http.Error(w, "Invalid redirect URI", http.StatusBadRequest)
		return
	}

	returnUrl := u.Scheme + "://" + u.Host + "/request"
	w.Header().Set("Location", returnUrl)
	w.WriteHeader(http.StatusFound)
}

type OAuthAccessResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int16  `json:"expires_in"`
}

func getDisplayName(email string, client_key string) string {
	username := ""
	if email == "" || !strings.Contains(email, "@") {
		return client_key
	} else {
		username = email[0:strings.Index(email, "@")]
		if len(username) < 6 {
			return client_key
		} else {
			return username[0:3] + "****" + username[len(username)-2:] + email[strings.Index(email, "@"):]
		}
	}
}

func generateRandomString(length int) string {
	var chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890-"
	ll := len(chars)
	b := make([]byte, length)
	rand.Read(b) // generates len(b) random bytes
	for i := 0; i < length; i++ {
		b[i] = chars[int(b[i])%ll]
	}
	return string(b)
}
