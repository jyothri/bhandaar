package collect

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
)

// Google Photos, through the Picker API: the user picks items in Google
// Photos, and a scan records what they picked, with each item's size. See
// docs/archive/photos-picker.md, "Backend".

// photosPickerApi is the Picker API's base URL; tests point it at a fake.
var photosPickerApi = "https://photospicker.googleapis.com/"

// Built on first use, after main has parsed the OAuth flags. Only the
// token's own grant matters; Scopes is for the record.
var photosConfig = sync.OnceValue(func() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{"https://www.googleapis.com/auth/photospicker.mediaitems.readonly"},
	}
})

// photosClient returns a client that authorizes as refreshToken, for the
// Picker API and for baseUrls, which need the same bearer token. No
// overall timeout: each request sets its own. Tests replace it.
var photosClient = func(refreshToken string) *http.Client {
	return photosConfig().Client(context.Background(), &oauth2.Token{RefreshToken: refreshToken})
}

// Waits that tests shorten.
var (
	// The shortest wait between polls, whatever the session suggests.
	minPollInterval = time.Second
	// The first wait before a retry; it doubles each time.
	photosBackoff = time.Second
	// How long listed baseUrls are used before listing again (they work
	// for 60 minutes).
	baseUrlTTL = 50 * time.Minute
)

const (
	pickedPageSize = 100 // the most mediaItems.list returns
	sizeWorkers    = 4
	photosRetries  = 3
	requestTimeout = 10 * time.Minute // per request, so a large video can download
	apiTimeout     = 30 * time.Second
	maxPollErrors  = 10
	// When the session doesn't say how long the user has to pick.
	defaultPickTimeout = 30 * time.Minute
)

// ErrPickNotWaiting is returned when cancelling a pick that isn't waiting.
var ErrPickNotWaiting = errors.New("the Photos pick isn't waiting")

// pickingSession is the Picker API's PickingSession.
type pickingSession struct {
	Id            string `json:"id"`
	PickerUri     string `json:"pickerUri"`
	PollingConfig struct {
		PollInterval string `json:"pollInterval"`
		TimeoutIn    string `json:"timeoutIn"`
	} `json:"pollingConfig"`
	ExpireTime    time.Time `json:"expireTime"`
	MediaItemsSet bool      `json:"mediaItemsSet"`
}

// pickedMediaItem is the Picker API's PickedMediaItem.
type pickedMediaItem struct {
	Id         string     `json:"id"`
	CreateTime *time.Time `json:"createTime"`
	Type       string     `json:"type"` // PHOTO or VIDEO
	MediaFile  struct {
		BaseUrl           string `json:"baseUrl"`
		MimeType          string `json:"mimeType"`
		Filename          string `json:"filename"`
		MediaFileMetadata struct {
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			CameraMake    string `json:"cameraMake"`
			CameraModel   string `json:"cameraModel"`
			PhotoMetadata *struct {
				FocalLength     float64 `json:"focalLength"`
				ApertureFNumber float64 `json:"apertureFNumber"`
				IsoEquivalent   int     `json:"isoEquivalent"`
				ExposureTime    string  `json:"exposureTime"`
			} `json:"photoMetadata"`
			VideoMetadata *struct {
				Fps              float64 `json:"fps"`
				ProcessingStatus string  `json:"processingStatus"`
			} `json:"videoMetadata"`
		} `json:"mediaFileMetadata"`
	} `json:"mediaFile"`
}

// pickerError is a Picker API call's non-2xx answer.
type pickerError struct {
	Status int
	Body   string
}

func (e *pickerError) Error() string {
	return fmt.Sprintf("Photos Picker API answered %d: %s", e.Status, e.Body)
}

// retryable reports whether a request's outcome is worth another try.
func retryable(resp *http.Response, err error) bool {
	return err != nil || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
}

// fetch makes a request, retrying 429s, 5xx and network errors with
// backoff, and hands the final response to read, which may read its body.
func fetch(client *http.Client, method string, url string, body []byte, timeout time.Duration, read func(*http.Response) error) error {
	wait := photosBackoff
	for attempt := 0; ; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			cancel()
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if retryable(resp, err) && attempt < photosRetries {
			if resp != nil {
				resp.Body.Close()
			}
			cancel()
			slog.Warn("Retrying Photos request", "method", method, "attempt", attempt+1, "error", err)
			time.Sleep(wait)
			wait *= 2
			continue
		}
		if err != nil {
			cancel()
			return err
		}
		err = read(resp)
		resp.Body.Close()
		cancel()
		return err
	}
}

