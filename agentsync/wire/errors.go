package wire

// ErrorResponse is the body of every error response. It has the same shape as
// be's errors (be/web/middleware.go).
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail describes one error.
type ErrorDetail struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details,omitempty"`
	Timestamp string         `json:"timestamp"`
}

// Error codes. Agents classify a response by its HTTP status first; the code
// only refines it (nginx answers some statuses itself, with an HTML body).
const (
	CodeInvalidRequest        = "INVALID_REQUEST"           // 400
	CodeInvalidBatch          = "INVALID_BATCH"             // 400
	CodeTokenExpired          = "TOKEN_EXPIRED"             // 401: refresh once, then retry
	CodeInvalidToken          = "INVALID_TOKEN"             // 401: missing or bad access token
	CodeInvalidCredentials    = "INVALID_CREDENTIALS"       // 401
	CodeInvalidRefreshToken   = "INVALID_REFRESH_TOKEN"     // 401
	CodeRefreshReused         = "REFRESH_REUSED"            // 401
	CodeAgentOwnedByOtherUser = "AGENT_OWNED_BY_OTHER_USER" // 403
	CodeAgentMismatch         = "AGENT_MISMATCH"            // 403
	CodeNotFound              = "NOT_FOUND"                 // 404
	CodeDriveNotOpen          = "DRIVE_NOT_OPEN"            // 404
	CodeStreamMismatch        = "STREAM_MISMATCH"           // 409
	CodePayloadTooLarge       = "PAYLOAD_TOO_LARGE"         // 413
	CodeIdempotencyKeyReused  = "IDEMPOTENCY_KEY_REUSED"    // 422
	CodeUpgradeRequired       = "UPGRADE_REQUIRED"          // 426
	CodeRateLimited           = "RATE_LIMITED"              // 429
	CodeTooManyAttempts       = "TOO_MANY_ATTEMPTS"         // 429
	CodeInternal              = "INTERNAL"                  // 500
	CodeServiceUnavailable    = "SERVICE_UNAVAILABLE"       // 503
)
