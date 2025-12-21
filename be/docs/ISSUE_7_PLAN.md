# Issue #7 Implementation Plan: Request Body Size Limits

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P0 - Critical Security Issue (DoS Vulnerability)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #7: No Request Body Size Limits (DoS Vulnerability)**. The current system allows clients to send unlimited request body sizes, which can lead to memory exhaustion and server crashes.

**Selected Approach:**
- **Per-endpoint limits**: API endpoints (1 MB), OAuth (16 KB), Default (512 KB)
- **Response**: HTTP 413 (Payload Too Large) with JSON error message
- **Logging**: Log with IP address and size for security monitoring
- **Implementation**: Middleware + per-handler overrides where needed

**Estimated Effort:** 2-3 hours

**Impact:**
- Prevents DoS attacks via large request bodies
- Protects server memory and stability
- Improves overall system resilience

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Implementation Details](#3-implementation-details)
4. [Testing Strategy](#4-testing-strategy)
5. [Deployment Plan](#5-deployment-plan)
6. [Monitoring and Alerts](#6-monitoring-and-alerts)

---

## 1. Current State Analysis

### 1.1 Vulnerability Description

**Problem:**
```go
// web/api.go:37-43
func DoScansHandler(w http.ResponseWriter, r *http.Request) {
    decoder := json.NewDecoder(r.Body)  // ❌ No size limit!
    var doScanRequest DoScanRequest
    err := decoder.Decode(&doScanRequest)
    // Attacker can send gigabytes of JSON
    // Server attempts to read entire body into memory
    // Result: OOM (Out of Memory) crash
}
```

**Attack Scenario:**
```bash
# Attacker sends 10 GB request
curl -X POST http://your-server.com/api/scans \
  -H "Content-Type: application/json" \
  -d @10GB_file.json

# Server attempts to decode entire 10 GB into memory
# Process memory usage spikes
# OOM killer terminates the process
# Service goes down
```

### 1.2 Affected Endpoints

| Endpoint | Method | Current State | Risk Level | Proposed Limit |
|----------|--------|---------------|------------|----------------|
| `/api/scans` | POST | ❌ No limit | CRITICAL | 1 MB |
| `/api/glink` | GET | ❌ No limit | HIGH | 16 KB |
| `/events` (SSE) | GET | ✅ No body | LOW | N/A |
| All GET endpoints | GET | ⚠️ Form data | MEDIUM | 16 KB |
| Future POST endpoints | POST | ❌ No limit | HIGH | 512 KB (default) |

### 1.3 Risk Assessment

**Without Size Limits:**
- 🔴 **Critical**: Single request can consume all available memory
- 🔴 **Critical**: Easy to execute (no authentication needed in current state)
- 🔴 **Critical**: No rate limiting compounds the issue
- 🟡 **High**: After authentication (Issue #3), still vulnerable to authenticated users

**Attack Complexity:** Low (simple curl command)
**Detection Difficulty:** Medium (requires memory monitoring)
**Business Impact:** Severe (complete service outage)

---

## 2. Target Architecture

### 2.1 Multi-Layered Defense

```
┌─────────────────────────────────────────────────────────────┐
│ Layer 1: Global Middleware (Default 512 KB)                │
│ - Applied to all routes                                     │
│ - Catches any endpoint without specific limit               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 2: Route-Specific Middleware                          │
│ - POST /api/scans: 1 MB (JSON payload for scan config)     │
│ - GET /api/glink: 16 KB (OAuth callback params)            │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 3: Handler-Level Validation                           │
│ - Additional validation of decoded content                  │
│ - Field-level size checks (e.g., max path length)          │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ Layer 4: Logging & Monitoring                               │
│ - Log oversized requests with IP, size, endpoint            │
│ - Alert on repeated attempts (potential attack)             │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Size Limit Configuration

```go
const (
    // Default limit for most endpoints
    DefaultMaxBodySize = 512 << 10  // 512 KB

    // Scan creation needs larger payload
    // (contains paths, filters, OAuth tokens)
    ScanRequestMaxBodySize = 1 << 20  // 1 MB

    // OAuth callback has minimal data
    OAuthCallbackMaxBodySize = 16 << 10  // 16 KB

    // Form data (query params, small POSTs)
    FormDataMaxBodySize = 16 << 10  // 16 KB
)
```

**Rationale:**

| Limit | Justification |
|-------|---------------|
| 1 MB (Scans) | Scan requests contain: paths (≤2000 chars), filters, OAuth tokens (≤800 chars), metadata. 1 MB provides 10x headroom. |
| 16 KB (OAuth) | OAuth responses are <1 KB. 16 KB provides 16x safety margin. |
| 512 KB (Default) | Conservative default that allows reasonable JSON payloads while preventing abuse. |

### 2.3 Error Response Format

```json
{
  "error": {
    "code": "PAYLOAD_TOO_LARGE",
    "message": "Request body exceeds maximum allowed size",
    "details": {
      "max_size_bytes": 1048576,
      "max_size_human": "1 MB"
    },
    "timestamp": "2025-12-21T10:30:00Z"
  }
}
```

---

## 3. Implementation Details

### 3.1 Core Middleware: `web/middleware.go`

**Update existing file or create if it doesn't exist:**

```go
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
    DefaultMaxBodySize       = 512 << 10  // 512 KB
    ScanRequestMaxBodySize   = 1 << 20    // 1 MB
    OAuthCallbackMaxBodySize = 16 << 10   // 16 KB
    FormDataMaxBodySize      = 16 << 10   // 16 KB
)

// RequestSizeLimitMiddleware limits the size of request bodies
func RequestSizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Wrap the request body with MaxBytesReader
            // This prevents the server from reading more than maxBytes
            r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

            // Create a custom response writer to catch the size limit error
            wrapper := &responseSizeLimitWriter{
                ResponseWriter: w,
                request:        r,
                maxBytes:       maxBytes,
            }

            next.ServeHTTP(wrapper, r)
        })
    }
}

