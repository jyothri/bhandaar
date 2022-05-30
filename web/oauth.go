package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/constants"
)

func oauth(r *mux.Router) {
	oauth := r.PathPrefix("/oauth/").Subrouter()
	oauth.HandleFunc("/glink", GoogleAccountLinkingHandler).Methods("GET")
}

func GoogleAccountLinkingHandler(w http.ResponseWriter, r *http.Request) {
	const googleTokenUrl = "https://oauth2.googleapis.com/token"
	const grantType = "authorization_code"
	// TODO(issues/1): Remove hardcoded redirect uri.
	const redirectUri = "http://localhost:8090/oauth/glink"

	var clientId = constants.OauthClientId
	var clientSecret = constants.OauthClientSecret

	// Retrieve authZ code from query params.
	err := r.ParseForm()
	if err != nil {
		panic(err)
	}
	code := r.FormValue("code")

	// Exchange authZ for refresh token.
	reqURL := fmt.Sprintf("%s?client_id=%s&client_secret=%s&code=%s&grant_type=%s&redirect_uri=%s", googleTokenUrl, clientId, clientSecret, code, grantType, redirectUri)
	req, err := http.NewRequest(http.MethodPost, reqURL, nil)
	if err != nil {
		fmt.Printf("could not create HTTP request: %v", err)
		w.WriteHeader(http.StatusBadRequest)
	}
	// We set this header since we want the response
	// as JSON
	req.Header.Set("accept", "application/json")

	// We will be using `httpClient` to make external HTTP requests later in our code
	httpClient := http.Client{}

	// Send out the HTTP request
	res, err := httpClient.Do(req)
	if err != nil {
		fmt.Printf("could not send HTTP request: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
	}
	defer res.Body.Close()

	// Parse the request body into the `OAuthAccessResponse` struct
	var t OAuthAccessResponse
	if err := json.NewDecoder(res.Body).Decode(&t); err != nil {
		fmt.Printf("could not parse JSON response: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Finally, send a response to redirect the user to the "startScan" page
	// with the token
	w.Header().Set("Location", "/startScan?refresh_token="+t.RefreshToken)
	w.WriteHeader(http.StatusFound)
}

type OAuthAccessResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int16  `json:"expires_in"`
}
