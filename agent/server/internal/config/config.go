// Package config reads agentserver's configuration from the environment.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jyothri/bhandaar/agent/server/internal/semver"
)

// DB holds the Postgres connection settings, with the same variables and
// defaults as be.
type DB struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
	SSLMode  string
}

// Config is the server's configuration.
type Config struct {
	DB                 DB
	Listen             string
	JWTSecret          []byte
	AccessTTL          time.Duration
	RefreshTTL         time.Duration
	MinAgentVersion    semver.Version
	LatestAgentVersion semver.Version
	AgentDownloadURL   string
	// TrustedProxies are the addresses X-Real-IP is accepted from (nginx).
	TrustedProxies []netip.Prefix
}

// MinJWTSecretBytes is the shortest JWT secret, after base64 decoding.
const MinJWTSecretBytes = 32

const defaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"

// withDefaults wraps getenv so that an unset or empty variable gives def.
func withDefaults(getenv func(string) string) func(key, def string) string {
	return func(key, def string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return def
	}
}

// LoadDB reads only the database settings, for the admin commands.
func LoadDB(getenv func(string) string) (DB, error) {
	get := withDefaults(getenv)
	var errs []error
	db := DB{
		Host:     get("DB_HOST", "hdd_db"),
		User:     get("DB_USER", "hddb"),
		Password: get("DB_PASSWORD", ""),
		Name:     get("DB_NAME", "hdd_db"),
		SSLMode:  get("DB_SSL_MODE", "disable"),
	}
	port, err := strconv.Atoi(get("DB_PORT", "5432"))
	if err != nil || port <= 0 || port > 65535 {
		errs = append(errs, fmt.Errorf("DB_PORT: invalid port %q", get("DB_PORT", "5432")))
	}
	db.Port = port
	switch db.SSLMode {
	case "disable", "require", "verify-ca", "verify-full":
	default:
		errs = append(errs, fmt.Errorf("DB_SSL_MODE: must be disable, require, verify-ca or verify-full, got %q", db.SSLMode))
	}
	return db, errors.Join(errs...)
}

// FromEnv reads the configuration from the environment.
func FromEnv() (Config, error) {
	return Load(os.Getenv)
}

// Load reads the configuration through getenv, and validates it.
func Load(getenv func(string) string) (Config, error) {
	get := withDefaults(getenv)
	var errs []error
	db, err := LoadDB(getenv)
	if err != nil {
		errs = append(errs, err)
	}
	cfg := Config{
		DB:               db,
		Listen:           get("AGENTSERVER_LISTEN", ":8091"),
		AgentDownloadURL: get("AGENTSERVER_AGENT_DOWNLOAD_URL", "https://github.com/jyothri/bhandaar/releases/latest"),
	}

	secret := getenv("AGENTSERVER_JWT_SECRET")
	if secret == "" {
		errs = append(errs, errors.New("AGENTSERVER_JWT_SECRET is required (generate one with: openssl rand -base64 48)"))
	} else if b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(secret)); err != nil {
		errs = append(errs, fmt.Errorf("AGENTSERVER_JWT_SECRET: not valid base64: %v", err))
	} else if len(b) < MinJWTSecretBytes {
		errs = append(errs, fmt.Errorf("AGENTSERVER_JWT_SECRET: %d bytes after decoding, need at least %d", len(b), MinJWTSecretBytes))
	} else {
		cfg.JWTSecret = b
	}

	duration := func(key, def string) time.Duration {
		d, err := time.ParseDuration(get(key, def))
		if err != nil || d <= 0 {
			errs = append(errs, fmt.Errorf("%s: invalid duration %q", key, get(key, def)))
		}
		return d
	}
	cfg.AccessTTL = duration("AGENTSERVER_ACCESS_TTL", "15m")
	cfg.RefreshTTL = duration("AGENTSERVER_REFRESH_TTL", "720h")

	version := func(key string) semver.Version {
		v, err := semver.Parse(get(key, "0.1.0"))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %v", key, err))
		}
		return v
	}
	cfg.MinAgentVersion = version("AGENTSERVER_MIN_AGENT_VERSION")
	cfg.LatestAgentVersion = version("AGENTSERVER_LATEST_AGENT_VERSION")
	if cfg.LatestAgentVersion.Less(cfg.MinAgentVersion) {
		errs = append(errs, fmt.Errorf("AGENTSERVER_LATEST_AGENT_VERSION (%s) is below AGENTSERVER_MIN_AGENT_VERSION (%s)",
			cfg.LatestAgentVersion, cfg.MinAgentVersion))
	}

	for _, s := range strings.Split(get("AGENTSERVER_TRUSTED_PROXIES", defaultTrustedProxies), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !strings.Contains(s, "/") {
			if a, err := netip.ParseAddr(s); err == nil {
				cfg.TrustedProxies = append(cfg.TrustedProxies, netip.PrefixFrom(a, a.BitLen()))
				continue
			}
		}
		p, err := netip.ParsePrefix(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("AGENTSERVER_TRUSTED_PROXIES: %v", err))
			continue
		}
		cfg.TrustedProxies = append(cfg.TrustedProxies, p.Masked())
	}

	return cfg, errors.Join(errs...)
}