// responseSizeLimitWriter wraps http.ResponseWriter to intercept errors
type responseSizeLimitWriter struct {
    http.ResponseWriter
    request  *http.Request
    maxBytes int64
    wroteHeader bool
}

func (w *responseSizeLimitWriter) Write(b []byte) (int, error) {
    // If handler hasn't written header yet, write it
    if !w.wroteHeader {
        w.WriteHeader(http.StatusOK)
    }
    return w.ResponseWriter.Write(b)
}

func (w *responseSizeLimitWriter) WriteHeader(statusCode int) {
    if w.wroteHeader {
        return
    }
    w.wroteHeader = true
    w.ResponseWriter.WriteHeader(statusCode)
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
```

### 3.2 Handler Updates: `web/api.go`

**Update `DoScansHandler` to explicitly handle size limit errors:**

```go
func DoScansHandler(w http.ResponseWriter, r *http.Request) {
    // Decode request with size limit protection
    decoder := json.NewDecoder(r.Body)
    var doScanRequest DoScanRequest
    err := decoder.Decode(&doScanRequest)

    // Check if error is due to size limit
    if handleMaxBytesError(w, r, err, ScanRequestMaxBodySize) {
        return
    }

    if err != nil {
        slog.Error("Failed to decode scan request", "error", err)
        http.Error(w, "Invalid request body", http.StatusBadRequest)
        return
    }

    // Continue with validation and processing
    slog.Info("Received scan request",
        "scan_type", doScanRequest.ScanType,
        "body_size_estimate", r.ContentLength)

    // ... rest of handler
}
```

**No changes needed for GET handlers** (they don't have request bodies).

### 3.3 OAuth Handler Updates: `web/oauth.go`

```go
func GoogleAccountLinkingHandler(w http.ResponseWriter, r *http.Request) {
    const googleTokenUrl = "https://oauth2.googleapis.com/token"
    const grantType = "authorization_code"

    // Parse form (already limited by middleware)
    err := r.ParseForm()
    if handleMaxBytesError(w, r, err, OAuthCallbackMaxBodySize) {
        return
    }

    if err != nil {
        slog.Error("Failed to parse OAuth form", "error", err)
        http.Error(w, "Invalid request format", http.StatusBadRequest)
        return
    }

    var redirectUri = r.FormValue("redirectUri")
    if redirectUri == "" {
        http.Error(w, "redirectUri not found in request", http.StatusBadRequest)
        return
    }

    // ... rest of handler
}
```

### 3.4 Router Configuration: `web/web_server.go`

```go
func Server() {
    slog.Info("Starting web server.")

    // Initialize JWT manager (if Issue #3 implemented)
    // ...

    r := mux.NewRouter()

    // Apply global default size limit to all routes
    r.Use(RequestSizeLimitMiddleware(DefaultMaxBodySize))

    // Public routes
    publicRouter := r.PathPrefix("/api/").Subrouter()
    publicRouter.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]bool{"ok": true})
    })

    // OAuth routes with smaller limit
    oauthRouter := r.PathPrefix("/api/").Subrouter()
    oauthRouter.Use(RequestSizeLimitMiddleware(OAuthCallbackMaxBodySize))
    oauthRouter.HandleFunc("/glink", GoogleAccountLinkingHandler).Methods("GET")

    // Protected routes (if Issue #3 implemented, add AuthMiddleware)
    protectedRouter := r.PathPrefix("/api/").Subrouter()
    // protectedRouter.Use(AuthMiddleware(jwtManager))

    // Scan endpoints with larger limit
    scanRouter := protectedRouter.PathPrefix("/scans").Subrouter()
    scanRouter.Use(RequestSizeLimitMiddleware(ScanRequestMaxBodySize))
    scanRouter.HandleFunc("", DoScansHandler).Methods("POST")

    // Other scan endpoints (GET) use default limit
    scanRouter.HandleFunc("", ListScansHandler).Methods("GET").Queries("page", "{page}")
    scanRouter.HandleFunc("", ListScansHandler).Methods("GET")
    scanRouter.HandleFunc("/requests/{account_key}", GetScanRequestsHandler).Methods("GET")
    scanRouter.HandleFunc("/accounts", GetAccountsHandler).Methods("GET")
    scanRouter.HandleFunc("/{scan_id}", DeleteScanHandler).Methods("DELETE")
    scanRouter.HandleFunc("/{scan_id}", ListScanDataHandler).Methods("GET").Queries("page", "{page}")
    scanRouter.HandleFunc("/{scan_id}", ListScanDataHandler).Methods("GET")

    // Other protected routes
    protectedRouter.HandleFunc("/accounts", GetRequestAccountsHandler).Methods("GET")
    protectedRouter.HandleFunc("/gmaildata/{scan_id}", ListMessageMetaDataHandler).Methods("GET").Queries("page", "{page}")
    protectedRouter.HandleFunc("/gmaildata/{scan_id}", ListMessageMetaDataHandler).Methods("GET")
    protectedRouter.HandleFunc("/photos/albums", ListAlbumsHandler).Methods("GET").Queries("refresh_token", "{refresh_token}")
    protectedRouter.HandleFunc("/photos/{scan_id}", ListPhotosHandler).Methods("GET").Queries("page", "{page}")
    protectedRouter.HandleFunc("/photos/{scan_id}", ListPhotosHandler).Methods("GET")

    // SSE endpoint
    sseRouter := r.PathPrefix("/events").Subrouter()
    // SSE doesn't have request body, use small form limit
    sseRouter.Use(RequestSizeLimitMiddleware(FormDataMaxBodySize))
    // sseRouter.Use(AuthMiddleware(jwtManager))
    sseRouter.HandleFunc("", sseHandler).Methods("GET")

    // CORS
    cors := cors.New(cors.Options{
        AllowedOrigins:   []string{constants.FrontendUrl},
        AllowCredentials: true,
        AllowedHeaders:   []string{"Content-Type", "Authorization"},
        AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
    })
    handler := cors.Handler(r)

    srv := &http.Server{
        Handler:      handler,
        Addr:         ":8090",
        WriteTimeout: 10 * time.Second,
        ReadTimeout:  10 * time.Second,
    }

    slog.Info("Server listening on :8090")
    log.Fatal(srv.ListenAndServe())
}
```

### 3.5 Configuration Management

**Optional: Make limits configurable via environment variables**

```go
// web/config.go
package web

