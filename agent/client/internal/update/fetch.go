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
	"io"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"time"
)

// DefaultBaseURL is where releases are downloaded from; EnvURL overrides
// it (tests, a mirror). A release's files are under
// <base>/driveagent/v<V>/.
const (
	DefaultBaseURL = "https://github.com/jyothri/bhandaar/releases/download"
	EnvURL         = "DRIVEAGENT_UPDATE_URL"
)

// Limits on what's downloaded.
const (
	maxBinary   = 100 << 20 // the tarball, and the driveagent in it
	maxSmall    = 64 << 10  // SHA256SUMS and its signature
	fileTimeout = 2 * time.Minute
)

// Source is where releases come from, and what they're checked with.
type Source struct {
	BaseURL      string
	Client       *http.Client
	Keys         []ed25519.PublicKey
	GOOS, GOARCH string
}

// DefaultSource is GitHub (or $DRIVEAGENT_UPDATE_URL), through the
// system's proxy settings, checked with the built-in keys, for this
// platform.
func DefaultSource() Source {
	base := os.Getenv(EnvURL)
	if base == "" {
		base = DefaultBaseURL
	}
	return Source{
		BaseURL: strings.TrimRight(base, "/"),
		Client:  &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
		Keys:    Keys,
		GOOS:    runtime.GOOS, GOARCH: runtime.GOARCH,
	}
}

// AssetName is the release tarball for a platform.
func AssetName(goos, goarch string) string {
	return "driveagent_" + goos + "_" + goarch + ".tar.gz"
}

// ReleasePage is the release's page on GitHub, for people.
func ReleasePage(version string) string {
	return "https://github.com/jyothri/bhandaar/releases/tag/driveagent/v" + version
}

// NoAssetError is a release without a build for this platform.
type NoAssetError struct{ Version, GOOS, GOARCH string }

func (e *NoAssetError) Error() string {
	return fmt.Sprintf("driveagent %s has no build for %s/%s", e.Version, e.GOOS, e.GOARCH)
}

// Fetch downloads release version and returns its driveagent binary, once
// SHA256SUMS is signed for that version with one of s.Keys, the tarball
// matches it, and the tarball holds a driveagent (docs/specs/
// agent-auto-update.md, "Download and verify"). Nothing is run.
func (s Source) Fetch(ctx context.Context, version string) ([]byte, error) {
	sums, err := s.get(ctx, version, "SHA256SUMS", maxSmall)
	if err != nil {
		return nil, err
	}
	sig, err := s.get(ctx, version, "SHA256SUMS.sig", maxSmall)
	if err != nil {
		return nil, err
	}
	if err := VerifySums(s.Keys, version, sums, sig); err != nil {
		return nil, err
	}
	asset := AssetName(s.GOOS, s.GOARCH)
	want, ok := lookupSum(sums, asset)
	if !ok {
		return nil, &NoAssetError{Version: version, GOOS: s.GOOS, GOARCH: s.GOARCH}
	}
	tarball, err := s.get(ctx, version, asset, maxBinary)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(tarball)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s doesn't match its SHA256SUMS line", asset)
	}
	return extract(tarball)
}

// get fetches one file of a release, of at most max bytes.
func (s Source) get(ctx context.Context, version, name string, max int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, fileTimeout)
	defer cancel()
	url := s.BaseURL + "/driveagent/v" + version + "/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	client := s.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("driveagent %s has no %s", version, name)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", name, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", name, max)
	}
	return b, nil
}

// lookupSum finds name's SHA-256 in a sha256sum listing.
func lookupSum(sums []byte, name string) (string, bool) {
	for _, line := range strings.Split(string(sums), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// extract returns the driveagent in a release tarball: a regular file at
// its top level. A path leading out of the tarball fails it.
func extract(tarball []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, fmt.Errorf("reading the tarball: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("the tarball holds no driveagent")
		}
		if err != nil {
			return nil, fmt.Errorf("reading the tarball: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if path.IsAbs(h.Name) || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("the tarball has a path outside it: %q", h.Name)
		}
		if name != "driveagent" {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return nil, errors.New("the tarball's driveagent isn't a regular file")
		}
		if h.Size > maxBinary {
			return nil, fmt.Errorf("the tarball's driveagent is larger than %d bytes", maxBinary)
		}
		return io.ReadAll(io.LimitReader(tr, maxBinary))
	}
}
