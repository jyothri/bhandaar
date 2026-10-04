package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// script is a stand-in driveagent that reports version v.
func script(v string) []byte {
	return []byte("#!/bin/sh\necho \"driveagent " + v + " (test), protocols [1]\"\n")
}

// tarball packs files (name → content) as a release does.
func tarball(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, b := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(b)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		tw.Write(b)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// release is a fake release server's content for one version.
type release struct {
	files map[string][]byte // by name: tarballs, SHA256SUMS, SHA256SUMS.sig
}

// signedRelease is release version v with a tarball for this platform
// holding bin, signed with priv.
func signedRelease(t *testing.T, priv ed25519.PrivateKey, v string, bin []byte) release {
	t.Helper()
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	tb := tarball(t, map[string][]byte{"driveagent": bin, "README.md": []byte("readme")})
	sum := sha256.Sum256(tb)
	sums := []byte(hex.EncodeToString(sum[:]) + "  " + asset + "\n")
	return release{files: map[string][]byte{
		asset:            tb,
		"SHA256SUMS":     sums,
		"SHA256SUMS.sig": ed25519.Sign(priv, Message(v, sums)),
	}}
}

// serve serves releases by version, at /driveagent/v<V>/<name>.
func serve(t *testing.T, releases map[string]release) Source {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, "/driveagent/v")
		v, name, _ := strings.Cut(rest, "/")
		b, found := releases[v].files[name]
		if !ok || !found {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return Source{BaseURL: srv.URL, Client: srv.Client(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

func TestFetchVerifiesAndExtracts(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	src := serve(t, map[string]release{"0.7.1": signedRelease(t, priv, "0.7.1", script("0.7.1"))})
	src.Keys = []ed25519.PublicKey{pub}
	bin, err := src.Fetch(context.Background(), "0.7.1")
	if err != nil || !bytes.Equal(bin, script("0.7.1")) {
		t.Fatalf("Fetch = %q, %v", bin, err)
	}
}

func TestFetchRefuses(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	good := func() release { return signedRelease(t, priv, "0.7.1", script("0.7.1")) }
	cases := map[string]struct {
		release func() release
		want    string
	}{
		"an unknown key": {func() release { return signedRelease(t, otherPriv, "0.7.1", script("0.7.1")) }, "signature doesn't verify"},
		// An older release, validly signed, served under the newer tag.
		"a replayed release": {func() release { return signedRelease(t, priv, "0.7.0", script("0.7.0")) }, "signature doesn't verify"},
		"a bad signature": {func() release {
			r := good()
			r.files["SHA256SUMS.sig"] = bytes.Repeat([]byte{1}, 64)
			return r
		}, "signature doesn't verify"},
		"no signature": {func() release {
			r := good()
			delete(r.files, "SHA256SUMS.sig")
			return r
		}, "has no SHA256SUMS.sig"},
		"a changed tarball": {func() release {
			r := good()
			r.files[asset] = tarball(t, map[string][]byte{"driveagent": []byte("evil")})
			return r
		}, "doesn't match its SHA256SUMS line"},
		"no build for this platform": {func() release {
			r := good()
			sums := []byte(strings.Repeat("0", 64) + "  driveagent_plan9_mips.tar.gz\n")
			r.files["SHA256SUMS"], r.files["SHA256SUMS.sig"] = sums, ed25519.Sign(priv, Message("0.7.1", sums))
			return r
		}, "has no build for"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			src := serve(t, map[string]release{"0.7.1": c.release()})
			src.Keys = []ed25519.PublicKey{pub}
			_, err := src.Fetch(context.Background(), "0.7.1")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want one saying %q", err, c.want)
			}
		})
	}
	var noAsset *NoAssetError
	src := serve(t, map[string]release{"0.7.1": cases["no build for this platform"].release()})
	src.Keys = []ed25519.PublicKey{pub}
	if _, err := src.Fetch(context.Background(), "0.7.1"); !errors.As(err, &noAsset) {
		t.Errorf("no build: err = %v, want a NoAssetError", err)
	}
}

func TestExtract(t *testing.T) {
	for name, c := range map[string]struct {
		files map[string][]byte
		want  string
	}{
		"./driveagent":   {map[string][]byte{"./driveagent": []byte("x")}, ""},
		"no driveagent":  {map[string][]byte{"README.md": []byte("x")}, "holds no driveagent"},
		"nested":         {map[string][]byte{"bin/driveagent": []byte("x")}, "holds no driveagent"},
		"a path outside": {map[string][]byte{"../driveagent": []byte("x")}, "path outside"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := extract(tarball(t, c.files))
			if (c.want == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.want)) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
	// Not a regular file.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "driveagent", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"})
	tw.Close()
	gz.Close()
	if _, err := extract(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Errorf("a symlink: err = %v", err)
	}
}

func TestFetchLimitsSizes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(bytes.Repeat([]byte("x"), maxSmall+1))
	}))
	defer srv.Close()
	src := Source{BaseURL: srv.URL, Client: srv.Client()}
	if _, err := src.Fetch(context.Background(), "0.7.1"); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("an oversize SHA256SUMS: err = %v", err)
	}
}

