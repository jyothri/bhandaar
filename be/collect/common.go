package collect

import (
	"errors"
	"fmt"
	"net/http"
	"sync"

	"google.golang.org/api/googleapi"
)

var lock sync.RWMutex

func checkError(err error, msg ...string) {
	if err != nil {
		retryEligible := isRetryError(err)
		fmt.Printf("retryEligible: %v\n", retryEligible)
		fmt.Println(msg)
		panic(err)
	}
}

func isRetryError(err error) bool {
	// Try Google API error
	var googleErr *googleapi.Error
	if errors.As(err, &googleErr) {
		statusCode := googleErr.Code
		if statusCode == http.StatusTooManyRequests {
			return true
		}
		fmt.Printf("Unknown Google API error: code: %v %v. Message: %v\n", statusCode, err, err.Error())
	}
	return false
}
