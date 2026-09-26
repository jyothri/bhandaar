package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jyothri/bhandaar/agentsync/wire"
)

// writeJSON writes v as a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode response", "error", err)
	}
}

// writeError writes an error in the same shape as be's.
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, wire.ErrorResponse{Error: wire.ErrorDetail{
		Code:      code,
		Message:   message,
		Details:   details,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}})
}

func writeInternal(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, wire.CodeInternal, "internal error", nil)
}

// decodeJSON reads a JSON request body into v. It answers the request itself
// and returns false on a failure: 413 past the body limit, otherwise 400.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	err := dec.Decode(v)
	if err == nil && dec.More() {
		err = errors.New("unexpected data after the JSON value")
	}
	if err == nil {
		return true
	}
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeError(w, http.StatusRequestEntityTooLarge, wire.CodePayloadTooLarge,
			fmt.Sprintf("request body exceeds %d bytes", tooBig.Limit),
			map[string]any{"max_size_bytes": tooBig.Limit})
		return false
	}
	writeError(w, http.StatusBadRequest, wire.CodeInvalidRequest, "invalid JSON body: "+err.Error(), nil)
	return false
}
