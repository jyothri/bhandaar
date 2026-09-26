package remote

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"
)

// LAN-first dialing (docs/specs/remote-sync-agent.md, "Reaching the server
// from the LAN"): from inside the home LAN the public name only works through
// the gateway's unreliable NAT hairpin, so the agent can be given the
// server's LAN address to try first. The TLS handshake still uses the
// remote URL's host name and verifies the certificate as usual, so a
// different host at that address on some other network gets nothing.
const (
	lanConnectTimeout   = time.Second
	lanHandshakeTimeout = 5 * time.Second
	lanRetryAfter       = 5 * time.Minute
)

// Route says how the last connection reached the server.
type Route struct {
	Via  string // "LAN", "DNS", or "direct" for a plain-http local server
	Addr string // the peer address
}

func (r Route) String() string {
	if r.Via == "" {
		return "not connected"
	}
	return "via " + r.Via + " " + r.Addr
}

// Dialer opens connections for the HTTP transport.
type Dialer struct {
	// LANAddr, if set, is tried first for every new TLS connection.
	LANAddr string
	// TLS is the base TLS config; nil means the system roots.
	TLS *tls.Config
	// DialDNS dials normally (through DNS). Tests override it.
	DialDNS func(ctx context.Context, network, addr string) (net.Conn, error)
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu           sync.Mutex
	skipLANUntil time.Time
	last         Route
}

func (d *Dialer) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Dialer) dialDNS(ctx context.Context, network, addr string) (net.Conn, error) {
	if d.DialDNS != nil {
		return d.DialDNS(ctx, network, addr)
	}
	var nd net.Dialer
	return nd.DialContext(ctx, network, addr)
}

// LastRoute is the route of the most recent connection.
func (d *Dialer) LastRoute() Route {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.last
}

func (d *Dialer) record(r Route) {
	d.mu.Lock()
	d.last = r
	d.mu.Unlock()
}

func (d *Dialer) tryLAN() bool {
	if d.LANAddr == "" {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.now().Before(d.skipLANUntil)
}

// DialContext is for plain-http (localhost) servers.
func (d *Dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := d.dialDNS(ctx, network, addr)
	if err == nil {
		d.record(Route{Via: "direct", Addr: conn.RemoteAddr().String()})
	}
	return conn, err
}

// DialTLSContext connects to addr (host:port of the remote URL): through
// LANAddr first when set, falling back to DNS on any failure.
func (d *Dialer) DialTLSContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{}
	if d.TLS != nil {
		cfg = d.TLS.Clone()
	}
	cfg.ServerName = host

	if d.tryLAN() {
		conn, err := d.handshake(ctx, cfg, func(ctx context.Context) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, lanConnectTimeout)
			defer cancel()
			var nd net.Dialer
			return nd.DialContext(ctx, network, d.LANAddr)
		}, lanHandshakeTimeout)
		if err == nil {
			d.record(Route{Via: "LAN", Addr: conn.RemoteAddr().String()})
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		d.mu.Lock()
		d.skipLANUntil = d.now().Add(lanRetryAfter)
		d.mu.Unlock()
	}

	conn, err := d.handshake(ctx, cfg, func(ctx context.Context) (net.Conn, error) {
		return d.dialDNS(ctx, network, addr)
	}, 0)
	if err != nil {
		return nil, err
	}
	d.record(Route{Via: "DNS", Addr: conn.RemoteAddr().String()})
	return conn, nil
}

// handshake dials and completes a verified TLS handshake, within timeout
// if it's not zero.
func (d *Dialer) handshake(ctx context.Context, cfg *tls.Config, dial func(context.Context) (net.Conn, error),
	timeout time.Duration) (net.Conn, error) {
	raw, err := dial(ctx)
	if err != nil {
		return nil, err
	}
	hctx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		hctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	conn := tls.Client(raw, cfg)
	if err := conn.HandshakeContext(hctx); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}