import (
    "os"
    "strconv"
)

type SizeLimits struct {
    Default       int64
    ScanRequest   int64
    OAuthCallback int64
    FormData      int64
}

func GetSizeLimits() SizeLimits {
    return SizeLimits{
        Default:       getEnvInt64("MAX_BODY_SIZE_DEFAULT", DefaultMaxBodySize),
        ScanRequest:   getEnvInt64("MAX_BODY_SIZE_SCAN", ScanRequestMaxBodySize),
        OAuthCallback: getEnvInt64("MAX_BODY_SIZE_OAUTH", OAuthCallbackMaxBodySize),
        FormData:      getEnvInt64("MAX_BODY_SIZE_FORM", FormDataMaxBodySize),
    }
}

func getEnvInt64(key string, defaultValue int64) int64 {
    if value := os.Getenv(key); value != "" {
        if i, err := strconv.ParseInt(value, 10, 64); err == nil {
            return i
        }
    }
    return defaultValue
}
```

---

## 4. Testing Strategy

### 4.1 Unit Tests

**`web/middleware_test.go`**

```go
package web

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strings"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestRequestSizeLimitMiddleware_WithinLimit(t *testing.T) {
    // Create a handler that just echoes back
    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("OK"))
    })

    // Wrap with size limit middleware (1 KB limit)
    middleware := RequestSizeLimitMiddleware(1024)
    wrapped := middleware(handler)

    // Create request with 512 bytes (within limit)
    body := strings.Repeat("a", 512)
    req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(body))
    w := httptest.NewRecorder()

    wrapped.ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
    assert.Equal(t, "OK", w.Body.String())
}

