package collect

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"google.golang.org/api/googleapi"
)

var lock sync.RWMutex

// Progress of the running scan (scans run one at a time, behind lock).
var counter_processed atomic.Int64
var counter_pending atomic.Int64

// resetCounters resets progress counters to zero for a new scan
func resetCounters() {
	counter_processed.Store(0)
	counter_pending.Store(0)
}

// logProgress publishes the running scan's counts every tick, and once more
// when done is signalled, then closes notificationChannel.
func logProgress(scanId int, ClientKey string, start time.Time, done <-chan bool, ticker *time.Ticker, notificationChannel chan<- notification.Progress) {
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
				Status:         notification.StatusRunning,
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
				Status:         notification.StatusRunning,
			}
			notificationChannel <- progress
		}
	}
}

// failStart marks a scan that was logged but couldn't start as failed, so
// it doesn't stay open forever, and returns err for the caller.
func failStart(scanId int, err error) (int, error) {
	db.MarkScanFailed(scanId, err.Error())
	return 0, err
}

// linkedAccount looks up a user's linked account; tests replace it.
var linkedAccount = db.GetOAuthToken

// googleAccount is the Google account a scan runs as.
type googleAccount struct {
	RefreshToken string
	// The linked account's key and display name, which the scan is recorded
	// under; both empty for a raw refresh token.
	ClientKey string
	Name      string
}

// resolveAccount returns the Google account a scan uses: the account
// clientKey, which userID must have linked, or else the refresh token given
// in the request. The name comes from the database, never the request, so
// all scans of one account list together (docs/specs/request-drive-scans.md,
// "Account names").
func resolveAccount(userID int64, clientKey string, token string) (googleAccount, error) {
	account := googleAccount{RefreshToken: token}
	if clientKey != "" {
		linked, err := linkedAccount(userID, clientKey)
		if err != nil {
			return googleAccount{}, err
		}
		account = googleAccount{RefreshToken: linked.RefreshToken, ClientKey: clientKey, Name: linked.DisplayName}
	}
	if account.RefreshToken == "" {
		return googleAccount{}, fmt.Errorf("refresh token is empty for account %s", clientKey)
	}
	return account, nil
}

func isRetryError(err error) bool {
	// Try Google API error
	var googleErr *googleapi.Error
	if errors.As(err, &googleErr) {
		statusCode := googleErr.Code
		if statusCode == http.StatusTooManyRequests {
			return true
		}
		if statusCode == http.StatusForbidden {
			if len(googleErr.Errors) > 0 && googleErr.Errors[0].Reason == "rateLimitExceeded" {
				fmt.Printf("rateLimitExceeded error. Message: %v\n", googleErr.Message)
				return true
			}
		}
		fmt.Printf("Unknown Google API error: code: %v Message: %v error: %v\n", statusCode, googleErr.Message, err)
	}
	return false
}