// pickerCall calls the Picker API and decodes its JSON answer into out.
func pickerCall(client *http.Client, method string, path string, out any) error {
	var body []byte
	if method == http.MethodPost {
		body = []byte("{}")
	}
	return fetch(client, method, photosPickerApi+path, body, apiTimeout, func(resp *http.Response) error {
		if resp.StatusCode/100 != 2 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
			return &pickerError{Status: resp.StatusCode, Body: string(b)}
		}
		if out == nil {
			return nil
		}
		return json.NewDecoder(resp.Body).Decode(out)
	})
}

// duration parses one of the API's durations ("5s", "1799.96s"), or
// returns def.
func duration(s string, def time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

// The running picks' pollers, by session key, so a cancel can stop one.
var pollers = struct {
	sync.Mutex
	cancel map[string]context.CancelFunc
}{cancel: map[string]context.CancelFunc{}}

// pick is a session being polled, then scanned.
type pick struct {
	key      string
	pickerId string
	userID   int64
	account  googleAccount
	client   *http.Client
	interval time.Duration
	pickBy   time.Time
}

// StartPhotosPick creates a picking session for userID's linked account
// clientKey, records it (db.ErrPickActive if the user has one already),
// and polls it in the background until the user has picked; then it scans
// what they picked.
func StartPhotosPick(userID int64, clientKey string) (db.PickerSession, error) {
	if active, err := db.HasActivePick(userID); err != nil {
		return db.PickerSession{}, err
	} else if active {
		return db.PickerSession{}, db.ErrPickActive
	}
	account, err := resolveAccount(userID, clientKey, "")
	if err != nil {
		return db.PickerSession{}, err
	}
	client := photosClient(account.RefreshToken)
	var session pickingSession
	if err := pickerCall(client, http.MethodPost, "v1/sessions", &session); err != nil {
		return db.PickerSession{}, fmt.Errorf("failed to create a Photos picking session: %w", err)
	}
	pickBy := time.Now().Add(duration(session.PollingConfig.TimeoutIn, defaultPickTimeout))
	if !session.ExpireTime.IsZero() && session.ExpireTime.Before(pickBy) {
		pickBy = session.ExpireTime
	}
	key := make([]byte, 16)
	rand.Read(key)
	saved := db.PickerSession{SessionKey: hex.EncodeToString(key), PickerId: session.Id, UserID: userID,
		ClientKey: clientKey, PickerUri: session.PickerUri, State: db.PickWaiting, PickBy: pickBy}
	if err := db.SavePickerSession(saved); err != nil {
		deletePickingSession(client, session.Id)
		return db.PickerSession{}, err
	}
	p := pick{key: saved.SessionKey, pickerId: session.Id, userID: userID, account: account, client: client,
		interval: duration(session.PollingConfig.PollInterval, minPollInterval), pickBy: pickBy}
	ctx, cancel := context.WithDeadline(context.Background(), pickBy)
	pollers.Lock()
	pollers.cancel[p.key] = cancel
	pollers.Unlock()
	go runPick(ctx, p)
	return saved, nil
}

// CancelPhotosPick cancels userID's waiting session sessionKey:
// db.ErrNotFound if it isn't theirs, ErrPickNotWaiting if it isn't waiting.
func CancelPhotosPick(userID int64, sessionKey string) error {
	session, err := db.GetPickerSession(userID, sessionKey)
	if err != nil {
		return err
	}
	moved, err := db.MovePickerSession(sessionKey, db.PickWaiting, db.PickCancelled, 0)
	if err != nil {
		return err
	}
	if !moved {
		return ErrPickNotWaiting
	}
	pollers.Lock()
	if cancel, ok := pollers.cancel[sessionKey]; ok {
		cancel()
	}
	pollers.Unlock()
	if account, err := resolveAccount(userID, session.ClientKey, ""); err == nil {
		deletePickingSession(photosClient(account.RefreshToken), session.PickerId)
	}
	return nil
}

// deletePickingSession deletes a session at Google; a failure is only
// logged, since sessions expire there anyway.
func deletePickingSession(client *http.Client, pickerId string) {
	if err := pickerCall(client, http.MethodDelete, "v1/sessions/"+url.PathEscape(pickerId), nil); err != nil {
		slog.Warn("Failed to delete Photos picking session", "error", err)
	}
}

// runPick waits for the user to pick, then scans what they picked. A pick
// that isn't made in time expires; one cancelled from the UI was already
// moved and deleted by CancelPhotosPick.
func runPick(ctx context.Context, p pick) {
	defer func() {
		pollers.Lock()
		if cancel, ok := pollers.cancel[p.key]; ok {
			cancel()
			delete(pollers.cancel, p.key)
		}
		pollers.Unlock()
	}()
	picked, err := waitForPick(ctx, p.client, p.pickerId, p.interval)
	if !picked {
		if moved, _ := db.MovePickerSession(p.key, db.PickWaiting, db.PickExpired, 0); moved {
			slog.Info("Photos pick expired", "reason", err)
			deletePickingSession(p.client, p.pickerId)
		}
		return
	}
	// A cancel may have come in since the last poll.
	if moved, err := db.MovePickerSession(p.key, db.PickWaiting, db.PickScanning, 0); err != nil || !moved {
		return
	}
	scanPick(p)
}

// waitForPick polls a session until the user has picked, ctx ends, or the
// session is gone. It reports whether they picked, and else why not.
func waitForPick(ctx context.Context, client *http.Client, pickerId string, interval time.Duration) (bool, error) {
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(max(interval, minPollInterval)):
		}
		var session pickingSession
		err := pickerCall(client, http.MethodGet, "v1/sessions/"+url.PathEscape(pickerId), &session)
		var apiErr *pickerError
		if errors.As(err, &apiErr) && apiErr.Status < 500 {
			return false, err // gone, or no longer ours
		}
		if err != nil {
			if failures++; failures >= maxPollErrors {
				return false, err
			}
			continue
		}
		failures = 0
		if session.MediaItemsSet {
			return true, nil
		}
		interval = duration(session.PollingConfig.PollInterval, interval)
	}
}

