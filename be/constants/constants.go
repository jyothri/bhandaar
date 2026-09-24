package constants

import (
	"flag"
	"strings"
)

var (
	OauthClientId     string
	OauthClientSecret string
	FrontendUrl       string
)

// Flags are registered here and parsed by main. Read these values only after
// flag.Parse(): at request time or lazily, never from another package's init().
func init() {
	flag.StringVar(&OauthClientId, "oauth_client_id", "dummy", "oauth client id")
	flag.StringVar(&OauthClientSecret, "oauth_client_secret", "dummy", "oauth client secret")
	flag.StringVar(&FrontendUrl, "frontend_url", "http://localhost:5173",
		"UI origin, or a comma-separated list of origins, trusted for CORS and as account-linking return targets.")
}

// FrontendOrigins returns the origins listed in -frontend_url, trimmed and
// without trailing slashes.
func FrontendOrigins() []string {
	var origins []string
	for _, origin := range strings.Split(FrontendUrl, ",") {
		origin = strings.TrimRight(strings.TrimSpace(origin), "/")
		if origin != "" {
			origins = append(origins, origin)
		}
	}
	return origins
}
