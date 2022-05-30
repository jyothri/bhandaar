package collect

import (
	"context"
	"fmt"
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
	go db.SaveScanMetadata("", gMailScan.Filter, scanId)
	gmailService := getGmailService(gMailScan.RefreshToken)
	go startGmailScan(gmailService, scanId, gMailScan.Filter, messageMetaData)
	go db.SaveMessageMetadataToDb(scanId, messageMetaData)
	return scanId
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
		messageList, err := messageListCall.Do()
		checkError(err)
		err = throttler.Wait(context.Background())
		checkError(err, fmt.Sprintf("Error with limiter: %s", err))

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
}

func parseMessageList(gmailService *gmail.Service, messageList *gmail.ListMessagesResponse, messageMetaData chan<- db.MessageMetadata, wg *sync.WaitGroup, throttler *rate.Limiter) {
	for _, message := range messageList.Messages {
		throttler.Wait(context.Background())
		go getMessageInfo(gmailService, message.Id, messageMetaData, wg)
	}
}

func getMessageInfo(gmailService *gmail.Service, id string, messageMetaData chan<- db.MessageMetadata, wg *sync.WaitGroup) {
	messageListCall := gmailService.Users.Messages.Get("me", id).Format("metadata").MetadataHeaders("From", "To", "Subject", "Date")
	message, err := messageListCall.Do()
	checkError(err)
	from := ""
	to := ""
	subject := ""
	date := ""
	for _, headers := range message.Payload.Headers {
		switch headers.Name {
		case "From":
			from = headers.Value
		case "To":
			to = headers.Value
		case "Subject":
			subject = headers.Value
		case "Date":
			date = headers.Value
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
		case t := <-ticker.C:
			fmt.Printf("At: %v. Processed= %v, in-progress= %v\n", t, counter_processed, counter_pending)
		}
	}
}

type GMailScan struct {
	Filter       string
	RefreshToken string
}
