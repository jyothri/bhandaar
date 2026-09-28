package collect

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/jyothri/hdd/db"
	"google.golang.org/api/googleapi"
)

var lock sync.RWMutex

// failStart marks a scan that was logged but couldn't start as failed, so
// it doesn't stay open forever, and returns err for the caller.
func failStart(scanId int, err error) (int, error) {
	db.MarkScanFailed(scanId, err.Error())
	return 0, err
}

// refreshToken returns the Google refresh token a scan uses: that of the
// account clientKey, which userID must have linked, or else the token given
// in the request.
func refreshToken(userID int64, clientKey string, token string) (string, error) {
	if clientKey != "" {
		account, err := db.GetOAuthToken(userID, clientKey)
		if err != nil {
			return "", err
		}
		token = account.RefreshToken
	}
	if token == "" {
		return "", fmt.Errorf("refresh token is empty for account %s", clientKey)
	}
	return token, nil
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
