package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func writeFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDefaults(t *testing.T) {
	s, err := Resolve(t.TempDir(), Flags{}, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if s.RemoteURL != DefaultRemoteURL || s.LANAddr != "" {
		t.Errorf("settings = %+v", s)
	}
}

func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{"remote_url": "https://file.example", "lan_addr": "10.0.0.1:443"}`)
	env := map[string]string{EnvRemoteURL: "https://env.example", EnvLANAddr: "10.0.0.2:443"}
	getenv := func(k string) string { return env[k] }

	s, _ := Resolve(dir, Flags{}, noEnv)
	if s.RemoteURL != "https://file.example" || s.LANAddr != "10.0.0.1:443" {
		t.Errorf("file: %+v", s)
	}
	s, _ = Resolve(dir, Flags{}, getenv)
	if s.RemoteURL != "https://env.example" || s.LANAddr != "10.0.0.2:443" {
		t.Errorf("env over file: %+v", s)
	}
	s, _ = Resolve(dir, Flags{RemoteURL: "https://flag.example/", LANAddr: "10.0.0.3:8443"}, getenv)
	if s.RemoteURL != "https://flag.example" || s.LANAddr != "10.0.0.3:8443" {
		t.Errorf("flag over env: %+v", s)
	}
}

func TestRemoteURLRules(t *testing.T) {
	good := map[string]string{
		"https://sm.jkurapati.com":  "https://sm.jkurapati.com",
		"https://sm.jkurapati.com/": "https://sm.jkurapati.com",
		"https://example.com:8443":  "https://example.com:8443",
		"http://localhost:8091":     "http://localhost:8091",
		"http://127.0.0.1:8091/":    "http://127.0.0.1:8091",
	}
	for in, want := range good {
		got, err := NormalizeRemoteURL(in)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"http://sm.jkurapati.com", "http://192.168.1.118", "ftp://x", "sm.jkurapati.com",
		"https://sm.jkurapati.com/agent", "https://user:pw@sm.jkurapati.com", "https://x?y=1", "",
	} {
		if _, err := NormalizeRemoteURL(in); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}

func TestInvalidSettings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{not json`)
	if _, err := Resolve(dir, Flags{}, noEnv); err == nil || !strings.Contains(err.Error(), FileName) {
		t.Errorf("bad config.json: %v", err)
	}
	for _, lan := range []string{"192.168.1.118", ":443", "host:0", "host:https"} {
		if _, err := Resolve(t.TempDir(), Flags{LANAddr: lan}, noEnv); err == nil {
			t.Errorf("lan_addr %q: want an error", lan)
		}
	}
	if _, err := Resolve(t.TempDir(), Flags{RemoteURL: "http://sm.jkurapati.com"}, noEnv); err == nil {
		t.Error("plain http to a remote host should be refused")
	}
}
