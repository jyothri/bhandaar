package collect

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

type fakeOpts struct {
	messages       int                 // message IDs m0..m(n-1) on the first page
	fetchDelay     time.Duration       // delay before each message fetch responds
	failSecondPage bool                // fail the next page's list call with a 400
	missing        map[string]struct{} // message IDs whose fetch returns 404
}

// fakeGmail serves a fake Gmail API for startGmailScan.
func fakeGmail(t *testing.T, o fakeOpts) (*gmail.Service, *atomic.Int32) {
	t.Helper()
	var fetched atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") != "" {
			http.Error(w, `{"error":{"code":400,"message":"bad page"}}`, http.StatusBadRequest)
			return
		}
		list := gmail.ListMessagesResponse{}
		if o.failSecondPage {
			list.NextPageToken = "page-2"
		}
		for i := 0; i < o.messages; i++ {
			list.Messages = append(list.Messages, &gmail.Message{Id: fmt.Sprintf("m%d", i)})
		}
		json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/gmail/v1/users/me/messages/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(o.fetchDelay)
		fetched.Add(1)
		id := r.URL.Path[len("/gmail/v1/users/me/messages/"):]
		if _, ok := o.missing[id]; ok {
			http.Error(w, `{"error":{"code":404,"message":"not found"}}`, http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(gmail.Message{Id: id, Payload: &gmail.MessagePart{}})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	svc, err := gmail.NewService(context.Background(),
		option.WithEndpoint(server.URL+"/"), option.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	return svc, &fetched
}

// Regression test for review item 7.8: a failed list call used to return
// while fetches were in flight; the caller then closed messageMetaData and
// the next fetch panicked with "send on closed channel", killing the server.
func TestStartGmailScanWaitsForFetchesWhenListingFails(t *testing.T) {
	const messages = 5
	svc, fetched := fakeGmail(t, fakeOpts{messages: messages, fetchDelay: 200 * time.Millisecond, failSecondPage: true})

	messageMetaData := make(chan db.MessageMetadata, 10)
	received := make(chan int)
	go func() {
		n := 0
		for range messageMetaData {
			n++
		}
		received <- n
	}()

	// Mirrors Gmail(): close the channel as soon as startGmailScan returns.
	err := startGmailScan(svc, 1, GMailScan{Filter: "in:inbox", ClientKey: "test-7.8"}, messageMetaData)
	close(messageMetaData)

	if err == nil {
		t.Fatal("expected an error from the failing second page")
	}
	if got := fetched.Load(); got != messages {
		t.Errorf("startGmailScan returned with %d of %d fetches done", got, messages)
	}
	select {
	case n := <-received:
		if n != messages {
			t.Errorf("received %d messages, want %d", n, messages)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("consumer did not finish")
	}
}

// Review follow-up: a message skipped after a permanent fetch error used to
// leave counter_pending incremented, so the scan's final progress event
// reported ActiveCount > 0.
func TestSkippedMessagesAreNotLeftActive(t *testing.T) {
	const clientKey = "test-skipped"
	svc, _ := fakeGmail(t, fakeOpts{messages: 3, missing: map[string]struct{}{"m1": {}}})

	events, unsubscribe := notification.Subscribe(clientKey)
	defer unsubscribe()

	messageMetaData := make(chan db.MessageMetadata, 10)
	go func() {
		for range messageMetaData {
		}
	}()
	if err := startGmailScan(svc, 1, GMailScan{Filter: "in:inbox", ClientKey: clientKey}, messageMetaData); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	close(messageMetaData)

	// The hub closes clientKey's subscribers after the scan's final event.
	var last notification.Progress
	deadline := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case p, ok := <-events:
			if !ok {
				done = true
				break
			}
			last = p
		case <-deadline:
			t.Fatal("timed out waiting for the final progress event")
		}
	}
	if last.ProcessedCount != 2 || last.ActiveCount != 0 {
		t.Fatalf("final progress: processed=%d active=%d, want processed=2 active=0",
			last.ProcessedCount, last.ActiveCount)
	}
}
