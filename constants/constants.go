package constants

import (
	"flag"
)

var (
	OauthClientId     string
	OauthClientSecret string
	RefreshToken      string
	StartWebServer    bool
)

func init() {
	flag.StringVar(&OauthClientId, "oauth_client_id", "dummy", "oauth client id")
	flag.StringVar(&OauthClientSecret, "oauth_client_secret", "dummy", "oauth client secret")
	flag.StringVar(&RefreshToken, "refresh_token", "dummy", "refresh token for the user")
	flag.BoolVar(&StartWebServer, "start_web_server", false, "Set to true to start a web server.")
	flag.Parse()
}
