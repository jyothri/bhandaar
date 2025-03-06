package collect

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jyothri/hdd/constants"
	"github.com/jyothri/hdd/db"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

var counter_processed int
var counter_pending int
var gmailConfig *oauth2.Config

const (
	MaxRetryCount = 3
	SleepTime     = 500 * time.Millisecond
)

func init() {
	gmailConfig = &oauth2.Config{
		ClientID:     constants.OauthClientId,
		ClientSecret: constants.OauthClientSecret,
		Endpoint:     google.Endpoint,
		Scopes:       []string{gmail.GmailReadonlyScope},
	}
}

func getGmailService(refreshToken string) *gmail.Service {
	tokenSrc := oauth2.Token{
		RefreshToken: refreshToken,
	}
	ctx := context.Background()
	gmailService, err := gmail.NewService(ctx, option.WithTokenSource(gmailConfig.TokenSource(ctx, &tokenSrc)))
	checkError(err)
	return gmailService
}

func Gmail(gMailScan GMailScan) int {
	messageMetaData := make(chan db.MessageMetadata, 10)
	scanId := db.LogStartScan("gmail")
	go db.SaveScanMetadata(gMailScan.Username, "", gMailScan.Filter, scanId)
	if gMailScan.ClientKey != "" {
		token := db.GetOAuthToken(gMailScan.ClientKey)
		gMailScan.RefreshToken = token.RefreshToken
	}
	if gMailScan.RefreshToken == "" {
		slog.Warn("Refresh token not found. Cannot proceed.")
		return -1
	}
	gmailService := getGmailService(gMailScan.RefreshToken)
	go startGmailScan(gmailService, scanId, gMailScan.Filter, messageMetaData)
	go db.SaveMessageMetadataToDb(scanId, gMailScan.Username, messageMetaData)
	return scanId
}

func GetIdentity(refreshToken string) string {
	if refreshToken == "" {
		slog.Warn("Refresh token not found. Cannot proceed.")
		return ""
	}
	gmailService := getGmailService(refreshToken)
	profile := gmailService.Users.GetProfile("me")
	profileInfo, err := profile.Do()
	checkError(err)
	return profileInfo.EmailAddress
}

func startGmailScan(gmailService *gmail.Service, scanId int, queryString string, messageMetaData chan<- db.MessageMetadata) {
	lock.Lock()
	defer lock.Unlock()
	var wg sync.WaitGroup
	ticker := time.NewTicker(5 * time.Second)
	done := make(chan bool)
	go logProgressToConsole(done, ticker)
	throttler := rate.NewLimiter(50, 5)

	messageListCall := gmailService.Users.Messages.List("me").Q(queryString)
	hasNextPage := true
	for hasNextPage {
		var messageList *gmail.ListMessagesResponse
		for i := 0; i < MaxRetryCount; i++ {
			messageListLocal, err := messageListCall.Do()
			if err == nil {
				messageList = messageListLocal
				break
			}
			if !isRetryError(err) || i == MaxRetryCount-1 {
				checkError(err)
			}
			slog.Info(fmt.Sprintf("Got retryable error for Query: %s. Retry count: %d.", queryString, i))
			time.Sleep(SleepTime)
			err = throttler.Wait(context.Background())
			checkError(err, fmt.Sprintf("Error with limiter: %s", err))
		}

		wg.Add(len(messageList.Messages))
		counter_pending += len(messageList.Messages)
		parseMessageList(gmailService, messageList, messageMetaData, &wg, throttler)
		if messageList.NextPageToken == "" {
			hasNextPage = false
		}
		messageListCall = messageListCall.PageToken(messageList.NextPageToken)
	}
	wg.Wait()
	done <- true
	ticker.Stop()
	close(messageMetaData)
	slog.Info(fmt.Sprintf("Finished Scan. ScanId: %v", scanId))
}

func parseMessageList(gmailService *gmail.Service, messageList *gmail.ListMessagesResponse, messageMetaData chan<- db.MessageMetadata, wg *sync.WaitGroup, throttler *rate.Limiter) {
	for _, message := range messageList.Messages {
		throttler.Wait(context.Background())
		go getMessageInfo(gmailService, message.Id, messageMetaData, MaxRetryCount, wg)
	}
}

func getMessageInfo(gmailService *gmail.Service, id string, messageMetaData chan<- db.MessageMetadata, retryCount int, wg *sync.WaitGroup) {
	messageListCall := gmailService.Users.Messages.Get("me", id).Format("metadata").MetadataHeaders("From", "To", "Subject", "Date")
	message, err := messageListCall.Do()
	if err != nil {
		if isRetryError(err) {
			slog.Info(fmt.Sprintf("Got retryable error for message: %s. Retry count: %d", id, retryCount))
			if retryCount > 0 {
				slog.Info(fmt.Sprintf("Retrying for message: %s after wait.", id))
				time.Sleep(SleepTime)
				getMessageInfo(gmailService, id, messageMetaData, retryCount-1, wg)
				return
			}
		}
		checkError(err)
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
	counter_processed += 1
	counter_pending -= 1
	wg.Done()
}

func logProgressToConsole(done <-chan bool, ticker *time.Ticker) {
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			slog.Info(fmt.Sprintf("Processed= %v, in-progress= %v", counter_processed, counter_pending))
		}
	}
}

type GMailScan struct {
	Filter       string
	RefreshToken string
	ClientKey    string
	Username     string
}