// installed sets up a "current" driveagent in a temp dir.
func installed(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "driveagent")
	if err := os.WriteFile(exe, script("0.7.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestInstall(t *testing.T) {
	exe := installed(t)
	if err := Install(context.Background(), exe, script("0.7.1"), "0.7.1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); !bytes.Equal(b, script("0.7.1")) {
		t.Errorf("driveagent is %q", b)
	}
	if b, _ := os.ReadFile(exe + ".prev"); !bytes.Equal(b, script("0.7.0")) {
		t.Errorf("driveagent.prev is %q", b)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode())
	}
	assertNoTemps(t, exe)
}

func TestInstallThroughASymlink(t *testing.T) {
	exe := installed(t)
	link := filepath.Join(t.TempDir(), "driveagent")
	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil || resolved != exe {
		t.Fatalf("resolved %q, %v", resolved, err)
	}
	if err := Install(context.Background(), resolved, script("0.7.1"), "0.7.1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(link); !bytes.Equal(b, script("0.7.1")) {
		t.Errorf("through the symlink: %q", b)
	}
}

func TestInstallRefuses(t *testing.T) {
	t.Run("the wrong version", func(t *testing.T) {
		exe := installed(t)
		err := Install(context.Background(), exe, script("0.6.9"), "0.7.1")
		if err == nil || !strings.Contains(err.Error(), "says it's 0.6.9, not 0.7.1") {
			t.Errorf("err = %v", err)
		}
		if b, _ := os.ReadFile(exe); !bytes.Equal(b, script("0.7.0")) {
			t.Errorf("driveagent changed: %q", b)
		}
		assertNoTemps(t, exe)
	})
	t.Run("a binary that doesn't run", func(t *testing.T) {
		exe := installed(t)
		if err := Install(context.Background(), exe, []byte("\x7fELFnope"), "0.7.1"); err == nil ||
			!strings.Contains(err.Error(), "doesn't run") {
			t.Errorf("err = %v", err)
		}
		assertNoTemps(t, exe)
	})
	t.Run("no room for .prev", func(t *testing.T) {
		exe := installed(t)
		// A directory where .prev would go can't be replaced by a file.
		if err := os.Mkdir(exe+".prev", 0o755); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(exe+".prev", "x"), nil, 0o644)
		err := Install(context.Background(), exe, script("0.7.1"), "0.7.1")
		if err == nil || !strings.Contains(err.Error(), ".prev") {
			t.Errorf("err = %v", err)
		}
		if b, _ := os.ReadFile(exe); !bytes.Equal(b, script("0.7.0")) {
			t.Errorf("driveagent changed without a .prev: %q", b)
		}
	})
	t.Run("an unwritable directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes anywhere")
		}
		exe := installed(t)
		dir := filepath.Dir(exe)
		os.Chmod(dir, 0o555)
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		var nw *NotWritableError
		if err := CheckWritable(exe); !errors.As(err, &nw) || nw.Dir != dir {
			t.Errorf("CheckWritable = %v", err)
		}
		if err := Install(context.Background(), exe, script("0.7.1"), "0.7.1"); !errors.As(err, &nw) {
			t.Errorf("Install = %v", err)
		}
	})
}

func assertNoTemps(t *testing.T, exe string) {
	t.Helper()
	entries, _ := os.ReadDir(filepath.Dir(exe))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".driveagent-") {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.6.1", "0.7.0", true}, {"0.7.0", "0.7.0", false}, {"0.7.0", "0.6.9", false},
		{"0.9.0", "0.10.0", true}, {"1.0.0", "0.99.99", false}, {"0.7.0", "0.7", false},
		{"0.7.0", "v0.8.0", false}, {"0.7.0", "0.08.0", false}, {"bad", "0.7.0", false},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestFailureRecord(t *testing.T) {
	dir := t.TempDir()
	if f := LoadFailure(dir); f.Blocks("0.7.1", time.Now()) {
		t.Error("no record blocks")
	}
	if err := RecordFailure(dir, "0.7.1", fmt.Errorf("boom")); err != nil {
		t.Fatal(err)
	}
	f := LoadFailure(dir)
	if !f.Blocks("0.7.1", time.Now()) || f.Error != "boom" {
		t.Errorf("record %+v doesn't block 0.7.1", f)
	}
	if f.Blocks("0.7.2", time.Now()) {
		t.Error("a newer version is blocked")
	}
	if f.Blocks("0.7.1", time.Now().Add(RetryAfter)) {
		t.Error("still blocked after an hour")
	}
	ClearFailure(dir)
	if LoadFailure(dir).Version != "" {
		t.Error("cleared, but still there")
	}
}
