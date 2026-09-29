package collect

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jyothri/hdd/notification"
	"golang.org/x/oauth2"
)

// fakeItem is a picked item the fake Picker API serves, and how its bytes'
// URL answers: head is HEAD's status (0 for 200), noLength leaves out
// Content-Length, and failFirst answers 503 that many times first.
type fakeItem struct {
	id        string
	video     bool
	notReady  bool
	body      string
	head      int
	noLength  bool
	failFirst int
}

// fakePicker serves the Picker API, and the items' bytes, for one session.
type fakePicker struct {
	t         *testing.T
	server    *httptest.Server
	items     []fakeItem
	pickAfter int32 // polls before mediaItemsSet
	polls     atomic.Int32
	lists     atomic.Int32
	deletes   atomic.Int32
	mu        sync.Mutex
	requests  map[string]int // "METHOD id" → count
}

func newFakePicker(t *testing.T, items ...fakeItem) *fakePicker {
	t.Helper()
	f := &fakePicker{t: t, items: items, requests: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "picker-1" {
			http.Error(w, `{"error": "not found"}`, http.StatusNotFound)
			return
		}
		set := f.polls.Add(1) > f.pickAfter
		json.NewEncoder(w).Encode(map[string]any{"id": "picker-1", "mediaItemsSet": set,
			"pollingConfig": map[string]string{"pollInterval": "0.001s"}})
	})
	mux.HandleFunc("DELETE /v1/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.deletes.Add(1)
		w.Write([]byte("{}"))
	})
	// Two items a page.
	mux.HandleFunc("GET /v1/mediaItems", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("sessionId") != "picker-1" {
			http.Error(w, "wrong session", http.StatusBadRequest)
			return
		}
		f.lists.Add(1)
		start, _ := strconv.Atoi(r.URL.Query().Get("pageToken"))
		end := min(start+2, len(f.items))
		page := map[string]any{"mediaItems": []map[string]any{}}
		for _, it := range f.items[start:end] {
			page["mediaItems"] = append(page["mediaItems"].([]map[string]any), f.item(it))
		}
		if end < len(f.items) {
			page["nextPageToken"] = strconv.Itoa(end)
		}
		json.NewEncoder(w).Encode(page)
	})
	mux.HandleFunc("/bytes/{id}", f.serveBytes)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)

	originalApi, originalPoll, originalBackoff := photosPickerApi, minPollInterval, photosBackoff
	photosPickerApi, minPollInterval, photosBackoff = f.server.URL+"/", time.Millisecond, time.Millisecond
	t.Cleanup(func() { photosPickerApi, minPollInterval, photosBackoff = originalApi, originalPoll, originalBackoff })
	return f
}

// client authorizes as the fake expects.
func (f *fakePicker) client() *http.Client {
	return oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"}))
}

func (f *fakePicker) item(it fakeItem) map[string]any {
	meta := map[string]any{"width": 4000, "height": 3000, "cameraMake": "Google", "cameraModel": "Pixel 8 Pro",
		"photoMetadata": map[string]any{"focalLength": 6.9, "apertureFNumber": 1.68, "isoEquivalent": 25,
			"exposureTime": "0.0005s"}}
	kind, mime := "PHOTO", "image/jpeg"
	if it.video {
		status := "READY"
		if it.notReady {
			status = "PROCESSING"
		}
		kind, mime = "VIDEO", "video/mp4"
		meta = map[string]any{"width": 1920, "height": 1080, "videoMetadata": map[string]any{"fps": 30, "processingStatus": status}}
	}
	return map[string]any{"id": it.id, "createTime": "2024-12-12T14:20:11.303Z", "type": kind,
		"mediaFile": map[string]any{"baseUrl": f.server.URL + "/bytes/" + it.id, "mimeType": mime,
			"filename": it.id + ".jpg", "mediaFileMetadata": meta}}
}

// serveBytes answers an item's bytes' URL ("<id>=d" or "<id>=dv").
func (f *fakePicker) serveBytes(w http.ResponseWriter, r *http.Request) {
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		f.t.Errorf("%s %s: Authorization = %q", r.Method, r.URL.Path, got)
	}
	id, suffix, _ := strings.Cut(r.PathValue("id"), "=")
	f.mu.Lock()
	f.requests[r.Method+" "+id]++
	count := f.requests[r.Method+" "+id]
	f.mu.Unlock()
	for _, it := range f.items {
		if it.id != id {
			continue
		}
		if want := map[bool]string{false: "d", true: "dv"}[it.video]; suffix != want {
			f.t.Errorf("%s asked for =%s, want =%s", id, suffix, want)
		}
		if count <= it.failFirst {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.Method == http.MethodHead && it.head != 0 {
			w.WriteHeader(it.head)
			return
		}
		if r.Method == http.MethodHead && it.noLength {
			// Chunked: no Content-Length.
			w.Header().Set("Transfer-Encoding", "chunked")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(it.body)))
		w.Write([]byte(it.body))
		return
	}
	http.NotFound(w, r)
}

func (f *fakePicker) count(method, id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[method+" "+id]
}

func TestWaitForPickPollsUntilPicked(t *testing.T) {
	f := newFakePicker(t)
	f.pickAfter = 3
	picked, err := waitForPick(context.Background(), f.client(), "picker-1", time.Millisecond)
	if !picked || err != nil {
		t.Fatalf("waitForPick = %v, %v; want picked", picked, err)
	}
	if polls := f.polls.Load(); polls != 4 {
		t.Errorf("polled %d times, want 4", polls)
	}
}

