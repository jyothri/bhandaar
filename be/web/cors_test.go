package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/notification"
)

// The progress stream must echo each -frontend_url origin, not the raw flag
// value, which is invalid as a header once it holds a list.
func TestScanProgressAllowsEachFrontendOrigin(t *testing.T) {
	original := constants.FrontendUrl
	constants.FrontendUrl = "https://sm.example.com,http://192.168.1.118:5173"
	t.Cleanup(func() { constants.FrontendUrl = original })

	r := mux.NewRouter()
	sse(r)
	server := httptest.NewServer(withCORS(r))
	t.Cleanup(server.Close)

	cases := map[string]string{
		"https://sm.example.com":    "https://sm.example.com",
		"http://192.168.1.118:5173": "http://192.168.1.118:5173",
		"https://evil.example.com":  "",
	}
	for origin, want := range cases {
		if got := streamAllowOrigin(t, server.URL, origin); got != want {
			t.Errorf("Origin %s: Access-Control-Allow-Origin = %q, want %q", origin, got, want)
		}
	}
}

// streamAllowOrigin opens the progress stream and returns its
// Access-Control-Allow-Origin. The stream sends headers with its first
// event, so this publishes progress until the response arrives.
func streamAllowOrigin(t *testing.T, baseURL, origin string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/sse/scanprogress", nil)
	req.Header.Set("Origin", origin)

	// The goroutine owns the publisher and closes it when told to stop.
	done, finished := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		publisher := notification.GetPublisher("cors-test")
		defer close(publisher)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				select {
				case publisher <- notification.Progress{ScanId: 1}:
				case <-done:
					return
				}
			}
		}
	}()
	defer func() {
		close(done)
		<-finished
	}()

	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /sse/scanprogress: %v", err)
	}
	defer res.Body.Close()
	return res.Header.Get("Access-Control-Allow-Origin")
}
