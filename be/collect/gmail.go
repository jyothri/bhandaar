package collect

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

var counter_processed atomic.Int64
var counter_pending atomic.Int64
var start time.Time
var gmailConfig *oauth2.Config

const (
	MaxRetryCount = 3
	SleepTime     = 1 * time.Second
)

func init() {
	gmailConfig = &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmail.GmailReadonlyScope},
	}
}

// resetCounters resets progress counters to zero for a new scan
func resetCounters() {
	counter_processed.Store(0)
	counter_pending.Store(0)
}

func getGmailService(refreshToken string) (*gmail.Service, error) {
	tokenSrc := oauth2.Token{
		RefreshToken: refreshToken,
	}
	ctx := context.Background()
	gmailService, err := gmail.NewService(ctx, option.WithTokenSource(gmailConfig.TokenSource(ctx, &tokenSrc)))
	if err != nil {
		return nil, fmt.Errorf("failed to create gmail service: %w", err)
	}
	return gmailService, nil
}

func Gmail(gMailScan GMailScan) (int, error) {
	// Phase 1: Create scan record (synchronous)
	scanId, err := db.LogStartScan("gmail")
	if err != nil {
		return 0, fmt.Errorf("failed to start gmail scan (account=%s, filter=%s): %w",
			gMailScan.ClientKey, gMailScan.Filter, err)
	}

	// Save metadata in background
	go func() {
		if err := db.SaveScanMetadata(gMailScan.Username, "", gMailScan.Filter, scanId); err != nil {
			slog.Error("Failed to save scan metadata",
				"scan_id", scanId,
				"error", err)
		}
	}()

	// Get refresh token
	if gMailScan.ClientKey != "" {
		token, err := db.GetOAuthToken(gMailScan.ClientKey)
		if err != nil {
			return 0, fmt.Errorf("failed to get OAuth token for client %s: %w", gMailScan.ClientKey, err)
		}
		gMailScan.RefreshToken = token.RefreshToken
	}
	if gMailScan.RefreshToken == "" {
		return 0, fmt.Errorf("refresh token is empty for account %s", gMailScan.ClientKey)
	}

	// Get Gmail service
	gmailService, err := getGmailService(gMailScan.RefreshToken)
	if err != nil {
		return 0, fmt.Errorf("failed to get gmail service for scan %d: %w", scanId, err)
	}

	// Phase 2: Start collection in background (asynchronous)
	messageMetaData := make(chan db.MessageMetadata, 10)
	go func() {
		defer close(messageMetaData)

		err := startGmailScan(gmailService, scanId, gMailScan, messageMetaData)
		if err != nil {
			slog.Error("Gmail scan collection failed",
				"scan_id", scanId,
				"account", gMailScan.ClientKey,
				"error", err)
			db.MarkScanFailed(scanId, err.Error())
			return
		}
	}()

	// Start processing messages in background
	go db.SaveMessageMetadataToDb(scanId, gMailScan.Username, messageMetaData)

	return scanId, nil
}

func GetIdentity(refreshToken string) (string, error) {
	if refreshToken == "" {
		return "", fmt.Errorf("refresh token is empty")
	}

	gmailService, err := getGmailService(refreshToken)
	if err != nil {
		return "", fmt.Errorf("failed to get gmail service: %w", err)
	}

	profile := gmailService.Users.GetProfile("me")
	profileInfo, err := profile.Do()
	if err != nil {
		return "", fmt.Errorf("failed to get user profile from Gmail API: %w", err)
	}

	return profileInfo.EmailAddress, nil
}

