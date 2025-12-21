package collect

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"google.golang.org/api/googleapi"
)

var lock sync.RWMutex

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
