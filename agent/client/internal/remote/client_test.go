package remote_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jyothri/bhandaar/agent/client/internal/remote"
	"github.com/jyothri/bhandaar/agent/client/internal/remote/remotetest"
	"github.com/jyothri/bhandaar/agent/client/internal/version"
	"github.com/jyothri/bhandaar/agent/wire"
)

const agentID = "7b0e5f0c-0000-4000-8000-000000000001"

func client(t *testing.T, url string) *remote.Client {
	t.Helper()
	c, err := remote.New(remote.Options{RemoteURL: url, AgentID: agentID})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

var ctx = context.Background()

func TestHeadersAndProtocol(t *testing.T) {
	srv := remotetest.New(t)
	c := client(t, srv.URL)
	if _, err := c.Health(ctx); err != nil {
		t.Fatal(err)
	}
	req, _ := srv.Last("/agent/health")
	if req.Header.Get(wire.HeaderAgentVersion) != version.Version || req.Header.Get(wire.HeaderAgentID) != agentID {
		t.Errorf("health headers = %v", req.Header)
	}
	if req.Header.Get(wire.HeaderAgentProtocol) != "" {
		t.Error("X-Agent-Protocol sent before the handshake")
	}
	hs, err := c.Handshake(ctx)
	if err != nil || hs.Decision != wire.DecisionOK || c.Protocol() != 1 {
		t.Fatalf("handshake = %+v, %v", hs, err)
	}
	if _, err := c.Login(ctx, "jyothri", "correct horse battery", "optiplex7070"); err != nil {
		t.Fatal(err)
	}
	req, _ = srv.Last("/agent/v1/auth/login")
	if req.Header.Get(wire.HeaderAgentProtocol) != "1" {
		t.Errorf("X-Agent-Protocol after the handshake = %q", req.Header.Get(wire.HeaderAgentProtocol))
	}
}

func TestHandshakeUpgrade(t *testing.T) {
	for _, d := range []string{wire.DecisionUpgradeRequired, wire.DecisionUnsupportedProtocol} {
		srv := remotetest.New(t)
		srv.Decision = d
		_, err := client(t, srv.URL).Handshake(ctx)
		if !errors.Is(err, remote.ErrUpgrade) {
			t.Errorf("%s: err = %v, want ErrUpgrade", d, err)
		}
	}
	srv := remotetest.New(t)
	srv.Decision = wire.DecisionUpgradeRecommended
	if hs, err := client(t, srv.URL).Handshake(ctx); err != nil || hs.Message == "" {
		t.Errorf("upgrade_recommended: %+v, %v", hs, err)
	}
}

func TestHealthDown(t *testing.T) {
	srv := remotetest.New(t)
	srv.Down = true
	if _, err := client(t, srv.URL).Health(ctx); !errors.Is(err, remote.ErrTransient) {
		t.Errorf("err = %v, want ErrTransient", err)
	}
}

// fake answers every request with one canned response, like nginx does for
// 413 and 502 (an HTML body).
func fake(t *testing.T, status int, contentType, body string, header ...string) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

const nginxHTML = "<html><head><title>%d</title></head><body><center><h1>Error</h1></center><hr><center>nginx</center></body></html>"

func jsonErr(code string) string {
	return `{"error":{"code":"` + code + `","message":"m","timestamp":"2026-09-26T00:00:00Z"}}`
}

func TestClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		ctype      string
		body       string
		header     []string
		kind       error
		code       string
		retryAfter time.Duration
	}{
		{"nginx 413", 413, "text/html", nginxHTML, nil, remote.ErrPermanent, "", 0},
		{"nginx 502", 502, "text/html", nginxHTML, nil, remote.ErrTransient, "", 0},
		{"nginx 504", 504, "text/html", nginxHTML, nil, remote.ErrTransient, "", 0},
		{"json 413", 413, "application/json", jsonErr(wire.CodePayloadTooLarge), nil, remote.ErrPermanent, wire.CodePayloadTooLarge, 0},
		{"400", 400, "application/json", jsonErr(wire.CodeInvalidBatch), nil, remote.ErrPermanent, wire.CodeInvalidBatch, 0},
		{"401 expired", 401, "application/json", jsonErr(wire.CodeTokenExpired), nil, remote.ErrAuth, wire.CodeTokenExpired, 0},
		{"401 reused", 401, "application/json", jsonErr(wire.CodeRefreshReused), nil, remote.ErrAuth, wire.CodeRefreshReused, 0},
		{"403", 403, "application/json", jsonErr(wire.CodeAgentMismatch), nil, remote.ErrPermanent, wire.CodeAgentMismatch, 0},
		{"404", 404, "application/json", jsonErr(wire.CodeDriveNotOpen), nil, remote.ErrPermanent, wire.CodeDriveNotOpen, 0},
		{"409", 409, "application/json", jsonErr(wire.CodeStreamMismatch), nil, remote.ErrPermanent, wire.CodeStreamMismatch, 0},
		{"422", 422, "application/json", jsonErr(wire.CodeIdempotencyKeyReused), nil, remote.ErrPermanent, wire.CodeIdempotencyKeyReused, 0},
		{"426", 426, "application/json", jsonErr(wire.CodeUpgradeRequired), nil, remote.ErrUpgrade, wire.CodeUpgradeRequired, 0},
		{"429", 429, "application/json", jsonErr(wire.CodeTooManyAttempts), []string{"Retry-After", "7"}, remote.ErrTransient, wire.CodeTooManyAttempts, 7 * time.Second},
		{"500", 500, "application/json", jsonErr(wire.CodeInternal), nil, remote.ErrTransient, wire.CodeInternal, 0},
		{"redirect", 302, "text/html", "", []string{"Location", "https://portal.example/"}, remote.ErrPermanent, "", 0},
		{"captive portal 200", 200, "text/html", "<html>sign in</html>", nil, remote.ErrTransient, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := client(t, fake(t, c.status, c.ctype, c.body, c.header...)).Refresh(ctx, "rt_x")
			var re *remote.Error
			if !errors.As(err, &re) {
				t.Fatalf("err = %v, want *remote.Error", err)
			}
			if !errors.Is(err, c.kind) || re.Code != c.code || re.Status != c.status || re.RetryAfter != c.retryAfter {
				t.Errorf("got kind=%v status=%d code=%q retry=%v; want %v %d %q %v", re.Kind, re.Status, re.Code, re.RetryAfter, c.kind, c.status, c.code, c.retryAfter)
			}
		})
	}
}

func TestRefreshable(t *testing.T) {
	for code, want := range map[string]bool{
		wire.CodeTokenExpired: true, wire.CodeInvalidToken: true, wire.CodeRefreshReused: false, wire.CodeInvalidCredentials: false,
	} {
		_, err := client(t, fake(t, 401, "application/json", jsonErr(code))).Refresh(ctx, "rt_x")
		if remote.Refreshable(err) != want {
			t.Errorf("%s: Refreshable = %v", code, !want)
		}
	}
}

func TestConnectionRefusedIsTransient(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if _, err := client(t, url).Health(ctx); !errors.Is(err, remote.ErrTransient) {
		t.Errorf("err = %v", err)
	}
}

func TestTimeoutIsTransient(t *testing.T) {
	stall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer stall.Close()
	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err := client(t, stall.URL).Health(tctx)
	if !errors.Is(err, remote.ErrTransient) || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want a transient timeout", err)
	}
}

func TestCancelledContext(t *testing.T) {
	srv := remotetest.New(t)
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client(t, srv.URL).Health(cctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