// scanPick records a scan of what the user picked, then ends the session.
func scanPick(p pick) {
	defer func() {
		deletePickingSession(p.client, p.pickerId)
		if _, err := db.MovePickerSession(p.key, db.PickScanning, db.PickDone, 0); err != nil {
			slog.Error("Failed to end Photos picking session", "error", err)
		}
	}()
	scanId, err := startScan("google_photos", p.userID, db.ScanMeta{Name: p.account.Name,
		ClientKey: p.account.ClientKey, SearchPath: "Picked in Google Photos"})
	if err != nil {
		slog.Error("Failed to start Photos scan", "client_key", p.account.ClientKey, "error", err)
		return
	}
	if _, err := db.MovePickerSession(p.key, db.PickScanning, db.PickScanning, scanId); err != nil {
		slog.Error("Failed to record the Photos scan's session", "scan_id", scanId, "error", err)
	}
	items, err := collectPicked(p.client, p.pickerId, scanId, p.account.ClientKey)
	if err == nil {
		err = db.SavePickedItems(scanId, items)
	}
	if err != nil {
		slog.Error("Photos scan failed", "scan_id", scanId, "error", err)
		db.MarkScanFailed(scanId, err.Error())
		return
	}
	if err := db.MarkScanCompleted(scanId); err != nil {
		slog.Error("Failed to mark scan complete", "scan_id", scanId, "error", err)
	}
}

// listPicked lists every item picked in a session.
func listPicked(client *http.Client, pickerId string) ([]pickedMediaItem, error) {
	var items []pickedMediaItem
	pageToken := ""
	for {
		q := url.Values{"sessionId": {pickerId}, "pageSize": {fmt.Sprint(pickedPageSize)}}
		if pageToken != "" {
			q.Set("pageToken", pageToken)
		}
		var page struct {
			MediaItems    []pickedMediaItem `json:"mediaItems"`
			NextPageToken string            `json:"nextPageToken"`
		}
		if err := pickerCall(client, http.MethodGet, "v1/mediaItems?"+q.Encode(), &page); err != nil {
			return nil, fmt.Errorf("failed to list picked items: %w", err)
		}
		items = append(items, page.MediaItems...)
		if pageToken = page.NextPageToken; pageToken == "" {
			return items, nil
		}
	}
}

// baseUrls hands out picked items' baseUrls, listing the session again
// once they're older than baseUrlTTL.
type baseUrls struct {
	mu       sync.Mutex
	client   *http.Client
	pickerId string
	listed   time.Time
	urls     map[string]string
}

func (b *baseUrls) set(items []pickedMediaItem) {
	b.listed = time.Now()
	b.urls = make(map[string]string, len(items))
	for _, it := range items {
		b.urls[it.Id] = it.MediaFile.BaseUrl
	}
}

func (b *baseUrls) get(id string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Since(b.listed) >= baseUrlTTL {
		items, err := listPicked(b.client, b.pickerId)
		if err != nil {
			return "", err
		}
		b.set(items)
	}
	return b.urls[id], nil
}

