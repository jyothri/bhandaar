package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// Size limit constants
const (
	DefaultMaxBodySize       = 512 << 10 // 512 KB
	ScanRequestMaxBodySize   = 1 << 20   // 1 MB
	OAuthCallbackMaxBodySize = 16 << 10  // 16 KB
	FormDataMaxBodySize      = 16 << 10  // 16 KB
)

// RequestSizeLimitMiddleware limits the size of request bodies
func RequestSizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Wrap the request body with MaxBytesReader
			// This prevents the server from reading more than maxBytes
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

			next.ServeHTTP(w, r)
		})
	}
}

// handleMaxBytesError checks if an error is due to request body being too large
func handleMaxBytesError(w http.ResponseWriter, r *http.Request, err error, maxBytes int64) bool {
	if err == nil {
		return false
	}

	// Check if error message indicates size limit exceeded
	errMsg := err.Error()
	if errMsg == "http: request body too large" ||
		errMsg == "request body too large" {

		// Log the oversized request attempt
		slog.Warn("Request body size limit exceeded",
			"remote_addr", r.RemoteAddr,
			"user_agent", r.UserAgent(),
			"method", r.Method,
			"path", r.URL.Path,
			"max_bytes", maxBytes,
			"max_human", formatBytes(maxBytes))

		// Return 413 Payload Too Large with JSON error
		writeErrorResponse(w, ErrorResponse{
			Error: ErrorDetail{
				Code:    "PAYLOAD_TOO_LARGE",
				Message: "Request body exceeds maximum allowed size",
				Details: map[string]interface{}{
					"max_size_bytes": maxBytes,
					"max_size_human": formatBytes(maxBytes),
				},
				Timestamp: time.Now().UTC().Format(time.RFC3339),
			},
		}, http.StatusRequestEntityTooLarge)

		return true
	}

	return false
}

// ErrorResponse represents a structured error response
type ErrorResponse struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail contains error information
type ErrorDetail struct {
	Code      string                 `json:"code"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Timestamp string                 `json:"timestamp"`
}

// writeErrorResponse writes a JSON error response
func writeErrorResponse(w http.ResponseWriter, errResp ErrorResponse, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(errResp); err != nil {
		slog.Error("Failed to encode error response", "error", err)
	}
}

// formatBytes formats bytes into human-readable format
func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
