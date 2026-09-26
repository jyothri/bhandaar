// Package config resolves the agent's remote settings. Precedence:
// flag > environment > <state-dir>/config.json > default.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultRemoteURL is the server the agent talks to unless told otherwise.
const DefaultRemoteURL = "https://sm.jkurapati.com"

// Environment variables.
const (
	EnvRemoteURL = "DRIVEAGENT_REMOTE_URL"
	EnvLANAddr   = "DRIVEAGENT_LAN_ADDR"
)

// FileName is the optional settings file in the state dir.
const FileName = "config.json"

// Settings are the resolved remote settings.
type Settings struct {
	// RemoteURL is the server's base URL, without a trailing slash.
	RemoteURL string
	// LANAddr, if set, is a host:port to try before DNS (see remote.Dialer).
	LANAddr string
}

// File is the shape of config.json.
type File struct {
	RemoteURL string `json:"remote_url,omitempty"`
	LANAddr   string `json:"lan_addr,omitempty"`
}

// Flags are the command-line values; empty means not given.
type Flags struct {
	RemoteURL string
	LANAddr   string
}

// Resolve works out the settings for stateDir.
func Resolve(stateDir string, flags Flags, getenv func(string) string) (Settings, error) {
	var file File
	b, err := os.ReadFile(filepath.Join(stateDir, FileName))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return Settings{}, err
	default:
		if err := json.Unmarshal(b, &file); err != nil {
			return Settings{}, fmt.Errorf("%s: %w", filepath.Join(stateDir, FileName), err)
		}
	}
	pick := func(flag, env, fromFile, def string) string {
		for _, v := range []string{flag, getenv(env), fromFile} {
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
		return def
	}
	s := Settings{
		RemoteURL: pick(flags.RemoteURL, EnvRemoteURL, file.RemoteURL, DefaultRemoteURL),
		LANAddr:   pick(flags.LANAddr, EnvLANAddr, file.LANAddr, ""),
	}
	if s.RemoteURL, err = NormalizeRemoteURL(s.RemoteURL); err != nil {
		return Settings{}, err
	}
	if s.LANAddr != "" {
		if err := checkHostPort(s.LANAddr); err != nil {
			return Settings{}, fmt.Errorf("lan_addr %q: %w", s.LANAddr, err)
		}
	}
	return s, nil
}

// NormalizeRemoteURL checks a remote URL and drops any trailing slash. It
// must be https, except for localhost and 127.0.0.1 (a local agentserver).
func NormalizeRemoteURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("remote url %q: %w", raw, err)
	}
	if u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Trim(u.Path, "/") != "" {
		return "", fmt.Errorf("remote url %q: want scheme://host[:port], e.g. %s", raw, DefaultRemoteURL)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if h := u.Hostname(); h != "localhost" && h != "127.0.0.1" {
			return "", fmt.Errorf("remote url %q: must use https (http is only allowed for localhost)", raw)
		}
	default:
		return "", fmt.Errorf("remote url %q: must use https", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

func checkHostPort(s string) error {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return err
	}
	if host == "" {
		return errors.New("missing host")
	}
	if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
		return fmt.Errorf("invalid port %q", port)
	}
	return nil
}