func startGmailScan(gmailService *gmail.Service, scanId int, gMailScan GMailScan, messageMetaData chan<- db.MessageMetadata) error {
	queryString := gMailScan.Filter
	start = time.Now()
	lock.Lock()
	defer lock.Unlock()
	resetCounters()
	var wg sync.WaitGroup
	ticker := time.NewTicker(5 * time.Second)
	done := make(chan bool)
	notificationChannel := notification.GetPublisher(gMailScan.ClientKey)
	go logProgress(scanId, gMailScan.ClientKey, done, ticker, notificationChannel)
	throttler := rate.NewLimiter(50, 5)

	messageListCall := gmailService.Users.Messages.List("me").Q(queryString)
	hasNextPage := true
	for hasNextPage {
		var messageList *gmail.ListMessagesResponse
		var lastErr error
		for i := 0; i < MaxRetryCount; i++ {
			messageListLocal, err := messageListCall.Do()
			if err == nil {
				messageList = messageListLocal
				lastErr = nil
				break
			}
			lastErr = err
			if !isRetryError(err) || i == MaxRetryCount-1 {
				done <- true
				ticker.Stop()
				return fmt.Errorf("failed to list messages for query '%s' after %d retries: %w",
					queryString, MaxRetryCount, err)
			}
			slog.Info(fmt.Sprintf("Got retryable error for Query: %s. Attempt #: %d of %d.", queryString, i, MaxRetryCount))
			time.Sleep(SleepTime)
			err = throttler.Wait(context.Background())
			if err != nil {
				done <- true
				ticker.Stop()
				return fmt.Errorf("rate limiter error: %w", err)
			}
		}
		if lastErr != nil {
			done <- true
			ticker.Stop()
			return fmt.Errorf("failed to get message list: %w", lastErr)
		}
		wg.Add(len(messageList.Messages))
		counter_pending.Add(int64(len(messageList.Messages)))
		parseMessageList(gmailService, messageList, messageMetaData, &wg, throttler)
		if messageList.NextPageToken == "" {
			hasNextPage = false
		}
		messageListCall = messageListCall.PageToken(messageList.NextPageToken)
	}
	wg.Wait()
	done <- true
	ticker.Stop()
	slog.Info(fmt.Sprintf("Finished Scan. ScanId: %v", scanId))
	return nil
}

func parseMessageList(gmailService *gmail.Service, messageList *gmail.ListMessagesResponse, messageMetaData chan<- db.MessageMetadata, wg *sync.WaitGroup, throttler *rate.Limiter) {
	for _, message := range messageList.Messages {
		throttler.Wait(context.Background())
		go getMessageInfo(gmailService, message.Id, messageMetaData, MaxRetryCount, wg)
	}
}

func getMessageInfo(gmailService *gmail.Service, id string, messageMetaData chan<- db.MessageMetadata, retryCount int, wg *sync.WaitGroup) {
	defer wg.Done()

	messageListCall := gmailService.Users.Messages.Get("me", id).Format("metadata").MetadataHeaders("From", "To", "Subject", "Date")
	message, err := messageListCall.Do()
	if err != nil {
		if isRetryError(err) {
			slog.Info(fmt.Sprintf("Got retryable error for message: %s. Retries remaining: %d", id, retryCount))
			if retryCount > 0 {
				slog.Info(fmt.Sprintf("Retrying for message: %s after wait.", id))
				time.Sleep(SleepTime)
				// Note: Don't call wg.Done() again - already deferred above
				wg.Add(1)
				go getMessageInfo(gmailService, id, messageMetaData, retryCount-1, wg)
				return
			}
		}
		// Log and skip this message instead of crashing
		slog.Error("Failed to get message info, skipping",
			"message_id", id,
			"retries_exhausted", retryCount == 0,
			"error", err)
		return
	}
	from := ""
	to := ""
	subject := ""
	date := time.Unix(message.InternalDate/1000, 0)
	for _, headers := range message.Payload.Headers {
		switch headers.Name {
		case "From":
			from = headers.Value
		case "To":
			to = headers.Value
		case "Subject":
			subject = headers.Value
		}
	}
	md := db.MessageMetadata{
		MessageId:    message.Id,
		ThreadId:     message.ThreadId,
		LabelIds:     message.LabelIds,
		From:         from,
		To:           to,
		Subject:      subject,
		Date:         date,
		SizeEstimate: message.SizeEstimate,
	}
	messageMetaData <- md
	counter_processed.Add(1)
	counter_pending.Add(-1)
	// wg.Done() is handled by defer at function start
}

func logProgress(scanId int, ClientKey string, done <-chan bool, ticker *time.Ticker, notificationChannel chan<- notification.Progress) {
	defer close(notificationChannel)
	for {
		select {
		case <-done:
			progress := notification.Progress{
				ProcessedCount: int(counter_processed.Load()),
				ActiveCount:    int(counter_pending.Load()),
				ScanId:         scanId,
				ClientKey:      ClientKey,
				ElapsedInSec:   int(time.Since(start).Seconds()),
			}
			notificationChannel <- progress
			return
		case <-ticker.C:
			progress := notification.Progress{
				ProcessedCount: int(counter_processed.Load()),
				ActiveCount:    int(counter_pending.Load()),
				ScanId:         scanId,
				ClientKey:      ClientKey,
				ElapsedInSec:   int(time.Since(start).Seconds()),
			}
			notificationChannel <- progress
		}
	}
}

type GMailScan struct {
	Filter       string
	RefreshToken string
	ClientKey    string
	Username     string
}
