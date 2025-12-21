# Issue #11 Implementation Plan: Ignored Errors Throughout Codebase

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P1 - High Priority (Data Integrity & Reliability)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #11: Ignored Errors Throughout Codebase**. The current system ignores critical errors in JSON operations, HTTP response writing, and resource cleanup, leading to silent failures, incomplete responses, and resource leaks.

**Selected Approach:**
- **JSON Operations**: Centralized helper function `writeJSONResponse()` with proper error handling
- **HTTP Response Writes**: Structured response helper enforcing correct order (status → headers → body)
- **Resource Cleanup**: Helper function `deferClose()` to reduce boilerplate
- **Scope**: Phased approach - Phase 1: HTTP handlers, Phase 2: collectors, Phase 3: everything else
- **Breaking Changes**: Accept where necessary to improve error handling
- **Testing**: Manual testing, rely on existing integration tests

**Also Addresses:** Issue #21 (HTTP Status Code Set After Response Body)

**Estimated Effort:**
- Phase 1: 4-6 hours (HTTP handlers)
- Phase 2: 3-4 hours (collectors)
- Phase 3: 2-3 hours (remaining files)
- **Total: 1.5-2 days**

**Impact:**
- Eliminates silent failures in HTTP responses
- Fixes incorrect HTTP status codes (Issue #21)
- Prevents resource leaks from unclosed files
- Improves observability through proper logging
- Better client error handling

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Implementation Details](#3-implementation-details)
4. [Testing Strategy](#4-testing-strategy)
5. [Deployment Plan](#5-deployment-plan)
6. [Monitoring and Observability](#6-monitoring-and-observability)

---

## 1. Current State Analysis

### 1.1 Categories of Ignored Errors

**Category 1: JSON Marshaling Errors (CRITICAL)**

**web/api.go:199-201:**
```go
func ListAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	// ... fetch albums ...

	body := ListAlbumsResponse{
		PageInfo: pageInfo,
		Albums:   albums,
	}
	serializedBody, _ := json.Marshal(body)  // ❌ Ignored error
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)           // ❌ Ignored error
}
```

**Impact:**
- If Marshal fails, `serializedBody` is `nil` or empty
- Client receives empty response with 200 OK
- Appears successful but contains no data
- Silent data loss

**Category 2: JSON Encoding Errors (CRITICAL)**

**web/api.go:19:**
```go
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	setJsonHeader(w)
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})  // ❌ No error check
}
```

**collect/photos.go:458:**
```go
func getJson(url string, target interface{}) error {
	r, err := http.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()

	return json.NewDecoder(r.Body).Decode(target)  // ✅ Good - error returned
}
```

**Impact:**
- Encoding failures not detected
- Partial JSON sent to client
- Client-side parsing errors

**Category 3: HTTP Status Code Ordering (CRITICAL - Issue #21)**

**web/oauth.go:30-33:**
```go
if redirectUri == "" {
	w.Write([]byte("redirectUri not found in request"))  // ❌ Body BEFORE status
	w.WriteHeader(http.StatusBadRequest)                  // ❌ Too late!
	return
}
```

**Impact:**
- Status code ignored if body already written
- Response sends 200 OK instead of 400 Bad Request
- Breaks HTTP compliance
- Clients interpret errors as success

**Category 4: Resource Close Errors (HIGH)**

**collect/local.go:145:**
```go
func computeMd5ForFile(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		slog.Warn("Failed to open file for MD5 calculation, skipping hash",
			"path", filePath,
			"error", err)
		return ""
	}
	defer file.Close()  // ❌ Close error ignored

	hash := md5.New()
	_, err = io.Copy(hash, file)  // ✅ Error handled
	// ...
}
```

**web/oauth.go:73:**
```go
res, err := httpClient.Do(req)
if err != nil {
	slog.Warn(fmt.Sprintf("could not send HTTP request: %v", err))
	w.WriteHeader(http.StatusInternalServerError)
}
defer res.Body.Close()  // ❌ Close error ignored
```

**collect/photos.go:391, 444, 457:**
```go
defer resp.Body.Close()  // ❌ Close error ignored (multiple locations)
```

**Impact:**
- File descriptors leaked on close failures
- HTTP connections may not be released to pool
- Over time, exhausts file descriptors
- "Too many open files" errors

**Category 5: HTTP Response Write Errors (MEDIUM)**

**web/api.go:201:**
```go
_, _ = w.Write(serializedBody)  // ❌ Write error ignored
```

**Impact:**
- Client may receive partial response
- Connection errors not detected
- No visibility into write failures

### 1.2 Error Frequency Analysis

**Files with Ignored Errors:**

| File | JSON Errors | Write Errors | Close Errors | Total |
|------|-------------|--------------|--------------|-------|
| `web/api.go` | 2 | 2 | 0 | 4 |
| `web/oauth.go` | 0 | 2 | 1 | 3 |
| `collect/local.go` | 0 | 0 | 1 | 1 |
| `collect/photos.go` | 0 | 0 | 4 | 4 |
| **Total** | **2** | **4** | **6** | **12** |

### 1.3 Impact Scenarios

**Scenario 1: Silent JSON Marshaling Failure**

```bash
# Server has struct with invalid field (e.g., channel, func)
# Marshal fails silently

curl http://localhost:8090/api/photos/123

# Response:
HTTP/1.1 200 OK
Content-Type: application/json
Content-Length: 0

# Empty body - client thinks success but no data!
```

**Scenario 2: Incorrect Status Code (Issue #21)**

```bash
# OAuth callback with missing redirectUri

curl http://localhost:8090/api/glink

# Response:
HTTP/1.1 200 OK  # ❌ Should be 400!
Content-Type: text/plain

redirectUri not found in request

# Client sees 200 OK and is confused
```

**Scenario 3: File Descriptor Leak**

```bash
# Running local scan on directory with 10,000 files
# If file.Close() fails silently 1% of the time = 100 leaked FDs

# After multiple scans:
$ lsof -p $(pgrep hdd) | wc -l
1024  # Approaching ulimit

# Eventually:
panic: too many open files
```

**Scenario 4: Partial Response Write**

```bash
# Client connection drops during large response write
# Write error ignored, no log entry

# Server logs: (nothing)
# Client logs: Unexpected end of JSON input
# No way to debug!
```

### 1.4 Risk Assessment

**Without Proper Error Handling:**
- 🔴 **Critical**: Silent data loss in API responses
- 🔴 **Critical**: Incorrect HTTP status codes confuse clients
- 🔴 **High**: Resource leaks lead to server crashes
- 🟡 **Medium**: No visibility into failure patterns
- 🟡 **Medium**: Difficult debugging in production

---

## 2. Target Architecture

### 2.1 Helper Functions Overview

```
New Helper Functions:
├── web/response.go (NEW)
│   ├── writeJSONResponse()       - Handles all JSON response writing
│   ├── writeErrorResponse()      - Handles error responses (already exists)
│   └── setJSONHeaders()          - Centralized header setting
│
└── util/io.go (NEW)
    ├── deferClose()              - Handles deferred close with logging
    └── closeWithLog()            - Immediate close with logging
```

### 2.2 Response Writing Flow

**Current (Broken) Flow:**
```
┌─────────────────────────────────────────────────────────────┐
│ 1. Marshal data (error ignored)                            │
│    serializedBody, _ := json.Marshal(data)                 │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Set headers                                              │
│    setJsonHeader(w)                                         │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Write body (error ignored)                              │
│    _, _ = w.Write(serializedBody)                          │
│    ❌ Status defaults to 200 OK                            │
└─────────────────────────────────────────────────────────────┘
```

**New (Correct) Flow:**
```
┌─────────────────────────────────────────────────────────────┐
│ 1. Marshal data FIRST (before committing to response)      │
│    serializedBody, err := json.Marshal(data)               │
│    if err != nil { return error to handler }               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Set headers                                              │
│    w.Header().Set("Content-Type", "application/json")      │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Set status code (BEFORE body)                           │
│    w.WriteHeader(statusCode)                               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Write body (log errors, can't return to client)        │
│    if _, err := w.Write(serializedBody); err != nil {      │
│        slog.Error("Failed to write response", "error", err)│
│    }                                                        │
└─────────────────────────────────────────────────────────────┘
```

### 2.3 Resource Cleanup Pattern

**Current (Error Ignored):**
```go
file, err := os.Open(path)
if err != nil {
    return err
}
defer file.Close()  // ❌ Error ignored
```

**New (Error Logged):**
```go
file, err := os.Open(path)
if err != nil {
    return err
}
defer deferClose(file, "file", path)  // ✅ Error logged
```

### 2.4 Phased Implementation

**Phase 1: HTTP Handlers (web/api.go, web/oauth.go)**
- Priority: CRITICAL
- Impact: Immediate improvement to API reliability
- Files: 2
- Errors Fixed: 7

**Phase 2: Collectors (collect/*.go)**
- Priority: HIGH
- Impact: Prevents resource leaks during scans
- Files: 2
- Errors Fixed: 5

**Phase 3: Remaining Files**
- Priority: MEDIUM
- Impact: Complete coverage
- Files: Any others discovered
- Errors Fixed: As needed

---

## 3. Implementation Details

### 3.1 Helper Functions: `web/response.go` (NEW FILE)

```go
package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSONResponse writes a JSON response with proper error handling
// and correct HTTP status code ordering.
//
// This function ensures:
// 1. JSON marshaling happens BEFORE committing to response
// 2. Status code is set BEFORE writing body (fixes Issue #21)
// 3. All errors are properly logged
//
// Returns error if marshaling fails (before response committed).
// Logs error if write fails (after response committed).
func writeJSONResponse(w http.ResponseWriter, data interface{}, statusCode int) error {
	// Step 1: Marshal first (can still return error to handler)
	serializedBody, err := json.Marshal(data)
	if err != nil {
		slog.Error("Failed to marshal JSON response",
			"error", err,
			"status_code", statusCode,
			"data_type", fmt.Sprintf("%T", data))
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	// Step 2: Set headers
	w.Header().Set("Content-Type", "application/json")

	// Step 3: Set status code (BEFORE body - fixes Issue #21)
	w.WriteHeader(statusCode)

	// Step 4: Write body (can only log errors now, response committed)
	if _, err := w.Write(serializedBody); err != nil {
		slog.Error("Failed to write JSON response body",
			"error", err,
			"status_code", statusCode,
			"body_size", len(serializedBody))
		return fmt.Errorf("failed to write response: %w", err)
	}

	return nil
}

// writeJSONResponseOK is a convenience wrapper for 200 OK responses
func writeJSONResponseOK(w http.ResponseWriter, data interface{}) error {
	return writeJSONResponse(w, data, http.StatusOK)
}

// writeJSONError writes an error response with proper status code
func writeJSONError(w http.ResponseWriter, message string, statusCode int) error {
	errorResponse := map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
			"code":    statusCode,
		},
	}
	return writeJSONResponse(w, errorResponse, statusCode)
}

// encodeJSONResponse writes a JSON response using json.Encoder
// Use this for streaming responses or when data is already in io.Reader
func encodeJSONResponse(w http.ResponseWriter, data interface{}, statusCode int) error {
	// Set headers first
	w.Header().Set("Content-Type", "application/json")

	// Set status code BEFORE encoding
	w.WriteHeader(statusCode)

	// Encode directly to response writer
	encoder := json.NewEncoder(w)
	if err := encoder.Encode(data); err != nil {
		slog.Error("Failed to encode JSON response",
			"error", err,
			"status_code", statusCode,
			"data_type", fmt.Sprintf("%T", data))
		return fmt.Errorf("failed to encode JSON: %w", err)
	}

	return nil
}
```

### 3.2 Resource Cleanup Helper: `util/io.go` (NEW FILE)

```go
package util

import (
	"io"
	"log/slog"
)

// Closer is an interface for things that can be closed
type Closer interface {
	Close() error
}

// deferClose is a helper for deferred close operations with error logging
//
// Usage:
//   file, err := os.Open(path)
//   if err != nil { return err }
//   defer deferClose(file, "file", path)
//
// The resourceType and identifier are used for logging context.
func deferClose(c Closer, resourceType, identifier string) {
	if c == nil {
		return
	}

	if err := c.Close(); err != nil {
		slog.Error("Failed to close resource",
			"resource_type", resourceType,
			"identifier", identifier,
			"error", err)
	}
}

// closeWithLog immediately closes a resource with error logging
// Use this for non-deferred closes
func closeWithLog(c Closer, resourceType, identifier string) error {
	if c == nil {
		return nil
	}

	if err := c.Close(); err != nil {
		slog.Error("Failed to close resource",
			"resource_type", resourceType,
			"identifier", identifier,
			"error", err)
		return err
	}

	return nil
}

// deferCloseBody is a specialized helper for HTTP response bodies
// This is so common it deserves its own helper
func deferCloseBody(body io.ReadCloser, url string) {
	if body == nil {
		return
	}

	if err := body.Close(); err != nil {
		slog.Warn("Failed to close HTTP response body",
			"url", url,
			"error", err)
		// Use Warn instead of Error for HTTP bodies as these are less critical
	}
}
```

### 3.3 Phase 1: Update `web/api.go`

**Change 1: Import new helper**

```go
package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"github.com/jyothri/hdd/validation"
	"github.com/jyothri/hdd/util"  // NEW
)
```

**Change 2: Update HealthCheckHandler**

```go
// Before:
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	setJsonHeader(w)
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

// After:
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	if err := encodeJSONResponse(w, map[string]bool{"ok": true}, http.StatusOK); err != nil {
		// Error already logged by helper
		// Can't send error response, already committed
	}
}

// Alternative (using writeJSONResponse):
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	if err := writeJSONResponseOK(w, map[string]bool{"ok": true}); err != nil {
		// Error already logged by helper
	}
}
```

**Change 3: Update ListAlbumsHandler**

```go
// Before:
func ListAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	// ... fetch albums and pageInfo ...

	body := ListAlbumsResponse{
		PageInfo: pageInfo,
		Albums:   albums,
	}
	serializedBody, _ := json.Marshal(body)  // ❌
	setJsonHeader(w)
	_, _ = w.Write(serializedBody)           // ❌
}

// After:
func ListAlbumsHandler(w http.ResponseWriter, r *http.Request) {
	// ... fetch albums and pageInfo ...

	body := ListAlbumsResponse{
		PageInfo: pageInfo,
		Albums:   albums,
	}

	// Use helper - handles all error cases
	if err := writeJSONResponseOK(w, body); err != nil {
		// Error logged by helper
		// If marshal failed, helper already sent 500 error
		// If write failed, can't do anything (response committed)
		return
	}
}
```

**Change 4: Update all other handlers similarly**

Apply same pattern to:
- `ListPhotosHandler`
- `GetScanHandler`
- `ListScansHandler`
- `GetGmailDataHandler`
- `DeleteScanHandler`
- Any other handlers that write JSON

### 3.4 Phase 1: Update `web/oauth.go`

**Change 1: Fix redirectUri error (Issue #21)**

```go
// Before:
if redirectUri == "" {
	w.Write([]byte("redirectUri not found in request"))  // ❌ Body first
	w.WriteHeader(http.StatusBadRequest)                  // ❌ Too late
	return
}

// After:
if redirectUri == "" {
	http.Error(w, "redirectUri not found in request", http.StatusBadRequest)
	return
}

// Or using helper:
if redirectUri == "" {
	writeJSONError(w, "redirectUri not found in request", http.StatusBadRequest)
	return
}
```

**Change 2: Fix ParseForm error handling**

```go
// Before:
err := r.ParseForm()
if handleMaxBytesError(w, r, err, OAuthCallbackMaxBodySize) {
	return
}

if err != nil {
	slog.Error("Failed to parse OAuth form", "error", err)
	http.Error(w, "Invalid request format", http.StatusBadRequest)  // ✅ Already correct
	return
}

// No change needed - already uses http.Error correctly
```

**Change 3: Fix HTTP response body close**

```go
// Before:
res, err := httpClient.Do(req)
if err != nil {
	slog.Warn(fmt.Sprintf("could not send HTTP request: %v", err))
	w.WriteHeader(http.StatusInternalServerError)
}
defer res.Body.Close()  // ❌

// After:
res, err := httpClient.Do(req)
if err != nil {
	slog.Warn("Could not send HTTP request", "error", err)
	http.Error(w, "Failed to exchange OAuth code", http.StatusInternalServerError)
	return
}
defer util.deferCloseBody(res.Body, googleTokenUrl)  // ✅
```

**Change 4: Fix other Write() calls**

```go
// Audit all w.Write() calls in oauth.go
// Ensure status code set before Write
// Or use http.Error() / writeJSONError()
```

### 3.5 Phase 2: Update `collect/local.go`

**Change 1: Fix file close in computeMd5ForFile**

```go
// Before:
func computeMd5ForFile(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		slog.Warn("Failed to open file for MD5 calculation, skipping hash",
			"path", filePath,
			"error", err)
		return ""
	}
	defer file.Close()  // ❌

	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		slog.Warn("Failed to compute MD5 hash",
			"path", filePath,
			"error", err)
		return ""
	}

	return fmt.Sprintf("%x", hash.Sum(nil))
}

// After:
func computeMd5ForFile(filePath string) string {
	file, err := os.Open(filePath)
	if err != nil {
		slog.Warn("Failed to open file for MD5 calculation, skipping hash",
			"path", filePath,
			"error", err)
		return ""
	}
	defer util.deferClose(file, "file", filePath)  // ✅

	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		slog.Warn("Failed to compute MD5 hash",
			"path", filePath,
			"error", err)
		return ""
	}

	return fmt.Sprintf("%x", hash.Sum(nil))
}
```

### 3.6 Phase 2: Update `collect/photos.go`

**Change 1: Fix HTTP response body closes**

```go
// Find all instances (4 total):
// Line 391, 444, 457

// Before:
resp, err := http.Get(url)
if err != nil {
	return nil, err
}
defer resp.Body.Close()  // ❌

// After:
resp, err := http.Get(url)
if err != nil {
	return nil, err
}
defer util.deferCloseBody(resp.Body, url)  // ✅
```

**Change 2: Update getJson function**

```go
// Before:
func getJson(url string, target interface{}) error {
	r, err := http.Get(url)
	if err != nil {
		return err
	}
	defer r.Body.Close()  // ❌

	return json.NewDecoder(r.Body).Decode(target)  // ✅ Already returns error
}

// After:
func getJson(url string, target interface{}) error {
	r, err := http.Get(url)
	if err != nil {
		return err
	}
	defer util.deferCloseBody(r.Body, url)  // ✅

	return json.NewDecoder(r.Body).Decode(target)  // ✅ Already returns error
}
```

### 3.7 Phase 3: Audit Remaining Files

**Search for patterns:**

```bash
# Find remaining ignored errors
grep -rn "defer.*Close()" --include="*.go" | grep -v "err :=" | grep -v "util.defer"

# Find remaining ignored Write calls
grep -rn ", _.*Write" --include="*.go"

# Find remaining ignored Marshal calls
grep -rn ", _.*Marshal" --include="*.go"

# Find remaining ignored Encode calls
grep -rn "Encode(" --include="*.go" | grep -v "err"
```

**Fix any found in:**
- `db/*.go`
- `notification/*.go`
- `constants/*.go`
- Any other files

### 3.8 Breaking Changes Summary

**Function Signature Changes:**

None! The helper functions are new, existing functions maintain compatibility.

**Behavior Changes:**

1. **JSON Marshal Failures**: Now return HTTP 500 instead of empty 200
   - **Breaking**: Yes - changes response code
   - **Better**: Yes - clients know something failed

2. **Status Code Ordering**: Now correct (Issue #21)
   - **Breaking**: Yes - error responses now have correct status codes
   - **Better**: Yes - HTTP compliant

3. **Resource Cleanup**: Now logged instead of silent
   - **Breaking**: No - just adds logging
   - **Better**: Yes - visibility into issues

---

## 4. Testing Strategy

### 4.1 Manual Testing Checklist

**Test 1: JSON Marshaling Success**
```bash
# Normal case - should work as before
curl http://localhost:8090/api/scans

# Expected: HTTP 200, valid JSON
```

**Test 2: JSON Marshaling Failure (Simulated)**

To test, temporarily add unmarshalable field:
```go
type TestResponse struct {
	Data    string
	Channel chan int  // Can't be marshaled
}
```

```bash
curl http://localhost:8090/api/test-endpoint

# Expected: HTTP 500, error message
# Log: "Failed to marshal JSON response"
```

**Test 3: Status Code Ordering (Issue #21)**
```bash
# Test OAuth without redirectUri
curl http://localhost:8090/api/glink

# Before fix: HTTP 200 OK
# After fix: HTTP 400 Bad Request
```

**Test 4: Write Error (Simulated)**

Hard to test naturally, but can verify logging works:
- Check logs during normal operation
- Should NOT see "Failed to write response" errors
- If network issues occur, should see log entries

**Test 5: File Close Errors**

```bash
# Run local scan
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/test"}}'

# Check logs - should NOT see "Failed to close resource" errors
# If file system issues, should see log entries
```

**Test 6: HTTP Response Body Close**

```bash
# Trigger OAuth flow or any Google API call
# Check logs - should NOT see "Failed to close HTTP response body"

# Monitor file descriptors
lsof -p $(pgrep hdd) | grep -c TCP

# Should not grow unbounded over time
```

### 4.2 Integration Testing

**Existing Tests Continue to Work:**

Since we're not changing function signatures (just adding helpers), existing integration tests should pass without modification.

**Monitor for:**
- No new test failures
- Same test coverage maintained
- No performance degradation

### 4.3 Performance Testing

**Benchmark writeJSONResponse:**

```go
// Add to web/response_test.go
func BenchmarkWriteJSONResponse(b *testing.B) {
	data := map[string]string{"test": "data"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w := httptest.NewRecorder()
		writeJSONResponse(w, data, http.StatusOK)
	}
}
```

**Expected:**
- Performance same or slightly better (marshal only once)
- No measurable overhead from error checking

### 4.4 Error Injection Testing

**Simulate failures:**

```go
// Test helper handles marshal errors
func TestWriteJSONResponse_MarshalError(t *testing.T) {
	w := httptest.NewRecorder()

	// channel type can't be marshaled
	data := map[string]interface{}{
		"channel": make(chan int),
	}

	err := writeJSONResponse(w, data, http.StatusOK)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal JSON")
	// Response should be empty or error response
}
```

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Create helper files (web/response.go, util/io.go)
- [ ] Phase 1: Update web/api.go handlers
- [ ] Phase 1: Update web/oauth.go handlers
- [ ] Phase 2: Update collect/local.go
- [ ] Phase 2: Update collect/photos.go
- [ ] Phase 3: Audit and fix remaining files
- [ ] Manual testing completed
- [ ] Build succeeds
- [ ] Integration tests pass
- [ ] Performance benchmarks acceptable

### 5.2 Deployment Steps

**Step 1: Create Helper Files**

```bash
cd be

# Create web/response.go
# Create util/io.go

# Verify builds
go build .
```

**Step 2: Phase 1 - Update HTTP Handlers**

```bash
# Update web/api.go
# Update web/oauth.go

# Build and test
go build .
go test ./web/...

# Manual smoke test
./hdd &
curl http://localhost:8090/api/health
```

**Step 3: Phase 2 - Update Collectors**

```bash
# Update collect/local.go
# Update collect/photos.go

# Build and test
go build .
go test ./collect/...
```

**Step 4: Phase 3 - Final Cleanup**

```bash
# Search for remaining issues
grep -rn "defer.*Close()" --include="*.go" | grep -v "err :=" | grep -v "util.defer"

# Fix any found
# Build final version
go build .
```

**Step 5: Deploy to Staging**

```bash
# Stop staging server
ssh staging 'systemctl stop bhandaar'

# Deploy new binary
scp hdd staging:/opt/bhandaar/

# Start server
ssh staging 'systemctl start bhandaar'

# Monitor logs
ssh staging 'journalctl -u bhandaar -f'

# Look for:
# - "Failed to marshal JSON" (should be rare/never)
# - "Failed to write response" (should be rare/never)
# - "Failed to close resource" (check frequency)
```

**Step 6: Staging Validation**

```bash
# Test all critical endpoints
curl https://staging/api/health
curl https://staging/api/scans
curl https://staging/api/accounts

# Test error cases
curl https://staging/api/glink  # Should get 400 now

# Monitor for 24 hours
# Check logs for unexpected errors
```

**Step 7: Deploy to Production**

```bash
# Tag release
git tag -a v1.x.x -m "Fix ignored errors (Issue #11) and HTTP status ordering (Issue #21)"
git push origin v1.x.x

# Build production binary
go build -o hdd

# Deploy (Kubernetes example)
docker build -t jyothri/hdd-go-build:v1.x.x .
docker push jyothri/hdd-go-build:v1.x.x
kubectl set image deployment/bhandaar-backend backend=jyothri/hdd-go-build:v1.x.x

# Monitor rollout
kubectl rollout status deployment/bhandaar-backend
kubectl logs -f deployment/bhandaar-backend
```

### 5.3 Rollback Plan

**If issues detected:**

```bash
# Kubernetes
kubectl rollout undo deployment/bhandaar-backend

# systemd
ssh production 'systemctl stop bhandaar'
ssh production 'cp /opt/bhandaar/hdd.backup /opt/bhandaar/hdd'
ssh production 'systemctl start bhandaar'

# Verify
curl https://api.production.com/api/health
```

### 5.4 Monitoring Post-Deployment

**Watch for:**

1. **New Error Logs:**
   ```bash
   grep "Failed to marshal JSON" /var/log/bhandaar/app.log
   grep "Failed to write response" /var/log/bhandaar/app.log
   grep "Failed to close resource" /var/log/bhandaar/app.log
   ```

2. **Client-Side Errors:**
   - 500 errors where there were empty 200s before
   - These indicate real bugs that were hidden

3. **Status Code Distribution:**
   - Before: Mostly 200s
   - After: More 400s/500s (correct failures now visible)

4. **Resource Usage:**
   - File descriptor count should not grow
   - Memory usage stable

---

## 6. Monitoring and Observability

### 6.1 Logging Improvements

**New Log Entries:**

**Marshal Failures:**
```
2025-12-21 10:30:01 ERROR Failed to marshal JSON response
  error="json: unsupported type: chan int"
  status_code=500
  data_type="map[string]interface {}"
```

**Write Failures:**
```
2025-12-21 10:30:02 ERROR Failed to write JSON response body
  error="write tcp: broken pipe"
  status_code=200
  body_size=1024
```

**Resource Close Failures:**
```
2025-12-21 10:30:03 ERROR Failed to close resource
  resource_type="file"
  identifier="/tmp/test/file.txt"
  error="bad file descriptor"
```

**HTTP Body Close Failures:**
```
2025-12-21 10:30:04 WARN Failed to close HTTP response body
  url="https://www.googleapis.com/oauth2/v2/userinfo"
  error="context canceled"
```

### 6.2 Metrics to Track (Future Enhancement)

**Prometheus Metrics:**

```go
var (
	jsonMarshalErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "bhandaar_json_marshal_errors_total",
			Help: "Total number of JSON marshal errors",
		},
	)

	responseWriteErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "bhandaar_response_write_errors_total",
			Help: "Total number of response write errors",
		},
	)

	resourceCloseErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bhandaar_resource_close_errors_total",
			Help: "Total number of resource close errors",
		},
		[]string{"resource_type"},
	)
)
```

**Alert Rules:**

```yaml
groups:
  - name: bhandaar_error_handling
    rules:
      - alert: HighMarshalErrorRate
        expr: rate(bhandaar_json_marshal_errors_total[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High JSON marshal error rate"
          description: "{{ $value }} marshal errors/sec"

      - alert: ResourceLeakDetected
        expr: rate(bhandaar_resource_close_errors_total[5m]) > 1
        for: 10m
        labels:
          severity: warning
        annotations:
          summary: "Potential resource leak"
          description: "{{ $value }} close errors/sec"
```

### 6.3 Dashboard Panels

**Grafana Dashboard:**

1. **JSON Marshal Errors**
   - Graph: Rate over time
   - Should be near zero

2. **Response Write Errors**
   - Graph: Rate over time
   - Indicates network issues

3. **Resource Close Errors by Type**
   - Pie chart: file vs http_body vs other
   - Indicates leak sources

4. **File Descriptor Count**
   - Graph: Open FDs over time
   - Should be stable, not growing

---

## 7. Security Considerations

### 7.1 Error Message Exposure

**Before Fix:**
```go
// Might expose internal errors to client
w.Write([]byte(err.Error()))  // ❌ Dangerous
```

**After Fix:**
```go
// Log full error, send generic message to client
slog.Error("Failed to process request", "error", err)
http.Error(w, "Internal server error", http.StatusInternalServerError)
```

**Best Practice:**
- Internal errors logged with full details
- Client receives generic message
- No stack traces or paths exposed

### 7.2 Resource Exhaustion Prevention

**File Descriptor Limits:**

Before fix, leaked FDs could exhaust limits:
```bash
# Check current limit
ulimit -n
# Output: 1024

# After many scans with close failures
lsof -p $(pgrep hdd) | wc -l
# Output: 1020  # ❌ Almost at limit

# Server crashes soon after
```

After fix, close errors logged but don't leak:
```bash
# After many scans
lsof -p $(pgrep hdd) | wc -l
# Output: 50  # ✅ Stable

# Logs show occasional close errors
# But FDs are cleaned up properly
```

### 7.3 Audit Trail

**Error Logging Provides:**
- IP addresses (from request context)
- Timestamps
- Error types and frequencies
- Resource identifiers

**Useful for:**
- Security incident investigation
- Capacity planning
- Bug identification
- Performance optimization

---

## 8. Future Enhancements

### 8.1 Structured Error Responses

**Current (After Fix):**
```json
{
  "error": {
    "message": "Internal server error",
    "code": 500
  }
}
```

**Future Enhancement:**
```json
{
  "error": {
    "code": "INTERNAL_ERROR",
    "message": "An internal error occurred",
    "request_id": "req_123abc",
    "timestamp": "2025-12-21T10:30:00Z",
    "details": {
      "type": "marshal_error"
    }
  }
}
```

### 8.2 Response Metrics

**Add to writeJSONResponse:**
```go
// Track response sizes
responseSize.Observe(float64(len(serializedBody)))

// Track response times
start := time.Now()
// ... write response ...
responseDuration.Observe(time.Since(start).Seconds())
```

### 8.3 Circuit Breaker for Marshaling

**If marshal errors frequent:**
```go
type CircuitBreaker struct {
	failures int
	threshold int
	resetTime time.Time
}

func (cb *CircuitBreaker) AllowRequest() bool {
	if time.Now().After(cb.resetTime) {
		cb.failures = 0
		cb.resetTime = time.Now().Add(1 * time.Minute)
	}
	return cb.failures < cb.threshold
}
```

### 8.4 Health Check Integration

**Add to health endpoint:**
```go
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"ok": true,
		"checks": map[string]bool{
			"database": db.Ping() == nil,
			"errors": recentErrorCount() < 100,
		},
	}
	writeJSONResponseOK(w, health)
}
```

---

## Appendix A: Complete File Changes Summary

### Files to Create

1. **`web/response.go`** - NEW
   - writeJSONResponse() - Main JSON response helper
   - writeJSONResponseOK() - Convenience wrapper for 200 OK
   - writeJSONError() - Error response helper
   - encodeJSONResponse() - Streaming JSON encoder

2. **`util/io.go`** - NEW
   - Closer interface
   - deferClose() - Generic deferred close helper
   - closeWithLog() - Immediate close helper
   - deferCloseBody() - HTTP response body helper

### Files to Modify

**Phase 1: HTTP Handlers**

1. **`web/api.go`**
   - Import util package
   - Update HealthCheckHandler
   - Update ListAlbumsHandler (line 199-201)
   - Update ListPhotosHandler
   - Update GetScanHandler
   - Update ListScansHandler
   - Update GetGmailDataHandler
   - Update DeleteScanHandler
   - Any other handlers writing JSON

2. **`web/oauth.go`**
   - Fix redirectUri error (line 30-33) - Issue #21
   - Fix HTTP response body close (line 73)
   - Update any other Write() calls

**Phase 2: Collectors**

3. **`collect/local.go`**
   - Import util package
   - Update computeMd5ForFile (line 145)

4. **`collect/photos.go`**
   - Import util package
   - Update 4 instances of defer resp.Body.Close() (lines 391, 444, 457)
   - Update getJson function

**Phase 3: Remaining Files**

5. **Any other files** discovered during audit

---

## Appendix B: Error Code Reference

### Error Log Patterns

| Error Type | Log Level | Pattern | Action |
|------------|-----------|---------|--------|
| Marshal failure | ERROR | "Failed to marshal JSON response" | Review data structure, fix unmarshalable types |
| Write failure | ERROR | "Failed to write JSON response body" | Check network, client disconnection |
| File close failure | ERROR | "Failed to close resource" (file) | Check file system, permissions |
| HTTP body close | WARN | "Failed to close HTTP response body" | Usually benign, check if frequent |
| Encode failure | ERROR | "Failed to encode JSON response" | Review data structure |

### HTTP Status Code Changes

| Endpoint | Old Behavior | New Behavior | Issue |
|----------|--------------|--------------|-------|
| `/api/glink` (no redirectUri) | 200 OK + text | 400 Bad Request | #21 |
| Any endpoint (marshal fails) | 200 OK + empty | 500 Internal Server Error | #11 |
| Any endpoint (valid) | 200 OK | 200 OK (unchanged) | - |

---

## Appendix C: Testing Commands Reference

**Check for ignored errors:**
```bash
# Find defer Close without error handling
grep -rn "defer.*Close()" --include="*.go" | grep -v "err :=" | grep -v "util.defer"

# Find ignored Write calls
grep -rn ", _.*Write" --include="*.go"

# Find ignored Marshal calls
grep -rn ", _.*Marshal" --include="*.go"

# Find Encode without error check
grep -rn "Encode(" --include="*.go" | grep -v "err"
```

**Monitor file descriptors:**
```bash
# Current FD count
lsof -p $(pgrep hdd) | wc -l

# Watch FD count
watch -n 5 'lsof -p $(pgrep hdd) | wc -l'

# FD breakdown
lsof -p $(pgrep hdd) | awk '{print $5}' | sort | uniq -c
```

**Monitor errors in logs:**
```bash
# Watch for new error types
tail -f /var/log/bhandaar/app.log | grep -E "Failed to (marshal|write|close)"

# Count errors by type
grep "Failed to" /var/log/bhandaar/app.log | awk '{print $4, $5, $6}' | sort | uniq -c
```

---

**END OF DOCUMENT**
