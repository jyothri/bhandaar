package remote

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// server is a TLS server whose certificate is valid for example.com (the
// httptest default), counting the HTTP requests it serves.
func server(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok","service":"agentserver"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// wrongCertServer serves a self-signed certificate for another name.
func wrongCertServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "wrong.test"}, DNSNames: []string{"wrong.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	var n atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1) }))
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, &n
}

// hangup accepts connections and closes them at once, counting them.
func hangup(t *testing.T) (string, *atomic.Int32) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	var n atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			c.Close()
		}
	}()
	return l.Addr().String(), &n
}

func refusedAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

// setup returns client options for https://example.com:<port>, where DNS
// "resolves" to srv. dnsCalls counts DNS dials.
func setup(t *testing.T, srv *httptest.Server, lanAddr string) (Options, *atomic.Int32) {
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	var dnsCalls atomic.Int32
	return Options{
		RemoteURL: "https://example.com:" + port,
		AgentID:   "7b0e5f0c-0000-4000-8000-000000000001",
		LANAddr:   lanAddr,
		TLS:       &tls.Config{RootCAs: pool},
		DialDNS: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dnsCalls.Add(1)
			if addr != "example.com:"+port {
				t.Errorf("DNS dial of %q, want example.com:%s", addr, port)
			}
			var d net.Dialer
			return d.DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}, &dnsCalls
}

func health(t *testing.T, o Options) *Client {
	t.Helper()
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	return c
}

func TestLANUsedWhenItWorks(t *testing.T) {
	srv, reqs := server(t)
	o, dns := setup(t, srv, srv.Listener.Addr().String())
	c := health(t, o)
	if r := c.Route(); r.Via != "LAN" || r.Addr != srv.Listener.Addr().String() {
		t.Errorf("route = %v", r)
	}
	if dns.Load() != 0 || reqs.Load() != 1 {
		t.Errorf("dns=%d requests=%d", dns.Load(), reqs.Load())
	}
}

func TestNoLANAddrUsesDNS(t *testing.T) {
	srv, _ := server(t)
	o, dns := setup(t, srv, "")
	c := health(t, o)
	if c.Route().Via != "DNS" || dns.Load() != 1 {
		t.Errorf("route = %v, dns = %d", c.Route(), dns.Load())
	}
}

func TestLANRefusedFallsBackToDNS(t *testing.T) {
	srv, _ := server(t)
	o, dns := setup(t, srv, refusedAddr(t))
	c := health(t, o)
	if c.Route().Via != "DNS" || dns.Load() != 1 {
		t.Errorf("route = %v, dns = %d", c.Route(), dns.Load())
	}
}

// Some other host at the LAN address, on another network: its certificate
// doesn't verify, so the request goes through DNS and nothing reaches it.
func TestLANWithWrongCertificateFallsBackToDNS(t *testing.T) {
	srv, reqs := server(t)
	wrong, wrongReqs := wrongCertServer(t)
	o, _ := setup(t, srv, wrong.Listener.Addr().String())
	c := health(t, o)
	if c.Route().Via != "DNS" {
		t.Errorf("route = %v", c.Route())
	}
	if wrongReqs.Load() != 0 {
		t.Errorf("the wrong server received %d requests", wrongReqs.Load())
	}
	if reqs.Load() != 1 {
		t.Errorf("the real server received %d requests", reqs.Load())
	}
}

func TestLANSkippedForFiveMinutesAfterFailure(t *testing.T) {
	srv, _ := server(t)
	lan, accepts := hangup(t)
	o, dns := setup(t, srv, lan)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	d := &Dialer{LANAddr: lan, TLS: o.TLS, DialDNS: o.DialDNS, Now: func() time.Time { mu.Lock(); defer mu.Unlock(); return now }}
	addr := "example.com:" + o.RemoteURL[len("https://example.com:"):]
	dial := func() {
		t.Helper()
		conn, err := d.DialTLSContext(context.Background(), "tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		conn.Close()
	}
	advance := func(dur time.Duration) { mu.Lock(); now = now.Add(dur); mu.Unlock() }

	dial() // LAN handshake fails -> DNS
	if accepts.Load() != 1 || dns.Load() != 1 || d.LastRoute().Via != "DNS" {
		t.Fatalf("first dial: lan=%d dns=%d route=%v", accepts.Load(), dns.Load(), d.LastRoute())
	}
	advance(4 * time.Minute)
	dial() // within 5 minutes: LAN not tried
	if accepts.Load() != 1 || dns.Load() != 2 {
		t.Errorf("within the skip window: lan=%d dns=%d", accepts.Load(), dns.Load())
	}
	advance(time.Minute + time.Second)
	dial() // after it: LAN tried again
	if accepts.Load() != 2 {
		t.Errorf("after the window: lan=%d", accepts.Load())
	}
}

func TestHTTPOnlyForLocalhost(t *testing.T) {
	for _, u := range []string{"http://sm.jkurapati.com", "http://192.168.1.118:8091"} {
		if _, err := New(Options{RemoteURL: u}); err == nil {
			t.Errorf("%s: want an error", u)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	c, err := New(Options{RemoteURL: "http://127.0.0.1:" + port})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := c.Route(); r.Via != "direct" || r.Addr != "127.0.0.1:"+strconv.Itoa(srv.Listener.Addr().(*net.TCPAddr).Port) {
		t.Errorf("route = %v", r)
	}
}
