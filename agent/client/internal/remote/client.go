// Package remote is driveagent's HTTP client for agentserver.
package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/config"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
	"github.com/jyothri/bhandaar/agent/wire"
)

// maxResponse caps how much of a response body is read.
const maxResponse = 1 << 20

// Options configures a Client.
type Options struct {
	RemoteURL string // checked with config.NormalizeRemoteURL
	AgentID   string
	LANAddr   string
	// TLS and DialDNS are for tests; see Dialer.
	TLS     *tls.Config
	DialDNS func(ctx context.Context, network, addr string) (net.Conn, error)
	// Timeout bounds each request; 0 means 60 s.
	Timeout time.Duration
}

// Client talks to agentserver. It is safe for concurrent use.
type Client struct {
	base     string
	agentID  string
	http     *http.Client
	dialer   *Dialer
	protocol int // negotiated by Handshake; 0 before
}

// New builds a client. It refuses plain http except to localhost.
func New(o Options) (*Client, error) {
	base, err := config.NormalizeRemoteURL(o.RemoteURL)
	if err != nil {
		return nil, err
	}
	d := &Dialer{LANAddr: o.LANAddr, TLS: o.TLS, DialDNS: o.DialDNS}
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		base:    base,
		agentID: o.AgentID,
		dialer:  d,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext:           d.DialContext,
				DialTLSContext:        d.DialTLSContext,
				MaxIdleConnsPerHost:   4,
				IdleConnTimeout:       90 * time.Second,
				ResponseHeaderTimeout: timeout,
			},
			// agentserver never redirects; a redirect means something else
			// (a captive portal, a misrouted proxy) answered.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// BaseURL is the normalized remote URL.
func (c *Client) BaseURL() string { return c.base }

// Route is how the last connection reached the server.
func (c *Client) Route() Route { return c.dialer.LastRoute() }

// Protocol is the negotiated protocol (0 before a successful handshake).
func (c *Client) Protocol() int { return c.protocol }

// Health calls GET /agent/health. Anything but 200 with status "ok" is
// ErrTransient.
func (c *Client) Health(ctx context.Context) (wire.HealthResponse, error) {
	var h wire.HealthResponse
	err := c.do(ctx, http.MethodGet, "/agent/health", nil, &h, "")
	if err == nil && h.Status != wire.HealthOK {
		err = &Error{Kind: ErrTransient, Status: http.StatusOK, Message: "server reports status " + strconv.Quote(h.Status)}
	}
	var e *Error
	if errors.As(err, &e) && e.Status == http.StatusServiceUnavailable && e.Code == "" {
		e.Message = "server reports it is unavailable (database)"
	}
	return h, err
}

// Handshake negotiates the protocol. An upgrade_required or
// unsupported_protocol decision returns the response together with an
// ErrUpgrade error carrying the server's message.
func (c *Client) Handshake(ctx context.Context) (wire.HandshakeResponse, error) {
	req := wire.HandshakeRequest{
		AgentVersion: version.Version, Protocols: version.Protocols, OS: runtime.GOOS, Arch: runtime.GOARCH,
	}
	var resp wire.HandshakeResponse
	if err := c.do(ctx, http.MethodPost, "/agent/v1/handshake", req, &resp, ""); err != nil {
		return resp, err
	}
	switch resp.Decision {
	case wire.DecisionOK, wire.DecisionUpgradeRecommended:
		c.protocol = resp.Protocol
		return resp, nil
	case wire.DecisionUpgradeRequired, wire.DecisionUnsupportedProtocol:
		msg := resp.Message
		if resp.DownloadURL != "" {
			msg += " (download: " + resp.DownloadURL + ")"
		}
		// The decision is in resp; the message is what the user needs.
		return resp, &Error{Kind: ErrUpgrade, Message: msg}
	default:
		return resp, &Error{Kind: ErrPermanent, Status: http.StatusOK, Message: "unknown handshake decision " + strconv.Quote(resp.Decision)}
	}
}

// Login exchanges a username and password for tokens.
func (c *Client) Login(ctx context.Context, username, password, hostname string) (wire.TokenResponse, error) {
	req := wire.LoginRequest{
		Username: username, Password: password, AgentID: c.agentID,
		Hostname: hostname, OS: runtime.GOOS, Arch: runtime.GOARCH,
	}
	var tr wire.TokenResponse
	err := c.do(ctx, http.MethodPost, "/agent/v1/auth/login", req, &tr, "")
	return tr, err
}

// Refresh rotates a refresh token.
func (c *Client) Refresh(ctx context.Context, refreshToken string) (wire.TokenResponse, error) {
	var tr wire.TokenResponse
	err := c.do(ctx, http.MethodPost, "/agent/v1/auth/refresh", wire.RefreshRequest{RefreshToken: refreshToken}, &tr, "")
	return tr, err
}

// Logout revokes the refresh token's family on the server.
func (c *Client) Logout(ctx context.Context, refreshToken string) error {
	return c.do(ctx, http.MethodPost, "/agent/v1/auth/logout", wire.RefreshRequest{RefreshToken: refreshToken}, nil, "")
}

// do sends one request. in (if not nil) is sent as JSON; a 2xx JSON body is
// decoded into out (if not nil).
func (c *Client) do(ctx context.Context, method, path string, in, out any, accessToken string) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "driveagent/"+version.Version)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set(wire.HeaderAgentVersion, version.Version)
	if c.agentID != "" {
		req.Header.Set(wire.HeaderAgentID, c.agentID)
	}
	if c.protocol > 0 {
		req.Header.Set(wire.HeaderAgentProtocol, strconv.Itoa(c.protocol))
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err() // the caller gave up (Ctrl-C); not the server's fault
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &Error{Kind: ErrTransient, Message: "timed out", Err: ctx.Err()}
		}
		return &Error{Kind: ErrTransient, Err: err}
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return &Error{Kind: ErrTransient, Status: resp.StatusCode, Err: err}
	}
	if resp.StatusCode == http.StatusServiceUnavailable && path == "/agent/health" {
		// Health's 503 has a JSON body, but not an error body.
		return &Error{Kind: ErrTransient, Status: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return classify(resp, b)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(b, out); err != nil {
		// A 200 that isn't our JSON: a captive portal or a misrouted proxy.
		return &Error{Kind: ErrTransient, Status: resp.StatusCode, Message: fmt.Sprintf("unexpected response body: %v", err)}
	}
	return nil
}
