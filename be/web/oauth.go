package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// tokenEndpoint is where authorization codes are exchanged; tests point it
// at a fake server.
var tokenEndpoint = google.Endpoint

func oauth(r *mux.Router) {
	// OAuth routes with smaller body limit (16 KB)
	oauthRouter := r.PathPrefix("/api/").Subrouter()
	oauthRouter.Use(RequestSizeLimitMiddleware(OAuthCallbackMaxBodySize))
	oauthRouter.HandleFunc("/glink", GoogleAccountLinkingHandler).Methods("GET")
}

func GoogleAccountLinkingHandler(w http.ResponseWriter, r *http.Request) {
	var redirectUri = r.FormValue("redirectUri")

	if redirectUri == "" {
		http.Error(w, "redirectUri not found in request", http.StatusBadRequest)
		return
	}
	returnUrl, err := linkReturnURL(redirectUri)
	if err != nil {
		slog.Warn("Rejected account-linking redirectUri", "redirect_uri", redirectUri, "error", err)
		http.Error(w, "redirectUri is not allowed", http.StatusBadRequest)
		return
	}

	// Retrieve authZ code from query params.
	err = r.ParseForm()
	if handleMaxBytesError(w, r, err, OAuthCallbackMaxBodySize) {
		return
	}

	if err != nil {
		slog.Error("Failed to parse OAuth form", "error", err)
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")
	if code == "" {
		http.Error(w, "code not found in request", http.StatusBadRequest)
		return
	}

	// Exchange the authorization code for tokens. oauth2 sends the fields
	// form-encoded in the POST body, keeping the client secret out of URLs
	// and logs, and returns an error for any non-2xx response.
	config := &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     tokenEndpoint,
		RedirectURL:  redirectUri,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	token, err := config.Exchange(ctx, code)
	if err != nil {
		slog.Warn("OAuth token exchange failed", "error", err)
		http.Error(w, "Failed to exchange the authorization code with Google", http.StatusBadGateway)
		return
	}

	if token.AccessToken == "" || token.RefreshToken == "" {
		slog.Warn("Access or refresh token missing from token response",
			"has_access_token", token.AccessToken != "",
			"has_refresh_token", token.RefreshToken != "")
		http.Error(w, "Access or Refresh token could not be obtained", http.StatusBadRequest)
		return
	}
	scope, _ := token.Extra("scope").(string)
	var expiresIn int16
	if !token.Expiry.IsZero() {
		expiresIn = int16(time.Until(token.Expiry).Seconds())
	}

	rawIDToken, _ := token.Extra("id_token").(string)
	identity, err := parseIDToken(rawIDToken)
	if err != nil {
		slog.Warn("No usable id_token in token response", "error", err)
		http.Error(w, "Google didn't identify the account; link it again, allowing access to your email address", http.StatusBadRequest)
		return
	}

	newClientKey := generateRandomString(12)
	clientKey, err := linkAccount(currentUser(r).ID, db.GoogleLink{
		GoogleSub:    identity.Sub,
		DisplayName:  getDisplayName(identity.Email, newClientKey),
		AccessToken:  token.AccessToken,
		RefreshToken: token.RefreshToken,
		Scope:        scope,
		ExpiresIn:    expiresIn,
		TokenType:    token.TokenType,
	}, newClientKey)
	if err != nil {
		slog.Error("Failed to save linked account", "error", err)
		http.Error(w, "Failed to save account information", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, returnUrl+"?"+url.Values{"account": {clientKey}}.Encode(), http.StatusFound)
}

// linkAccount stores a linked account; tests replace it.
var linkAccount = db.LinkAccount

// googleIdentity is the part of an OpenID Connect ID token linking uses.
type googleIdentity struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
}

// parseIDToken reads the claims of the id_token Google's token endpoint
// returned. Its signature isn't checked: it came straight from Google over
// TLS, in the code exchange, which is the case OpenID Connect (section
// 3.1.3.7) and Google's docs exempt from validation.
func parseIDToken(raw string) (googleIdentity, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return googleIdentity{}, errors.New("id_token is missing or not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return googleIdentity{}, fmt.Errorf("failed to decode id_token: %w", err)
	}
	var identity googleIdentity
	if err := json.Unmarshal(payload, &identity); err != nil {
		return googleIdentity{}, fmt.Errorf("failed to parse id_token: %w", err)
	}
	if identity.Sub == "" {
		return googleIdentity{}, errors.New("id_token has no sub")
	}
	return identity, nil
}

// linkReturnURL checks that redirectUri belongs to a UI this backend serves
// (one of -frontend_url's origins, which CORS already trusts), and returns
// the page on that UI to send the user to after linking. Only the origin
// is compared; the return URL is built from -frontend_url, never from the
// request.
func linkReturnURL(redirectUri string) (string, error) {
	u, err := url.Parse(redirectUri)
	if err != nil {
		return "", err
	}
	if u.User == nil {
		for _, origin := range constants.FrontendOrigins() {
			frontend, err := url.Parse(origin)
			if err != nil || frontend.Host == "" {
				continue
			}
			if u.Scheme == frontend.Scheme && strings.EqualFold(u.Host, frontend.Host) {
				return frontend.Scheme + "://" + frontend.Host + "/request", nil
			}
		}
	}
	return "", fmt.Errorf("origin %s://%s is not in -frontend_url (%s)", u.Scheme, u.Host, constants.FrontendUrl)
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