func TestRequestSizeLimitMiddleware_ExceedsLimit(t *testing.T) {
    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Try to decode JSON
        var data map[string]interface{}
        decoder := json.NewDecoder(r.Body)
        err := decoder.Decode(&data)

        if handleMaxBytesError(w, r, err, 1024) {
            return
        }

        w.WriteHeader(http.StatusOK)
    })

    middleware := RequestSizeLimitMiddleware(1024)
    wrapped := middleware(handler)

    // Create request with 2 KB (exceeds 1 KB limit)
    body := strings.Repeat("a", 2048)
    req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    wrapped.ServeHTTP(w, req)

    assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

    var errResp ErrorResponse
    err := json.Unmarshal(w.Body.Bytes(), &errResp)
    require.NoError(t, err)
    assert.Equal(t, "PAYLOAD_TOO_LARGE", errResp.Error.Code)
    assert.Contains(t, errResp.Error.Message, "exceeds maximum")
}

func TestRequestSizeLimitMiddleware_ExactLimit(t *testing.T) {
    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })

    middleware := RequestSizeLimitMiddleware(1024)
    wrapped := middleware(handler)

    // Exactly 1024 bytes
    body := strings.Repeat("a", 1024)
    req := httptest.NewRequest("POST", "/test", bytes.NewBufferString(body))
    w := httptest.NewRecorder()

    wrapped.ServeHTTP(w, req)

    // Should succeed (at the limit)
    assert.Equal(t, http.StatusOK, w.Code)
}

