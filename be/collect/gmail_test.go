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
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// fakeGmail serves one page of message IDs, then fails the next page's list
// call with a non-retryable error. Message fetches are slow, so they are
// still in flight when listing fails.
func fakeGmail(t *testing.T, messages int, fetchDelay time.Duration) (*gmail.Service, *atomic.Int32) {
	t.Helper()
	var fetched atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("pageToken") != "" {
			http.Error(w, `{"error":{"code":400,"message":"bad page"}}`, http.StatusBadRequest)
			return
		}
		list := gmail.ListMessagesResponse{NextPageToken: "page-2"}
		for i := 0; i < messages; i++ {
			list.Messages = append(list.Messages, &gmail.Message{Id: fmt.Sprintf("m%d", i)})
		}
		json.NewEncoder(w).Encode(list)
	})
	mux.HandleFunc("/gmail/v1/users/me/messages/", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(fetchDelay)
		fetched.Add(1)
		json.NewEncoder(w).Encode(gmail.Message{
			Id:      r.URL.Path[len("/gmail/v1/users/me/messages/"):],
			Payload: &gmail.MessagePart{},
		})
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
	svc, fetched := fakeGmail(t, messages, 200*time.Millisecond)

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
