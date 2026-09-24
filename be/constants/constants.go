package constants

import (
	"flag"
	"testing"
)

var (
	OauthClientId     string
	OauthClientSecret string
	FrontendUrl       string
)

func init() {
	flag.StringVar(&OauthClientId, "oauth_client_id", "dummy", "oauth client id")
	flag.StringVar(&OauthClientSecret, "oauth_client_secret", "dummy", "oauth client secret")
	flag.StringVar(&FrontendUrl, "frontend_url", "http://localhost:5173", "URLs allowlisted by UI for CORS.")
	// Parsed here, not in main, because other packages' init() read these
	// values. Skipped in test binaries, whose -test.* flags aren't registered
	// until after init; tests use the defaults.
	if !testing.Testing() {
		flag.Parse()
	}
}