func TestFormatBytes(t *testing.T) {
    tests := []struct {
        bytes    int64
        expected string
    }{
        {512, "512 B"},
        {1024, "1.0 KB"},
        {1536, "1.5 KB"},
        {1048576, "1.0 MB"},
        {1572864, "1.5 MB"},
        {5242880, "5.0 MB"},
    }

    for _, tt := range tests {
        t.Run(tt.expected, func(t *testing.T) {
            result := formatBytes(tt.bytes)
            assert.Equal(t, tt.expected, result)
        })
    }
}

func TestHandleMaxBytesError_NotSizeError(t *testing.T) {
    w := httptest.NewRecorder()
    r := httptest.NewRequest("POST", "/test", nil)

    // Non-size-limit error
    err := fmt.Errorf("some other error")

    handled := handleMaxBytesError(w, r, err, 1024)

    assert.False(t, handled)
    assert.Equal(t, http.StatusOK, w.Code) // No response written
}

func TestHandleMaxBytesError_NilError(t *testing.T) {
    w := httptest.NewRecorder()
    r := httptest.NewRequest("POST", "/test", nil)

    handled := handleMaxBytesError(w, r, nil, 1024)

    assert.False(t, handled)
}
```

### 4.2 Integration Tests

**`web/api_test.go`** (add to existing or create)

```go
package web