func TestWaitForPickStopsAtTheDeadline(t *testing.T) {
	f := newFakePicker(t)
	f.pickAfter = 1 << 30
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if picked, err := waitForPick(ctx, f.client(), "picker-1", time.Millisecond); picked || err == nil {
		t.Errorf("waitForPick = %v, %v; want not picked, with the deadline", picked, err)
	}
}

func TestWaitForPickStopsWhenTheSessionIsGone(t *testing.T) {
	f := newFakePicker(t)
	picked, err := waitForPick(context.Background(), f.client(), "deleted", time.Millisecond)
	if picked || err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("waitForPick = %v, %v; want not picked, with a 404", picked, err)
	}
}

func md5Of(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestCollectPickedSizesEachItem(t *testing.T) {
	f := newFakePicker(t,
		fakeItem{id: "photo-head", body: "12345"},
		fakeItem{id: "photo-chunked", body: "abcdefgh", noLength: true},
		fakeItem{id: "photo-no-head", body: "xyz", head: http.StatusMethodNotAllowed},
		fakeItem{id: "photo-forbidden", body: "x", head: http.StatusForbidden},
		fakeItem{id: "photo-flaky", body: "1234567", failFirst: 2},
		fakeItem{id: "video-ready", video: true, body: "0123456789"},
		fakeItem{id: "video-processing", video: true, notReady: true, body: "0123"},
	)
	items, err := collectPicked(f.client(), "picker-1", 1, "photos-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		size := "nil"
		if it.Size != nil {
			size = fmt.Sprint(*it.Size)
		}
		got[it.MediaItemId] = fmt.Sprintf("%s %s %s %s", it.MediaType, size, it.SizeSource, it.Md5Hash)
	}
	want := map[string]string{
		"photo-head":       "PHOTO 5 head ",
		"photo-chunked":    "PHOTO 8 download " + md5Of("abcdefgh"),
		"photo-no-head":    "PHOTO 3 download " + md5Of("xyz"),
		"photo-forbidden":  "PHOTO nil unavailable ",
		"photo-flaky":      "PHOTO 7 head ",
		"video-ready":      "VIDEO 10 head ",
		"video-processing": "VIDEO nil unavailable ",
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s = %q, want %q", id, got[id], w)
		}
	}
	if len(items) != len(want) {
		t.Errorf("%d items, want %d", len(items), len(want))
	}
	if n := f.count("HEAD", "photo-forbidden"); n != 1 {
		t.Errorf("HEAD photo-forbidden %d times, want 1 (no retry)", n)
	}
	if n := f.count("HEAD", "photo-flaky"); n != 3 {
		t.Errorf("HEAD photo-flaky %d times, want 3 (two retries)", n)
	}
	if n := f.count("HEAD", "video-processing") + f.count("GET", "video-processing"); n != 0 {
		t.Errorf("fetched a video that isn't ready %d times", n)
	}
	if n := f.count("GET", "photo-head"); n != 0 {
		t.Errorf("downloaded an item HEAD sized")
	}
}

func TestPickedItemKeepsTheMetadata(t *testing.T) {
	f := newFakePicker(t, fakeItem{id: "p", body: "x"}, fakeItem{id: "v", video: true, body: "y"})
	items, err := collectPicked(f.client(), "picker-1", 1, "photos-"+t.Name())
	if err != nil {
		t.Fatal(err)
	}
	photo, video := items[0], items[1]
	if photo.Filename != "p.jpg" || photo.MimeType != "image/jpeg" || *photo.Width != 4000 || *photo.Height != 3000 ||
		photo.CameraModel != "Pixel 8 Pro" || *photo.FNumber != 1.68 || *photo.Iso != 25 ||
		photo.ExposureTime != "0.0005s" || photo.Fps != nil ||
		!photo.CreateTime.Equal(time.Date(2024, 12, 12, 14, 20, 11, 303e6, time.UTC)) {
		t.Errorf("photo = %+v", photo)
	}
	if video.MediaType != "VIDEO" || *video.Fps != 30 || video.Iso != nil || video.CameraMake != "" {
		t.Errorf("video = %+v", video)
	}
}

func TestCollectPickedListsAgainForFreshBaseUrls(t *testing.T) {
	f := newFakePicker(t, fakeItem{id: "a", body: "1"}, fakeItem{id: "b", body: "2"}, fakeItem{id: "c", body: "3"})
	original := baseUrlTTL
	baseUrlTTL = 0
	t.Cleanup(func() { baseUrlTTL = original })
	if _, err := collectPicked(f.client(), "picker-1", 1, "photos-"+t.Name()); err != nil {
		t.Fatal(err)
	}
	// Two pages to start with, then two for each of the three items.
	if lists := f.lists.Load(); lists != 8 {
		t.Errorf("listed %d pages, want 8", lists)
	}
}

func TestCollectPickedPublishesProgress(t *testing.T) {
	f := newFakePicker(t, fakeItem{id: "a", body: "1"}, fakeItem{id: "b", body: "22"}, fakeItem{id: "c", body: "333"})
	clientKey := "photos-" + t.Name()
	events, unsubscribe := notification.Subscribe(clientKey)
	defer unsubscribe()
	if _, err := collectPicked(f.client(), "picker-1", 5, clientKey); err != nil {
		t.Fatal(err)
	}
	select {
	case progress := <-events:
		if progress.ScanId != 5 || progress.ClientKey != clientKey || progress.ProcessedCount != 3 {
			t.Errorf("progress = %+v, want scan 5 of %s with 3 items", progress, clientKey)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no progress published")
	}
}

func TestCollectPickedFailsWhenListingFails(t *testing.T) {
	f := newFakePicker(t)
	if _, err := collectPicked(f.client(), "another-session", 1, "photos-"+t.Name()); err == nil {
		t.Error("collectPicked of a session it can't list: want an error")
	}
}
