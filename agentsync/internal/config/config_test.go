package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

var goodSecret = base64.StdEncoding.EncodeToString(make([]byte, 48))

func TestDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"AGENTSYNC_JWT_SECRET": goodSecret}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8091" || cfg.AccessTTL != 15*time.Minute || cfg.RefreshTTL != 720*time.Hour {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.DB.Host != "hdd_db" || cfg.DB.Port != 5432 || cfg.DB.User != "hddb" || cfg.DB.Name != "hdd_db" || cfg.DB.SSLMode != "disable" {
		t.Errorf("unexpected DB defaults: %+v", cfg.DB)
	}
	if cfg.MinAgentVersion.String() != "0.1.0" || cfg.LatestAgentVersion.String() != "0.1.0" {
		t.Errorf("unexpected versions: %s %s", cfg.MinAgentVersion, cfg.LatestAgentVersion)
	}
	if cfg.AgentDownloadURL != "https://github.com/jyothri/bhandaar/releases/latest" {
		t.Errorf("download url = %q", cfg.AgentDownloadURL)
	}
	if len(cfg.JWTSecret) != 48 {
		t.Errorf("secret length = %d", len(cfg.JWTSecret))
	}
	if len(cfg.TrustedProxies) != 5 {
		t.Errorf("trusted proxies = %v", cfg.TrustedProxies)
	}
}

func TestOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"AGENTSYNC_JWT_SECRET":           goodSecret,
		"AGENTSYNC_LISTEN":               "127.0.0.1:9000",
		"AGENTSYNC_ACCESS_TTL":           "5m",
		"AGENTSYNC_MIN_AGENT_VERSION":    "0.2.0",
		"AGENTSYNC_LATEST_AGENT_VERSION": "0.3.1",
		"AGENTSYNC_TRUSTED_PROXIES":      "192.168.1.118, 172.18.0.0/16",
		"DB_HOST":                        "localhost",
		"DB_PORT":                        "5433",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:9000" || cfg.AccessTTL != 5*time.Minute || cfg.DB.Host != "localhost" || cfg.DB.Port != 5433 {
		t.Errorf("overrides not applied: %+v", cfg)
	}
	if cfg.MinAgentVersion.String() != "0.2.0" || cfg.LatestAgentVersion.String() != "0.3.1" {
		t.Errorf("versions: %s %s", cfg.MinAgentVersion, cfg.LatestAgentVersion)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0].String() != "192.168.1.118/32" {
		t.Errorf("trusted proxies = %v", cfg.TrustedProxies)
	}
}

func TestInvalid(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"missing secret":   {map[string]string{}, "AGENTSYNC_JWT_SECRET is required"},
		"short secret":     {map[string]string{"AGENTSYNC_JWT_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 31))}, "31 bytes"},
		"not base64":       {map[string]string{"AGENTSYNC_JWT_SECRET": "not base64 at all!!"}, "not valid base64"},
		"bad ttl":          {map[string]string{"AGENTSYNC_JWT_SECRET": goodSecret, "AGENTSYNC_ACCESS_TTL": "soon"}, "AGENTSYNC_ACCESS_TTL"},
		"negative ttl":     {map[string]string{"AGENTSYNC_JWT_SECRET": goodSecret, "AGENTSYNC_REFRESH_TTL": "-1h"}, "AGENTSYNC_REFRESH_TTL"},
		"bad version":      {map[string]string{"AGENTSYNC_JWT_SECRET": goodSecret, "AGENTSYNC_MIN_AGENT_VERSION": "v1"}, "AGENTSYNC_MIN_AGENT_VERSION"},
		"latest below min": {map[string]string{"AGENTSYNC_JWT_SECRET": goodSecret, "AGENTSYNC_MIN_AGENT_VERSION": "0.3.0", "AGENTSYNC_LATEST_AGENT_VERSION": "0.2.0"}, "is below"},
		"bad port":         {map[string]string{"AGENTSYNC_JWT_SECRET": goodSecret, "DB_PORT": "x"}, "DB_PORT"},
		"bad ssl mode":     {map[string]string{"AGENTSYNC_JWT_SECRET": goodSecret, "DB_SSL_MODE": "prefer"}, "DB_SSL_MODE"},
		"bad proxy":        {map[string]string{"AGENTSYNC_JWT_SECRET": goodSecret, "AGENTSYNC_TRUSTED_PROXIES": "nginx"}, "AGENTSYNC_TRUSTED_PROXIES"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(env(c.env))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}