func TestDoScansHandler_OversizedRequest(t *testing.T) {
    // Setup
    router := mux.NewRouter()
    scanRouter := router.PathPrefix("/api/scans").Subrouter()
    scanRouter.Use(RequestSizeLimitMiddleware(ScanRequestMaxBodySize))
    scanRouter.HandleFunc("", DoScansHandler).Methods("POST")

    // Create oversized request (2 MB, exceeds 1 MB limit)
    largeJSON := map[string]interface{}{
        "ScanType": "Local",
        "LocalScan": map[string]interface{}{
            "Source": strings.Repeat("a", 2*1024*1024), // 2 MB string
        },
    }
    body, _ := json.Marshal(largeJSON)

    req := httptest.NewRequest("POST", "/api/scans", bytes.NewBuffer(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    router.ServeHTTP(w, req)

    // Should return 413
    assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

    var errResp ErrorResponse
    err := json.Unmarshal(w.Body.Bytes(), &errResp)
    require.NoError(t, err)
    assert.Equal(t, "PAYLOAD_TOO_LARGE", errResp.Error.Code)
}

func TestDoScansHandler_NormalRequest(t *testing.T) {
    router := mux.NewRouter()
    scanRouter := router.PathPrefix("/api/scans").Subrouter()
    scanRouter.Use(RequestSizeLimitMiddleware(ScanRequestMaxBodySize))
    scanRouter.HandleFunc("", DoScansHandler).Methods("POST")

    // Normal sized request
    scanRequest := DoScanRequest{
        ScanType: "Local",
        LocalScan: collect.LocalScan{
            Source: "/tmp/test",
        },
    }
    body, _ := json.Marshal(scanRequest)

    req := httptest.NewRequest("POST", "/api/scans", bytes.NewBuffer(body))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    router.ServeHTTP(w, req)

    // Should process normally (may fail on auth/db, but not on size)
    assert.NotEqual(t, http.StatusRequestEntityTooLarge, w.Code)
}
```

### 4.3 Manual Testing

**Test with curl:**

```bash
# Test 1: Normal request (should succeed)
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Source":"/tmp/test"}}'

# Expected: 200 OK or auth error (depending on Issue #3 status)

# Test 2: Oversized request (should fail with 413)
# Create 2 MB file
dd if=/dev/zero of=/tmp/large.json bs=1M count=2

curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d @/tmp/large.json

# Expected: 413 Payload Too Large with JSON error

# Test 3: At the limit (should succeed)
# Create exactly 1 MB payload
python3 -c "import json; print(json.dumps({'ScanType':'Local','LocalScan':{'Source':'a'*1048000}}))" > /tmp/1mb.json

curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d @/tmp/1mb.json

# Expected: Should process (may fail on validation, but not size)

# Test 4: Stress test - multiple oversized requests
for i in {1..10}; do
  curl -X POST http://localhost:8090/api/scans \
    -H "Content-Type: application/json" \
    -d @/tmp/large.json &
done
wait

# Expected: All return 413, server remains responsive
# Check server logs for oversized request warnings
```

### 4.4 Load Testing

**Using `vegeta` for load testing:**

```bash
# Install vegeta
go install github.com/tsenart/vegeta@latest

# Create attack file
cat > attack.txt <<EOF
POST http://localhost:8090/api/scans
Content-Type: application/json
@/tmp/large.json

EOF

# Run load test
echo "POST http://localhost:8090/api/scans" | \
  vegeta attack -duration=10s -rate=50 -body=/tmp/large.json -header="Content-Type: application/json" | \
  vegeta report

# Expected results:
# - All requests should return 413
# - Server should remain responsive
# - Memory usage should remain stable
```

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Code review completed
- [ ] Unit tests passing
- [ ] Integration tests passing
- [ ] Manual testing completed
- [ ] Load testing shows stable memory usage
- [ ] Documentation updated
- [ ] Rollback plan prepared

### 5.2 Deployment Steps

**Step 1: Deploy to Staging**

```bash
# Build
cd be
go build -o hdd

# Test size limits
curl -X POST http://staging:8090/api/scans \
  -H "Content-Type: application/json" \
  -d @large_test.json

# Verify 413 response
```

**Step 2: Monitor Staging**

```bash
# Watch logs for size limit warnings
tail -f /var/log/bhandaar/app.log | grep "size limit exceeded"

# Monitor memory usage
watch -n 1 'ps aux | grep hdd | grep -v grep'
```

**Step 3: Deploy to Production**

```bash
# Deploy new binary
systemctl stop bhandaar
cp hdd /usr/local/bin/hdd
systemctl start bhandaar

# Verify service is running
systemctl status bhandaar

# Test endpoint
curl -X GET http://localhost:8090/api/health
```

**Step 4: Post-Deployment Verification**

```bash
# Test normal request
curl -X POST http://production:8090/api/scans \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Source":"/tmp/test"}}'

# Expected: Normal operation

# Test oversized request (from trusted IP)
curl -X POST http://production:8090/api/scans \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d @large_test.json

# Expected: 413 with JSON error
```

### 5.3 Rollback Plan

If issues are detected:

```bash
# Stop current service
systemctl stop bhandaar

# Restore previous binary
cp /usr/local/bin/hdd.backup /usr/local/bin/hdd

# Restart
systemctl start bhandaar

# Verify
systemctl status bhandaar
curl http://localhost:8090/api/health
```

---

## 6. Monitoring and Alerts

### 6.1 Metrics to Track

**Application Metrics:**
- Count of 413 responses (oversized requests)
- Average request body size per endpoint
- Peak request body size per hour
- Memory usage before/after implementation

**Log Aggregation (if using ELK/Splunk):**

```
# Query for oversized requests
log.level:WARN AND message:"Request body size limit exceeded"

# Dashboard showing:
# - Count of oversized requests over time
# - Top IPs triggering size limits
# - Most affected endpoints
```

### 6.2 Alert Rules

**Prometheus Alert Rules:**

```yaml
groups:
  - name: request_size_limits
    rules:
      - alert: HighOversizedRequestRate
        expr: rate(http_requests_total{status="413"}[5m]) > 10
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High rate of oversized requests"
          description: "{{ $value }} oversized requests per second from {{ $labels.remote_addr }}"

      - alert: PossibleDoSAttack
        expr: rate(http_requests_total{status="413"}[1m]) > 100
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Possible DoS attack detected"
          description: "{{ $value }} oversized requests per second - possible attack"
```

### 6.3 Logging Examples

**Successful Request (Normal):**
```
2025-12-21T10:30:00Z INFO Received scan request scan_type=Local body_size_estimate=1234
```

**Oversized Request (Blocked):**
```
2025-12-21T10:30:01Z WARN Request body size limit exceeded remote_addr=192.168.1.100 user_agent=curl/7.68.0 method=POST path=/api/scans max_bytes=1048576 max_human="1.0 MB"
```

**Attack Pattern (Multiple oversized requests):**
```
2025-12-21T10:30:01Z WARN Request body size limit exceeded remote_addr=192.168.1.100 ...
2025-12-21T10:30:02Z WARN Request body size limit exceeded remote_addr=192.168.1.100 ...
2025-12-21T10:30:03Z WARN Request body size limit exceeded remote_addr=192.168.1.100 ...
[... repeated 50 times in 1 minute ...]
```

### 6.4 Dashboard Metrics

**Grafana Dashboard Panels:**

1. **Request Size Distribution**
   - Histogram of request body sizes
   - P50, P95, P99 percentiles

2. **413 Responses Over Time**
   - Time series graph
   - Grouped by endpoint

3. **Top IPs by 413 Responses**
   - Table showing potential attackers

4. **Memory Usage Comparison**
   - Before/after implementation
   - Should show more stable memory

---

## 7. Additional Improvements (Future)

### 7.1 Rate Limiting Integration

Combine with rate limiting (from Issue #15):

```go
// Penalize IPs that repeatedly send oversized requests
func RequestSizeLimitMiddleware(maxBytes int64) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

            // ... existing code ...

            // If oversized, count against rate limit
            if isOversized {
                rateLimiter.RecordViolation(r.RemoteAddr)
            }

            next.ServeHTTP(w, r)
        })
    }
}
```

### 7.2 Dynamic Limits Based on User

After authentication (Issue #3):

```go
func getUserSizeLimit(userID uuid.UUID) int64 {
    // Premium users get higher limits
    user, _ := db.GetUserByID(userID)
    if user.IsPremium {
        return 10 << 20  // 10 MB
    }
    return ScanRequestMaxBodySize  // 1 MB
}
```

### 7.3 Endpoint-Specific Telemetry

```go
// Track size statistics per endpoint
type SizeStats struct {
    Endpoint   string
    AvgSize    int64
    MaxSize    int64
    P95Size    int64
    TotalCount int64
}
```

---

## 8. Security Considerations

### 8.1 Defense in Depth

**Multiple Layers:**
1. ✅ Reverse proxy (nginx/HAProxy) - First line of defense
2. ✅ Application middleware - This implementation
3. ✅ Handler validation - Field-level checks
4. ⏳ Rate limiting - Prevent repeated attacks (Issue #15)
5. ⏳ Authentication - Identify attackers (Issue #3)

### 8.2 Nginx Configuration (Optional)

Add to nginx as additional layer:

```nginx
server {
    listen 80;
    server_name your-domain.com;

    # Global limit
    client_max_body_size 2M;

    location /api/scans {
        # Higher limit for scan endpoint
        client_max_body_size 2M;
        proxy_pass http://localhost:8090;
    }

    location /api/glink {
        # Lower limit for OAuth
        client_max_body_size 16k;
        proxy_pass http://localhost:8090;
    }

    location /api/ {
        # Default for other endpoints
        client_max_body_size 512k;
        proxy_pass http://localhost:8090;
    }
}
```

### 8.3 Attack Scenarios and Mitigations

| Attack | Mitigation |
|--------|------------|
| Single large request | ✅ Size limit blocks it |
| Many large requests | ✅ Size limit + rate limiting |
| Slowloris (slow POST) | ✅ ReadTimeout in http.Server |
| Gzip bomb | ⚠️ Need decompression limit |
| Multipart bomb | ⚠️ Need multipart size limit |

**Additional Hardening:**

```go
srv := &http.Server{
    Addr:         ":8090",
    Handler:      handler,
    ReadTimeout:  10 * time.Second,   // Prevent slow reads
    WriteTimeout: 10 * time.Second,   // Prevent slow writes
    IdleTimeout:  120 * time.Second,  // Close idle connections
    MaxHeaderBytes: 1 << 20,          // 1 MB max headers
}
```

---

## Appendix A: Complete File Changes Summary

### Files to Modify

1. **`web/middleware.go`** - Create or update
   - Add `RequestSizeLimitMiddleware`
   - Add `handleMaxBytesError`
   - Add `ErrorResponse` types
   - Add `formatBytes` helper

2. **`web/api.go`** - Update
   - Add `handleMaxBytesError` check to `DoScansHandler`

3. **`web/oauth.go`** - Update
   - Add `handleMaxBytesError` check to form parsing

4. **`web/web_server.go`** - Update
   - Apply middleware to routes
   - Configure different limits per route

### Files to Create

1. **`web/middleware_test.go`** - New
   - Unit tests for middleware

2. **`web/config.go`** - Optional
   - Environment-based configuration

### No Changes Needed

- ✅ `db/database.go` - No changes
- ✅ `collect/*.go` - No changes
- ✅ `notification/*.go` - No changes

---

## Appendix B: Testing Checklist

### Unit Tests
- [ ] Middleware blocks oversized requests
- [ ] Middleware allows requests within limit
- [ ] Middleware allows requests at exact limit
- [ ] Error response format is correct
- [ ] Logging includes all required fields
- [ ] formatBytes works correctly

### Integration Tests
- [ ] DoScansHandler blocks oversized requests
- [ ] DoScansHandler processes normal requests
- [ ] OAuth handler blocks oversized forms
- [ ] GET endpoints not affected

### Manual Tests
- [ ] curl with oversized body returns 413
- [ ] curl with normal body works
- [ ] Response has correct JSON error format
- [ ] Server logs oversized attempts

### Load Tests
- [ ] 100 oversized requests don't crash server
- [ ] Memory usage remains stable
- [ ] Server remains responsive

### Security Tests
- [ ] Cannot exhaust memory with large request
- [ ] Cannot bypass limit with chunked encoding
- [ ] Cannot bypass limit with compression

---

## Appendix C: Useful Commands

### Generate Test Files

```bash
# Create 1 MB test file
dd if=/dev/zero of=/tmp/1mb.bin bs=1M count=1

# Create 2 MB test file
dd if=/dev/zero of=/tmp/2mb.bin bs=1M count=2

# Create valid JSON at size limit
python3 -c "
import json
data = {
    'ScanType': 'Local',
    'LocalScan': {
        'Source': 'a' * 1048000  # ~1 MB
    }
}
print(json.dumps(data))
" > /tmp/1mb.json
```

### Monitor Memory Usage

```bash
# Watch server memory
watch -n 1 'ps aux | grep hdd | grep -v grep | awk "{print \$6/1024\" MB\"}"'

# Monitor during load test
while true; do
  ps aux | grep hdd | grep -v grep | awk '{print strftime("%Y-%m-%d %H:%M:%S"), $6/1024" MB"}'
  sleep 1
done
```

### Test Different Sizes

```bash
# Helper function to test different sizes
test_size() {
  SIZE=$1
  echo "Testing ${SIZE} bytes..."

  python3 -c "print('a' * $SIZE)" | \
    curl -X POST http://localhost:8090/api/scans \
      -H "Content-Type: application/json" \
      -d @- \
      -w "\nHTTP Status: %{http_code}\n" \
      -s
}

# Test various sizes
test_size 1000      # 1 KB - should succeed
test_size 524288    # 512 KB - should succeed
test_size 1048576   # 1 MB - at limit, should succeed
test_size 1048577   # Just over 1 MB - should fail with 413
test_size 10485760  # 10 MB - should fail with 413
```

---

**END OF DOCUMENT**