// collectPicked lists a session's picked items and sizes each one,
// publishing progress under clientKey.
func collectPicked(client *http.Client, pickerId string, scanId int, clientKey string) ([]db.PickedItem, error) {
	lock.Lock()
	defer lock.Unlock()
	resetCounters()
	ticker := time.NewTicker(5 * time.Second)
	done := make(chan bool)
	go logProgress(scanId, clientKey, time.Now(), done, ticker, notification.GetPublisher(clientKey))
	defer func() {
		done <- true
		ticker.Stop()
	}()

	listed, err := listPicked(client, pickerId)
	if err != nil {
		return nil, err
	}
	urls := &baseUrls{client: client, pickerId: pickerId}
	urls.set(listed)
	counter_pending.Store(int64(len(listed)))

	items := make([]db.PickedItem, len(listed))
	next := make(chan int)
	throttler := rate.NewLimiter(10, sizeWorkers)
	var wg sync.WaitGroup
	for range sizeWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				items[i] = pickedItem(listed[i])
				if err := throttler.Wait(context.Background()); err == nil {
					sizeItem(client, urls, listed[i], &items[i])
				}
				counter_processed.Add(1)
				counter_pending.Add(-1)
			}
		}()
	}
	for i := range listed {
		next <- i
	}
	close(next)
	wg.Wait()
	return items, nil
}

// pickedItem is what a scan records of an item, before its size.
func pickedItem(it pickedMediaItem) db.PickedItem {
	meta := it.MediaFile.MediaFileMetadata
	item := db.PickedItem{MediaItemId: it.Id, MediaType: it.Type, MimeType: it.MediaFile.MimeType,
		Filename: it.MediaFile.Filename, CreateTime: it.CreateTime, CameraMake: meta.CameraMake,
		CameraModel: meta.CameraModel, SizeSource: db.SizeUnavailable}
	if meta.Width > 0 && meta.Height > 0 {
		item.Width, item.Height = &meta.Width, &meta.Height
	}
	if photo := meta.PhotoMetadata; photo != nil {
		item.FocalLength, item.FNumber = &photo.FocalLength, &photo.ApertureFNumber
		item.Iso, item.ExposureTime = &photo.IsoEquivalent, photo.ExposureTime
	}
	if video := meta.VideoMetadata; video != nil {
		item.Fps = &video.Fps
	}
	return item
}

// sizeItem finds an item's size from a HEAD of its bytes' URL, or else by
// downloading them, which also gives their MD5. Sizes are of the copy
// Google Photos keeps (see docs/archive/photos-picker.md, "Step 0
// findings"). An item it can't size keeps SizeUnavailable.
func sizeItem(client *http.Client, urls *baseUrls, it pickedMediaItem, item *db.PickedItem) {
	suffix := "=d"
	if it.Type == "VIDEO" {
		video := it.MediaFile.MediaFileMetadata.VideoMetadata
		if video == nil || video.ProcessingStatus != "READY" {
			return // no bytes to serve yet
		}
		suffix = "=dv"
	}
	baseUrl, err := urls.get(it.Id)
	if err != nil || baseUrl == "" {
		slog.Warn("No baseUrl for picked item", "media_item_id", it.Id, "error", err)
		return
	}
	download := false
	err = fetch(client, http.MethodHead, baseUrl+suffix, nil, requestTimeout, func(resp *http.Response) error {
		switch {
		case resp.StatusCode == http.StatusOK && resp.ContentLength >= 0:
			size := resp.ContentLength
			item.Size, item.SizeSource = &size, db.SizeFromHead
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return fmt.Errorf("HEAD answered %d", resp.StatusCode)
		default:
			download = true
		}
		return nil
	})
	if err != nil || !download {
		if err != nil {
			slog.Warn("Failed to size picked item", "media_item_id", it.Id, "error", err)
		}
		return
	}
	err = fetch(client, http.MethodGet, baseUrl+suffix, nil, requestTimeout, func(resp *http.Response) error {
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("GET answered %d", resp.StatusCode)
		}
		hash := md5.New()
		size, err := io.Copy(hash, resp.Body)
		if err != nil {
			return err
		}
		item.Size, item.SizeSource, item.Md5Hash = &size, db.SizeFromDownload, hex.EncodeToString(hash.Sum(nil))
		return nil
	})
	if err != nil {
		slog.Warn("Failed to download picked item", "media_item_id", it.Id, "error", err)
	}
}
