package remote

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jyothri/bhandaar/agent/wire"
)

// The four kinds of failure. Test with errors.Is(err, remote.ErrTransient)
// and so on; *Error carries the details.
var (
	// ErrTransient: the server is unreachable or busy; back off and retry.
	ErrTransient = errors.New("remote unavailable")
	// ErrAuth: the credentials were refused. TOKEN_EXPIRED and INVALID_TOKEN
	// are worth one refresh; anything else needs "driveagent login".
	ErrAuth = errors.New("remote refused the credentials")
	// ErrUpgrade: this driveagent is too old (or too new) for the server.
	ErrUpgrade = errors.New("driveagent upgrade required")
	// ErrPermanent: a request the server will never accept; retrying won't help.
	ErrPermanent = errors.New("remote rejected the request")
)

// Error is a failed request.
type Error struct {
	Kind       error  // one of the four kinds above
	Status     int    // HTTP status, or 0 when there was no response
	Code       string // the JSON error code, if the body had one
	Message    string
	RetryAfter time.Duration // from Retry-After, for 429 and 503
	Err        error         // the underlying network error, if any
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Kind.Error())
	if e.Status != 0 {
		fmt.Fprintf(&b, ": HTTP %d", e.Status)
	}
	if e.Code != "" {
		fmt.Fprintf(&b, " %s", e.Code)
	}
	if e.Message != "" {
		fmt.Fprintf(&b, ": %s", e.Message)
	}
	if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// Is makes errors.Is(err, ErrTransient) (etc.) match on the kind.
func (e *Error) Is(target error) bool { return target == e.Kind }

// Refreshable reports whether a refreshed access token might fix err.
func Refreshable(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Kind == ErrAuth &&
		(e.Code == wire.CodeTokenExpired || e.Code == wire.CodeInvalidToken)
}

// classify turns a non-2xx response into an *Error. It goes by the status
// code first: nginx answers some statuses itself (413, 502, 504) with an
// HTML body, so the JSON code only refines the kind.
func classify(resp *http.Response, body []byte) *Error {
	e := &Error{Status: resp.StatusCode}
	var parsed wire.ErrorResponse
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Code != "" {
		e.Code, e.Message = parsed.Error.Code, parsed.Error.Message
	} else {
		e.Message = http.StatusText(resp.StatusCode) + " (not a JSON error; likely from a proxy)"
	}
	switch s := resp.StatusCode; {
	case s == http.StatusUnauthorized:
		e.Kind = ErrAuth
	case s == http.StatusUpgradeRequired:
		e.Kind = ErrUpgrade
	case s == http.StatusTooManyRequests, s == http.StatusRequestTimeout, s >= 500:
		e.Kind = ErrTransient
		e.RetryAfter = retryAfter(resp.Header.Get("Retry-After"))
	default:
		// 400, 403, 404, 409, 413, 422 and anything unexpected. Callers act
		// on some of these by Status/Code (404 DRIVE_NOT_OPEN, 409
		// STREAM_MISMATCH, 413 halve the batch).
		e.Kind = ErrPermanent
	}
	return e
}

func retryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}
