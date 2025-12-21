# Issue #10 Implementation Plan: No Input Validation

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P0 - Critical (Security & Data Integrity)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #10: No Input Validation**. The current system accepts and processes API requests without validating inputs, leading to crashes, data corruption, and security vulnerabilities.

**Selected Approach:**
- **Validation Method**: Custom Validate() methods (no external library)
- **Validation Scope**: All API request bodies (POST/PUT endpoints)
- **Error Response**: Multiple errors at once with structured format
- **String Limits**: Conservative (Path: 1000, RefreshToken: 800, Filter: 500, AccountKey: 100)
- **Path Validation**: Basic (non-empty, max length, no null bytes)
- **Token Validation**: Format-only (non-empty, max length)

**Estimated Effort:** 6-8 hours

**Impact:**
- Prevents crashes from malformed input
- Eliminates data corruption risks
- Provides clear error messages to clients
- Improves security posture
- Enables easier debugging

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Implementation Details](#3-implementation-details)
4. [Testing Strategy](#4-testing-strategy)
5. [Deployment Plan](#5-deployment-plan)
6. [Security Considerations](#6-security-considerations)
7. [Future Enhancements](#7-future-enhancements)

---

## 1. Current State Analysis

### 1.1 Current Implementation

**web/api.go (lines 300-344):**
```go
type DoScanRequest struct {
	ScanType    string
	LocalScan   collect.LocalScan
	GDriveScan  collect.GDriveScan
	GMailScan   collect.GMailScan
	GPhotosScan collect.GPhotosScan
}

func DoScansHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var doScanRequest DoScanRequest
	err := decoder.Decode(&doScanRequest)

	// ❌ No validation of doScanRequest fields!

	if err != nil {
		// ... error handling
	}

	// Directly use unvalidated input
	switch doScanRequest.ScanType {
	case "Local":
		collect.RunLocalScan(doScanRequest.LocalScan)  // ❌ No validation
	case "GDrive":
		collect.RunDriveScan(doScanRequest.GDriveScan) // ❌ No validation
	// ...
	}
}
```

**collect/local.go:**
```go
type LocalScan struct {
	Path string  // ❌ No validation
}

func RunLocalScan(localScan LocalScan) error {
	// ❌ Directly uses localScan.Path without validation
	// Could be empty, too long, contain null bytes, etc.
	filepath.Walk(localScan.Path, ...)
}
```

**collect/gmail.go:**
```go
type GMailScan struct {
	Filter       string  // ❌ No validation
	RefreshToken string  // ❌ No validation
	ClientKey    string  // ❌ No validation
	Username     string  // ❌ No validation
}

func RunGMailScan(ctx context.Context, gmailScan GMailScan, scanId int64) error {
	// ❌ Directly uses all fields without validation
}
```

### 1.2 Vulnerabilities and Issues

| Vulnerability | Severity | Impact | Example |
|---------------|----------|--------|---------|
| **Empty required fields** | CRITICAL | Crashes, nil pointer dereferences | `{"ScanType":"Local","LocalScan":{"Path":""}}` |
| **Excessively long strings** | HIGH | Memory exhaustion, DoS | `{"Path":"a"*1000000}` |
| **Invalid ScanType** | HIGH | Unhandled cases, silent failures | `{"ScanType":"Invalid"}` |
| **Null bytes in paths** | HIGH | Path traversal, file system attacks | `{"Path":"/tmp\x00../etc/passwd"}` |
| **Missing required OAuth tokens** | HIGH | API failures, crashes | `{"GMailScan":{"RefreshToken":""}}` |
| **Invalid filter syntax** | MEDIUM | Gmail API errors, failed scans | `{"Filter":"from:@@@invalid"}` |
| **SQL injection in filters** | MEDIUM | Database corruption (if filter stored) | Depends on usage |

### 1.3 Attack Scenarios

**Scenario 1: Empty Path Crash**
```bash
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":""}}'

# Result: filepath.Walk("", ...) crashes or hangs
# Server logs: panic or indefinite blocking
```

**Scenario 2: Memory Exhaustion**
```bash
# Create 10 MB path string
python3 -c "import json; print(json.dumps({'ScanType':'Local','LocalScan':{'Path':'a'*10485760}}))" | \
  curl -X POST http://localhost:8090/api/scans \
    -H "Content-Type: application/json" \
    -d @-

# Result: High memory usage, potential OOM
```

**Scenario 3: Path Traversal with Null Bytes**
```bash
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp\u0000../../../etc"}}'

# Result: Potential access to restricted paths
```

**Scenario 4: Missing OAuth Token**
```bash
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"GMail","GMailScan":{"RefreshToken":"","Filter":"is:unread"}}'

# Result: Google API call fails with unclear error
# Database may be corrupted with invalid scan record
```

### 1.4 Current Error Handling Gaps

| Gap | Current Behavior | Desired Behavior |
|-----|------------------|------------------|
| **No field-level errors** | Generic "bad request" | Specific field errors |
| **Single error at a time** | Returns after first issue | Returns all issues at once |
| **Unclear error messages** | "Invalid request" | "Path is required and cannot be empty" |
| **No error codes** | HTTP status only | Structured error codes per field |
| **Poor debugging** | No context in logs | Full request logged on validation failure |

### 1.5 Impact Assessment

**Without Input Validation:**
- 🔴 **Critical**: Server crashes from nil/empty fields
- 🔴 **Critical**: Data corruption in database from invalid values
- 🔴 **High**: Memory exhaustion from oversized strings
- 🔴 **High**: Security vulnerabilities (path traversal, injection)
- 🟡 **Medium**: Poor user experience (unclear errors)
- 🟡 **Medium**: Difficult debugging (no input logging)

**Cost of Current State:**
- User frustration from cryptic errors
- Server downtime from crashes
- Time wasted debugging production issues
- Potential security incidents

---

## 2. Target Architecture

### 2.1 Validation Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. HTTP Request Received                                    │
│    POST /api/scans                                          │
│    Body: {"ScanType":"Local","LocalScan":{"Path":"..."}}   │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Size Limit Middleware (Issue #7)                        │
│    - Enforces max body size (1 MB for scans)               │
│    - Returns HTTP 413 if exceeded                          │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. JSON Decoding                                            │
│    - json.Decode(&doScanRequest)                           │
│    - Handles malformed JSON                                │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Request-Level Validation                                 │
│    - doScanRequest.Validate()                              │
│    - Validates ScanType field                              │
│    - Ensures exactly one scan type populated               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Scan Type-Specific Validation                           │
│    - LocalScan.Validate()                                   │
│    - GDriveScan.Validate()                                  │
│    - GMailScan.Validate()                                   │
│    - GPhotosScan.Validate()                                 │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
                    ┌───────────────┐
                    │  Valid?       │
                    └───────────────┘
                      │           │
                     Yes          No
                      │           │
                      │           ▼
                      │     ┌─────────────────────────────┐
                      │     │ Return HTTP 400             │
                      │     │ JSON error response with:   │
                      │     │ - Error code                │
                      │     │ - Error message             │
                      │     │ - Field-level errors        │
                      │     │ - Timestamp                 │
                      │     └─────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ 6. Process Valid Request                                    │
│    - Save scan to database                                  │
│    - Start background scan                                  │
│    - Return HTTP 200                                        │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Validation Architecture

**Validation Package Structure:**

```
be/
├── validation/
│   ├── validation.go         # Core validation types and helpers
│   ├── scan_validators.go    # Validators for scan types
│   └── validation_test.go    # Unit tests
├── collect/
│   ├── local.go             # LocalScan.Validate()
│   ├── gmail.go             # GMailScan.Validate()
│   ├── drive.go             # GDriveScan.Validate()
│   └── photos.go            # GPhotosScan.Validate()
└── web/
    └── api.go               # DoScanRequest.Validate()
```

**Validation Types:**

```go
// validation/validation.go

// ValidationError represents a single field validation error
type ValidationError struct {
	Field   string `json:"field"`            // Field name (e.g., "LocalScan.Path")
	Code    string `json:"code"`             // Error code (e.g., "REQUIRED", "TOO_LONG")
	Message string `json:"message"`          // Human-readable message
	Value   string `json:"value,omitempty"`  // Invalid value (truncated for logs)
}

// ValidationErrors is a collection of field validation errors
type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	// Returns all errors as formatted string
}

func (ve ValidationErrors) HasErrors() bool {
	return len(ve) > 0
}

// Validator interface for all validatable types
type Validator interface {
	Validate() ValidationErrors
}
```

### 2.3 Error Response Format

**Successful Validation:**
```json
{
  "scan_id": 12345,
  "status": "Pending",
  "created_at": "2025-12-21T10:30:00Z"
}
```

**Validation Failure (HTTP 400):**
```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "Request validation failed",
    "timestamp": "2025-12-21T10:30:00Z",
    "validation_errors": [
      {
        "field": "ScanType",
        "code": "INVALID_VALUE",
        "message": "ScanType must be one of: Local, GDrive, GMail, GPhotos"
      },
      {
        "field": "LocalScan.Path",
        "code": "REQUIRED",
        "message": "Path is required and cannot be empty"
      },
      {
        "field": "GMailScan.RefreshToken",
        "code": "TOO_LONG",
        "message": "RefreshToken exceeds maximum length of 800 characters"
      }
    ]
  }
}
```

### 2.4 Validation Rules Summary

| Field | Type | Rules | Error Codes |
|-------|------|-------|-------------|
| **DoScanRequest.ScanType** | string | Required, one of: Local/GDrive/GMail/GPhotos | REQUIRED, INVALID_VALUE |
| **LocalScan.Path** | string | Required, non-empty, max 1000 chars, no null bytes | REQUIRED, EMPTY, TOO_LONG, INVALID_CHARS |
| **GDriveScan.QueryString** | string | Max 500 chars | TOO_LONG |
| **GDriveScan.RefreshToken** | string | Required, non-empty, max 800 chars | REQUIRED, EMPTY, TOO_LONG |
| **GMailScan.Filter** | string | Max 500 chars | TOO_LONG |
| **GMailScan.RefreshToken** | string | Required, non-empty, max 800 chars | REQUIRED, EMPTY, TOO_LONG |
| **GMailScan.ClientKey** | string | Required, non-empty, max 100 chars | REQUIRED, EMPTY, TOO_LONG |
| **GMailScan.Username** | string | Required, non-empty, max 100 chars | REQUIRED, EMPTY, TOO_LONG |
| **GPhotosScan.AlbumId** | string | Max 100 chars | TOO_LONG |
| **GPhotosScan.RefreshToken** | string | Required, non-empty, max 800 chars | REQUIRED, EMPTY, TOO_LONG |

---

## 3. Implementation Details

### 3.1 Validation Package: `validation/validation.go` (NEW FILE)

```go
package validation

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidationError represents a single field validation error
type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Value   string `json:"value,omitempty"`
}

// ValidationErrors is a collection of field validation errors
type ValidationErrors []ValidationError

// Error implements the error interface
func (ve ValidationErrors) Error() string {
	if len(ve) == 0 {
		return "no validation errors"
	}

	var messages []string
	for _, err := range ve {
		messages = append(messages, fmt.Sprintf("%s: %s", err.Field, err.Message))
	}
	return strings.Join(messages, "; ")
}

// HasErrors returns true if there are validation errors
func (ve ValidationErrors) HasErrors() bool {
	return len(ve) > 0
}

// Add adds a validation error to the collection
func (ve *ValidationErrors) Add(field, code, message string) {
	*ve = append(*ve, ValidationError{
		Field:   field,
		Code:    code,
		Message: message,
	})
}

// AddWithValue adds a validation error with the invalid value
func (ve *ValidationErrors) AddWithValue(field, code, message, value string) {
	// Truncate value for logging (max 50 chars)
	truncatedValue := value
	if len(value) > 50 {
		truncatedValue = value[:50] + "..."
	}

	*ve = append(*ve, ValidationError{
		Field:   field,
		Code:    code,
		Message: message,
		Value:   truncatedValue,
	})
}

// Validator interface for all validatable types
type Validator interface {
	Validate() ValidationErrors
}

// Error codes
const (
	ErrRequired      = "REQUIRED"
	ErrEmpty         = "EMPTY"
	ErrTooLong       = "TOO_LONG"
	ErrInvalidValue  = "INVALID_VALUE"
	ErrInvalidChars  = "INVALID_CHARS"
	ErrInvalidFormat = "INVALID_FORMAT"
)

// Common validation helpers

// ValidateRequired checks if a string field is present
func ValidateRequired(field, value string, errors *ValidationErrors) {
	if value == "" {
		errors.Add(field, ErrRequired, fmt.Sprintf("%s is required and cannot be empty", field))
	}
}

// ValidateNonEmpty checks if a string field is not empty (different from required)
func ValidateNonEmpty(field, value string, errors *ValidationErrors) {
	if len(strings.TrimSpace(value)) == 0 {
		errors.Add(field, ErrEmpty, fmt.Sprintf("%s cannot be empty or whitespace only", field))
	}
}

// ValidateMaxLength checks if a string exceeds maximum length
func ValidateMaxLength(field, value string, maxLength int, errors *ValidationErrors) {
	if utf8.RuneCountInString(value) > maxLength {
		errors.AddWithValue(
			field,
			ErrTooLong,
			fmt.Sprintf("%s exceeds maximum length of %d characters", field, maxLength),
			value,
		)
	}
}

// ValidateNoNullBytes checks if a string contains null bytes
func ValidateNoNullBytes(field, value string, errors *ValidationErrors) {
	if strings.Contains(value, "\x00") {
		errors.AddWithValue(
			field,
			ErrInvalidChars,
			fmt.Sprintf("%s contains invalid null bytes", field),
			value,
		)
	}
}

// ValidateStringField performs common string validations
func ValidateStringField(field, value string, required bool, maxLength int, errors *ValidationErrors) {
	if required {
		ValidateRequired(field, value, errors)
		if value == "" {
			return // No point in further validation if empty
		}
	}

	if value != "" {
		ValidateNonEmpty(field, value, errors)
		ValidateMaxLength(field, value, maxLength, errors)
		ValidateNoNullBytes(field, value, errors)
	}
}

// ValidatePath validates file system paths
func ValidatePath(field, value string, errors *ValidationErrors) {
	ValidateStringField(field, value, true, 1000, errors)

	// Additional path-specific validation could go here
	// For now, keeping it basic as per user's choice
}

// ValidateRefreshToken validates OAuth refresh tokens
func ValidateRefreshToken(field, value string, errors *ValidationErrors) {
	ValidateStringField(field, value, true, 800, errors)

	// Format-only validation as per user's choice
	// Could add format checks here if needed in future
}

// ValidateFilter validates search/query filter strings
func ValidateFilter(field, value string, required bool, errors *ValidationErrors) {
	ValidateStringField(field, value, required, 500, errors)
}

// ValidateAccountKey validates account/client keys
func ValidateAccountKey(field, value string, errors *ValidationErrors) {
	ValidateStringField(field, value, true, 100, errors)
}

// ValidateUsername validates username fields
func ValidateUsername(field, value string, errors *ValidationErrors) {
	ValidateStringField(field, value, true, 100, errors)
}

// ValidateAlbumId validates album ID fields
func ValidateAlbumId(field, value string, required bool, errors *ValidationErrors) {
	ValidateStringField(field, value, required, 100, errors)
}
```

### 3.2 Update Scan Types with Validation: `collect/local.go`

**Add Validate() method to LocalScan:**

```go
package collect

import (
	"github.com/jyothri/hdd/validation"
)

type LocalScan struct {
	Path string
}

// Validate validates the LocalScan request
func (ls *LocalScan) Validate() validation.ValidationErrors {
	var errors validation.ValidationErrors

	validation.ValidatePath("LocalScan.Path", ls.Path, &errors)

	return errors
}

// Rest of local.go unchanged
```

### 3.3 Update GDriveScan: `collect/drive.go`

**Add Validate() method to GDriveScan:**

```go
package collect

import (
	"github.com/jyothri/hdd/validation"
)

type GDriveScan struct {
	QueryString  string
	RefreshToken string
}

// Validate validates the GDriveScan request
func (gds *GDriveScan) Validate() validation.ValidationErrors {
	var errors validation.ValidationErrors

	// QueryString is optional
	validation.ValidateFilter("GDriveScan.QueryString", gds.QueryString, false, &errors)

	// RefreshToken is required
	validation.ValidateRefreshToken("GDriveScan.RefreshToken", gds.RefreshToken, &errors)

	return errors
}

// Rest of drive.go unchanged
```

### 3.4 Update GMailScan: `collect/gmail.go`

**Add Validate() method to GMailScan:**

```go
package collect

import (
	"github.com/jyothri/hdd/validation"
)

type GMailScan struct {
	Filter       string
	RefreshToken string
	ClientKey    string
	Username     string
}

// Validate validates the GMailScan request
func (gms *GMailScan) Validate() validation.ValidationErrors {
	var errors validation.ValidationErrors

	// Filter is optional
	validation.ValidateFilter("GMailScan.Filter", gms.Filter, false, &errors)

	// RefreshToken is required
	validation.ValidateRefreshToken("GMailScan.RefreshToken", gms.RefreshToken, &errors)

	// ClientKey is required
	validation.ValidateAccountKey("GMailScan.ClientKey", gms.ClientKey, &errors)

	// Username is required
	validation.ValidateUsername("GMailScan.Username", gms.Username, &errors)

	return errors
}

// Rest of gmail.go unchanged
```

### 3.5 Update GPhotosScan: `collect/photos.go`

**Add Validate() method to GPhotosScan:**

```go
package collect

import (
	"github.com/jyothri/hdd/validation"
)

type GPhotosScan struct {
	AlbumId      string
	FetchSize    bool
	FetchMd5Hash bool
	RefreshToken string
}

// Validate validates the GPhotosScan request
func (gps *GPhotosScan) Validate() validation.ValidationErrors {
	var errors validation.ValidationErrors

	// AlbumId is optional
	validation.ValidateAlbumId("GPhotosScan.AlbumId", gps.AlbumId, false, &errors)

	// RefreshToken is required
	validation.ValidateRefreshToken("GPhotosScan.RefreshToken", gps.RefreshToken, &errors)

	// Boolean fields don't need validation

	return errors
}

// Rest of photos.go unchanged
```

### 3.6 Update DoScanRequest: `web/api.go`

**Add Validate() method and update handler:**

```go
package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/collect"
	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/validation"
)

type DoScanRequest struct {
	ScanType    string
	LocalScan   collect.LocalScan
	GDriveScan  collect.GDriveScan
	GMailScan   collect.GMailScan
	GPhotosScan collect.GPhotosScan
}

// Validate validates the DoScanRequest
func (dsr *DoScanRequest) Validate() validation.ValidationErrors {
	var errors validation.ValidationErrors

	// Validate ScanType
	validScanTypes := map[string]bool{
		"Local":   true,
		"GDrive":  true,
		"GMail":   true,
		"GPhotos": true,
	}

	if dsr.ScanType == "" {
		errors.Add("ScanType", validation.ErrRequired, "ScanType is required")
		return errors // Can't proceed without ScanType
	}

	if !validScanTypes[dsr.ScanType] {
		errors.Add(
			"ScanType",
			validation.ErrInvalidValue,
			"ScanType must be one of: Local, GDrive, GMail, GPhotos",
		)
		return errors
	}

	// Validate the appropriate scan type
	switch dsr.ScanType {
	case "Local":
		localErrors := dsr.LocalScan.Validate()
		errors = append(errors, localErrors...)

	case "GDrive":
		driveErrors := dsr.GDriveScan.Validate()
		errors = append(errors, driveErrors...)

	case "GMail":
		gmailErrors := dsr.GMailScan.Validate()
		errors = append(errors, gmailErrors...)

	case "GPhotos":
		photosErrors := dsr.GPhotosScan.Validate()
		errors = append(errors, photosErrors...)
	}

	return errors
}

func DoScansHandler(w http.ResponseWriter, r *http.Request) {
	decoder := json.NewDecoder(r.Body)
	var doScanRequest DoScanRequest
	err := decoder.Decode(&doScanRequest)

	// Check if error is due to size limit (from Issue #7)
	if handleMaxBytesError(w, r, err, ScanRequestMaxBodySize) {
		return
	}

	if err != nil {
		slog.Error("Failed to decode scan request",
			"error", err,
			"remote_addr", r.RemoteAddr)
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	// VALIDATE REQUEST
	validationErrors := doScanRequest.Validate()
	if validationErrors.HasErrors() {
		slog.Warn("Scan request validation failed",
			"remote_addr", r.RemoteAddr,
			"scan_type", doScanRequest.ScanType,
			"error_count", len(validationErrors),
			"errors", validationErrors.Error())

		writeValidationErrorResponse(w, validationErrors)
		return
	}

	// Validation passed, proceed with scan
	slog.Info("Scan request validated successfully",
		"scan_type", doScanRequest.ScanType,
		"remote_addr", r.RemoteAddr)

	var scanId int64

	switch doScanRequest.ScanType {
	case "Local":
		scanId, err = db.CreateScan("Local", doScanRequest.LocalScan.Path, "")
		if err != nil {
			slog.Error("Failed to create local scan",
				"error", err,
				"path", doScanRequest.LocalScan.Path)
			http.Error(w, "Failed to create scan", http.StatusInternalServerError)
			return
		}
		go collect.RunLocalScan(doScanRequest.LocalScan, scanId)

	case "GDrive":
		scanId, err = db.CreateScan("GDrive", "", doScanRequest.GDriveScan.RefreshToken)
		if err != nil {
			slog.Error("Failed to create drive scan", "error", err)
			http.Error(w, "Failed to create scan", http.StatusInternalServerError)
			return
		}
		go collect.RunDriveScan(doScanRequest.GDriveScan, scanId)

	case "GMail":
		scanId, err = db.CreateScan("GMail", "", doScanRequest.GMailScan.RefreshToken)
		if err != nil {
			slog.Error("Failed to create gmail scan", "error", err)
			http.Error(w, "Failed to create scan", http.StatusInternalServerError)
			return
		}
		go collect.RunGMailScan(r.Context(), doScanRequest.GMailScan, scanId)

	case "GPhotos":
		scanId, err = db.CreateScan("GPhotos", "", doScanRequest.GPhotosScan.RefreshToken)
		if err != nil {
			slog.Error("Failed to create photos scan", "error", err)
			http.Error(w, "Failed to create scan", http.StatusInternalServerError)
			return
		}
		go collect.RunGPhotosScan(doScanRequest.GPhotosScan, scanId)

	default:
		// Should never happen after validation, but defensive programming
		slog.Error("Unknown scan type after validation",
			"scan_type", doScanRequest.ScanType)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Return success response
	response := map[string]interface{}{
		"scan_id":    scanId,
		"status":     "Pending",
		"created_at": time.Now().UTC().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode success response", "error", err)
	}
}

// writeValidationErrorResponse writes a structured validation error response
func writeValidationErrorResponse(w http.ResponseWriter, validationErrors validation.ValidationErrors) {
	response := ValidationErrorResponse{
		Error: ValidationErrorDetail{
			Code:             "VALIDATION_FAILED",
			Message:          "Request validation failed",
			Timestamp:        time.Now().UTC().Format(time.RFC3339),
			ValidationErrors: validationErrors,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode validation error response", "error", err)
	}
}

// ValidationErrorResponse is the top-level error response
type ValidationErrorResponse struct {
	Error ValidationErrorDetail `json:"error"`
}

// ValidationErrorDetail contains validation error details
type ValidationErrorDetail struct {
	Code             string                     `json:"code"`
	Message          string                     `json:"message"`
	Timestamp        string                     `json:"timestamp"`
	ValidationErrors validation.ValidationErrors `json:"validation_errors"`
}

// Rest of api.go unchanged
```

---

## 4. Testing Strategy

### 4.1 Unit Tests

**`validation/validation_test.go`** (NEW FILE)

```go
package validation

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateRequired(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		expectError bool
	}{
		{"valid non-empty", "test", false},
		{"empty string", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errors ValidationErrors
			ValidateRequired("TestField", tt.value, &errors)

			if tt.expectError {
				assert.True(t, errors.HasErrors())
				assert.Equal(t, "TestField", errors[0].Field)
				assert.Equal(t, ErrRequired, errors[0].Code)
			} else {
				assert.False(t, errors.HasErrors())
			}
		})
	}
}

func TestValidateNonEmpty(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		expectError bool
	}{
		{"valid", "test", false},
		{"whitespace only", "   ", true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errors ValidationErrors
			ValidateNonEmpty("TestField", tt.value, &errors)

			if tt.expectError {
				assert.True(t, errors.HasErrors())
			} else {
				assert.False(t, errors.HasErrors())
			}
		})
	}
}

func TestValidateMaxLength(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		maxLength   int
		expectError bool
	}{
		{"within limit", "test", 10, false},
		{"at limit", "1234567890", 10, false},
		{"exceeds limit", "12345678901", 10, true},
		{"unicode within limit", "こんにちは", 10, false},
		{"unicode exceeds limit", "こんにちは世界こんにちは世界", 10, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errors ValidationErrors
			ValidateMaxLength("TestField", tt.value, tt.maxLength, &errors)

			if tt.expectError {
				assert.True(t, errors.HasErrors())
				assert.Equal(t, ErrTooLong, errors[0].Code)
			} else {
				assert.False(t, errors.HasErrors())
			}
		})
	}
}

func TestValidateNoNullBytes(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		expectError bool
	}{
		{"valid", "/tmp/test", false},
		{"null byte in middle", "/tmp\x00test", true},
		{"null byte at end", "/tmp/test\x00", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errors ValidationErrors
			ValidateNoNullBytes("TestField", tt.value, &errors)

			if tt.expectError {
				assert.True(t, errors.HasErrors())
				assert.Equal(t, ErrInvalidChars, errors[0].Code)
			} else {
				assert.False(t, errors.HasErrors())
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		expectError bool
		errorCode   string
	}{
		{"valid path", "/tmp/test", false, ""},
		{"empty path", "", true, ErrRequired},
		{"path with null byte", "/tmp\x00/test", true, ErrInvalidChars},
		{"path too long", string(make([]byte, 1001)), true, ErrTooLong},
		{"whitespace only", "   ", true, ErrEmpty},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errors ValidationErrors
			ValidatePath("TestPath", tt.value, &errors)

			if tt.expectError {
				assert.True(t, errors.HasErrors())
				assert.Equal(t, tt.errorCode, errors[0].Code)
			} else {
				assert.False(t, errors.HasErrors())
			}
		})
	}
}

func TestValidationErrors_Error(t *testing.T) {
	errors := ValidationErrors{
		{Field: "Field1", Code: "REQUIRED", Message: "Field1 is required"},
		{Field: "Field2", Code: "TOO_LONG", Message: "Field2 is too long"},
	}

	errStr := errors.Error()
	assert.Contains(t, errStr, "Field1: Field1 is required")
	assert.Contains(t, errStr, "Field2: Field2 is too long")
}

func TestValidationErrors_HasErrors(t *testing.T) {
	var empty ValidationErrors
	assert.False(t, empty.HasErrors())

	notEmpty := ValidationErrors{
		{Field: "Test", Code: "REQUIRED", Message: "Test"},
	}
	assert.True(t, notEmpty.HasErrors())
}
```

**`collect/local_test.go`** (Add tests)

```go
package collect

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocalScan_Validate(t *testing.T) {
	tests := []struct {
		name        string
		scan        LocalScan
		expectError bool
		errorField  string
		errorCode   string
	}{
		{
			name:        "valid scan",
			scan:        LocalScan{Path: "/tmp/test"},
			expectError: false,
		},
		{
			name:        "empty path",
			scan:        LocalScan{Path: ""},
			expectError: true,
			errorField:  "LocalScan.Path",
			errorCode:   "REQUIRED",
		},
		{
			name:        "path with null byte",
			scan:        LocalScan{Path: "/tmp\x00/test"},
			expectError: true,
			errorField:  "LocalScan.Path",
			errorCode:   "INVALID_CHARS",
		},
		{
			name:        "path too long",
			scan:        LocalScan{Path: string(make([]byte, 1001))},
			expectError: true,
			errorField:  "LocalScan.Path",
			errorCode:   "TOO_LONG",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := tt.scan.Validate()

			if tt.expectError {
				assert.True(t, errors.HasErrors())
				assert.Equal(t, tt.errorField, errors[0].Field)
				assert.Equal(t, tt.errorCode, errors[0].Code)
			} else {
				assert.False(t, errors.HasErrors())
			}
		})
	}
}
```

**`collect/gmail_test.go`** (Add tests)

```go
package collect

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGMailScan_Validate(t *testing.T) {
	validToken := "ya29.a0AfH6SMBxyz..."
	longToken := string(make([]byte, 801))

	tests := []struct {
		name        string
		scan        GMailScan
		expectError bool
		errorCount  int
	}{
		{
			name: "valid scan",
			scan: GMailScan{
				Filter:       "is:unread",
				RefreshToken: validToken,
				ClientKey:    "test-key",
				Username:     "testuser",
			},
			expectError: false,
		},
		{
			name: "missing all required fields",
			scan: GMailScan{
				Filter: "is:unread",
			},
			expectError: true,
			errorCount:  3, // RefreshToken, ClientKey, Username
		},
		{
			name: "token too long",
			scan: GMailScan{
				Filter:       "is:unread",
				RefreshToken: longToken,
				ClientKey:    "test-key",
				Username:     "testuser",
			},
			expectError: true,
			errorCount:  1,
		},
		{
			name: "filter too long",
			scan: GMailScan{
				Filter:       string(make([]byte, 501)),
				RefreshToken: validToken,
				ClientKey:    "test-key",
				Username:     "testuser",
			},
			expectError: true,
			errorCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := tt.scan.Validate()

			if tt.expectError {
				assert.True(t, errors.HasErrors())
				assert.Equal(t, tt.errorCount, len(errors))
			} else {
				assert.False(t, errors.HasErrors())
			}
		})
	}
}
```

**`web/api_test.go`** (Add tests)

```go
package web

import (
	"testing"

	"github.com/jyothri/hdd/collect"
	"github.com/stretchr/testify/assert"
)

func TestDoScanRequest_Validate(t *testing.T) {
	tests := []struct {
		name        string
		request     DoScanRequest
		expectError bool
		errorCount  int
	}{
		{
			name: "valid local scan",
			request: DoScanRequest{
				ScanType:  "Local",
				LocalScan: collect.LocalScan{Path: "/tmp/test"},
			},
			expectError: false,
		},
		{
			name: "missing scan type",
			request: DoScanRequest{
				LocalScan: collect.LocalScan{Path: "/tmp/test"},
			},
			expectError: true,
			errorCount:  1,
		},
		{
			name: "invalid scan type",
			request: DoScanRequest{
				ScanType:  "InvalidType",
				LocalScan: collect.LocalScan{Path: "/tmp/test"},
			},
			expectError: true,
			errorCount:  1,
		},
		{
			name: "local scan with empty path",
			request: DoScanRequest{
				ScanType:  "Local",
				LocalScan: collect.LocalScan{Path: ""},
			},
			expectError: true,
			errorCount:  1,
		},
		{
			name: "gmail scan with missing fields",
			request: DoScanRequest{
				ScanType:  "GMail",
				GMailScan: collect.GMailScan{Filter: "is:unread"},
			},
			expectError: true,
			errorCount:  3, // RefreshToken, ClientKey, Username
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := tt.request.Validate()

			if tt.expectError {
				assert.True(t, errors.HasErrors())
				assert.Equal(t, tt.errorCount, len(errors))
			} else {
				assert.False(t, errors.HasErrors())
			}
		})
	}
}
```

### 4.2 Integration Tests

**Manual Integration Testing:**

```bash
# Test 1: Valid local scan
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "Local",
    "LocalScan": {"Path": "/tmp/test"}
  }'

# Expected: HTTP 200, scan created

# Test 2: Empty path
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "Local",
    "LocalScan": {"Path": ""}
  }'

# Expected: HTTP 400, validation error for "LocalScan.Path"

# Test 3: Invalid scan type
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "InvalidType",
    "LocalScan": {"Path": "/tmp/test"}
  }'

# Expected: HTTP 400, validation error for "ScanType"

# Test 4: Multiple validation errors
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "GMail",
    "GMailScan": {
      "Filter": "",
      "RefreshToken": "",
      "ClientKey": "",
      "Username": ""
    }
  }'

# Expected: HTTP 400, multiple validation errors

# Test 5: Path too long
python3 -c "import json; print(json.dumps({'ScanType':'Local','LocalScan':{'Path':'a'*1001}}))" | \
  curl -X POST http://localhost:8090/api/scans \
    -H "Content-Type: application/json" \
    -d @-

# Expected: HTTP 400, validation error "TOO_LONG"

# Test 6: Path with null byte
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "Local",
    "LocalScan": {"Path": "/tmp\u0000/test"}
  }'

# Expected: HTTP 400, validation error "INVALID_CHARS"

# Test 7: Valid Gmail scan
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "GMail",
    "GMailScan": {
      "Filter": "is:unread",
      "RefreshToken": "ya29.a0AfH6SMBxyz...",
      "ClientKey": "test-client",
      "Username": "testuser@gmail.com"
    }
  }'

# Expected: HTTP 200, scan created
```

### 4.3 Expected Validation Error Responses

**Example 1: Empty Path**

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "Request validation failed",
    "timestamp": "2025-12-21T10:30:00Z",
    "validation_errors": [
      {
        "field": "LocalScan.Path",
        "code": "REQUIRED",
        "message": "LocalScan.Path is required and cannot be empty"
      }
    ]
  }
}
```

**Example 2: Multiple Errors**

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "Request validation failed",
    "timestamp": "2025-12-21T10:30:00Z",
    "validation_errors": [
      {
        "field": "GMailScan.RefreshToken",
        "code": "REQUIRED",
        "message": "GMailScan.RefreshToken is required and cannot be empty"
      },
      {
        "field": "GMailScan.ClientKey",
        "code": "REQUIRED",
        "message": "GMailScan.ClientKey is required and cannot be empty"
      },
      {
        "field": "GMailScan.Username",
        "code": "REQUIRED",
        "message": "GMailScan.Username is required and cannot be empty"
      }
    ]
  }
}
```

**Example 3: Path Too Long**

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "Request validation failed",
    "timestamp": "2025-12-21T10:30:00Z",
    "validation_errors": [
      {
        "field": "LocalScan.Path",
        "code": "TOO_LONG",
        "message": "LocalScan.Path exceeds maximum length of 1000 characters",
        "value": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa..."
      }
    ]
  }
}
```

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Code review completed
- [ ] Unit tests passing (`go test ./validation/...`)
- [ ] Integration tests passing
- [ ] Manual testing completed with all scenarios
- [ ] Performance testing (no significant overhead)
- [ ] Documentation updated
- [ ] Rollback plan prepared

### 5.2 Deployment Steps

**Step 1: Build and Test**

```bash
cd be

# Run all tests
go test ./...

# Build the application
go build -o hdd

# Verify build
./hdd --version  # or test startup
```

**Step 2: Deploy to Staging**

```bash
# Stop staging server
ssh staging-server 'systemctl stop bhandaar'

# Deploy new binary
scp hdd staging-server:/opt/bhandaar/

# Start server
ssh staging-server 'systemctl start bhandaar'

# Verify logs
ssh staging-server 'journalctl -u bhandaar -n 50'
```

**Step 3: Test on Staging**

```bash
# Test valid request
curl -X POST https://staging-api.example.com/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/test"}}'

# Test validation failure
curl -X POST https://staging-api.example.com/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":""}}'

# Expected: HTTP 400 with validation errors
```

**Step 4: Monitor Staging**

```bash
# Watch logs for validation errors
ssh staging-server 'journalctl -u bhandaar -f | grep -i validation'

# Check for any unexpected errors
ssh staging-server 'journalctl -u bhandaar -f | grep -i error'
```

**Step 5: Deploy to Production**

```bash
# Tag release
git tag -a v1.x.x -m "Add input validation (Issue #10)"
git push origin v1.x.x

# Build production binary
go build -o hdd

# Deploy (similar to staging)
# For Kubernetes:
docker build -t jyothri/hdd-go-build:v1.x.x .
docker push jyothri/hdd-go-build:v1.x.x
kubectl set image deployment/bhandaar-backend backend=jyothri/hdd-go-build:v1.x.x
kubectl rollout status deployment/bhandaar-backend
```

**Step 6: Post-Deployment Verification**

```bash
# Test production endpoint
curl https://api.production.com/api/health

# Test validation with valid request
curl -X POST https://api.production.com/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/test"}}'

# Monitor logs
kubectl logs -f deployment/bhandaar-backend | grep validation
```

### 5.3 Rollback Procedure

If issues are detected:

```bash
# Kubernetes: Rollback to previous deployment
kubectl rollout undo deployment/bhandaar-backend

# systemd: Restore previous binary
ssh production-server 'systemctl stop bhandaar'
ssh production-server 'cp /opt/bhandaar/hdd.backup /opt/bhandaar/hdd'
ssh production-server 'systemctl start bhandaar'

# Verify rollback
curl https://api.production.com/api/health
```

### 5.4 Gradual Rollout Strategy (Optional)

For large deployments, consider gradual rollout:

```bash
# Kubernetes: Canary deployment
# Update only 1 replica first
kubectl patch deployment bhandaar-backend -p '{"spec":{"replicas":3}}'
kubectl set image deployment/bhandaar-backend backend=new-image --record

# Monitor canary pod
kubectl get pods -l app=bhandaar-backend
kubectl logs -f <canary-pod-name>

# If successful, scale to all replicas
kubectl rollout status deployment/bhandaar-backend
```

---

## 6. Security Considerations

### 6.1 Input Validation Security Benefits

**Before Implementation:**
- ❌ **Critical**: Null byte injection in paths
- ❌ **Critical**: Path traversal via empty/invalid paths
- ❌ **High**: Memory exhaustion from oversized strings
- ❌ **High**: Database corruption from invalid data
- ❌ **Medium**: Unclear errors aid attackers

**After Implementation:**
- ✅ **Protected**: Null bytes detected and rejected
- ✅ **Protected**: Empty/invalid paths rejected
- ✅ **Protected**: String length limits enforced (combined with Issue #7)
- ✅ **Protected**: All fields validated before database
- ✅ **Monitored**: Validation failures logged with details

### 6.2 Defense in Depth

**Layer 1: Size Limits (Issue #7)**
- Maximum request body size: 1 MB
- Prevents DoS via oversized payloads

**Layer 2: Input Validation (Issue #10 - This Plan)**
- Field-level validation
- String length limits
- Character validation (no null bytes)
- Required field checks

**Layer 3: Database Constraints**
- Schema-level constraints (VARCHAR limits, NOT NULL)
- Last line of defense

**Layer 4: Application Logic**
- Additional validation in collection functions
- OAuth token refresh validation via Google API

### 6.3 Security Logging

**What Gets Logged:**

```go
slog.Warn("Scan request validation failed",
	"remote_addr", r.RemoteAddr,          // Track source IP
	"scan_type", doScanRequest.ScanType,  // Track attack pattern
	"error_count", len(validationErrors), // Track error severity
	"errors", validationErrors.Error())   // Full error details
```

**Log Analysis:**

```bash
# Find validation attack patterns
grep "validation failed" /var/log/bhandaar/app.log | \
  awk '{print $NF}' | sort | uniq -c | sort -rn

# Track repeat offenders
grep "validation failed" /var/log/bhandaar/app.log | \
  grep -oP 'remote_addr=\K[^\s]+' | sort | uniq -c | sort -rn
```

### 6.4 Rate Limiting Integration

**Combined with Issue #15 (future):**

```go
// If same IP fails validation repeatedly, rate limit
if validationFailureCount[ip] > 10 {
	// Implement rate limiting or blocking
}
```

### 6.5 Information Disclosure Prevention

**Validation Errors:**
- ✅ Do NOT reveal internal paths or system details
- ✅ Do NOT reveal database schema
- ✅ Generic messages for generic clients
- ✅ Detailed errors only for validation failures

**Example - Safe Error:**
```json
{
  "field": "LocalScan.Path",
  "code": "REQUIRED",
  "message": "Path is required and cannot be empty"
}
```

**Example - Unsafe Error (AVOID):**
```json
{
  "field": "LocalScan.Path",
  "message": "sql: Scan error on column index 3, name \"path\": converting NULL to string is unsupported"
}
```

---

## 7. Future Enhancements

### 7.1 Phase 2 Validations (After Initial Deployment)

**Advanced Path Validation:**
```go
// Validate path exists (optional)
if _, err := os.Stat(path); os.IsNotExist(err) {
	errors.Add("LocalScan.Path", "PATH_NOT_FOUND", "Path does not exist")
}

// Validate path is directory
if info, err := os.Stat(path); err == nil && !info.IsDir() {
	errors.Add("LocalScan.Path", "NOT_DIRECTORY", "Path must be a directory")
}

// Validate path is readable
if _, err := os.Open(path); err != nil {
	errors.Add("LocalScan.Path", "NOT_READABLE", "Path is not readable")
}
```

**Advanced Token Validation:**
```go
// Validate OAuth token format (JWT structure)
func ValidateOAuthTokenFormat(token string) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("invalid OAuth token format")
	}
	// Validate base64 encoding
	// ...
}

// Validate token not expired (if possible without API call)
// Validate token has required scopes
```

**Gmail Filter Validation:**
```go
// Validate Gmail search syntax
func ValidateGmailFilter(filter string) error {
	// Check for valid Gmail operators
	validOperators := []string{"from:", "to:", "subject:", "has:", "is:", "in:", "label:"}
	// Validate filter syntax
	// ...
}
```

**Drive Query Validation:**
```go
// Validate Google Drive query syntax
func ValidateDriveQuery(query string) error {
	// Check for valid Drive query operators
	// name, mimeType, modifiedTime, etc.
	// ...
}
```

### 7.2 Validation Library Migration (Optional)

**If codebase grows significantly, consider:**

```go
import "github.com/go-playground/validator/v10"

type LocalScan struct {
	Path string `validate:"required,min=1,max=1000,excludes=\x00"`
}

validate := validator.New()
err := validate.Struct(localScan)
```

**Pros:**
- Declarative validation
- Less boilerplate code
- Built-in validators

**Cons:**
- External dependency
- Less control over error messages
- Steeper learning curve

**Recommendation:** Stick with custom validation for now, reevaluate after 6 months.

### 7.3 Client-Side Validation

**Frontend can use same validation rules:**

```typescript
// ui/src/validation/scanValidation.ts

export interface ValidationError {
  field: string;
  code: string;
  message: string;
}

export function validateLocalScan(path: string): ValidationError[] {
  const errors: ValidationError[] = [];

  if (!path || path.trim() === '') {
    errors.push({
      field: 'LocalScan.Path',
      code: 'REQUIRED',
      message: 'Path is required and cannot be empty'
    });
  }

  if (path.length > 1000) {
    errors.push({
      field: 'LocalScan.Path',
      code: 'TOO_LONG',
      message: 'Path exceeds maximum length of 1000 characters'
    });
  }

  if (path.includes('\0')) {
    errors.push({
      field: 'LocalScan.Path',
      code: 'INVALID_CHARS',
      message: 'Path contains invalid null bytes'
    });
  }

  return errors;
}
```

**Benefits:**
- Faster feedback for users
- Reduced server load
- Consistent UX

**Note:** Server-side validation is still REQUIRED (never trust client).

### 7.4 Monitoring and Alerting

**Prometheus Metrics:**

```go
var (
	validationFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bhandaar_validation_failures_total",
			Help: "Total number of validation failures",
		},
		[]string{"scan_type", "field", "error_code"},
	)
)

// In validation handler
validationFailuresTotal.WithLabelValues(
	doScanRequest.ScanType,
	error.Field,
	error.Code,
).Inc()
```

**Alerting Rules:**

```yaml
groups:
  - name: bhandaar_validation
    rules:
      - alert: HighValidationFailureRate
        expr: rate(bhandaar_validation_failures_total[5m]) > 10
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "High validation failure rate"
          description: "{{ $value }} validation failures/sec"
```

---

## Appendix A: Complete File Changes Summary

### Files to Create

1. **`validation/validation.go`** - NEW
   - ValidationError and ValidationErrors types
   - Validation helper functions
   - Error code constants
   - Field validators

2. **`validation/validation_test.go`** - NEW
   - Unit tests for validation package
   - Test all helper functions
   - Edge case coverage

### Files to Modify

1. **`collect/local.go`**
   - Add Validate() method to LocalScan

2. **`collect/drive.go`**
   - Add Validate() method to GDriveScan

3. **`collect/gmail.go`**
   - Add Validate() method to GMailScan

4. **`collect/photos.go`**
   - Add Validate() method to GPhotosScan

5. **`web/api.go`**
   - Add Validate() method to DoScanRequest
   - Update DoScansHandler to call Validate()
   - Add writeValidationErrorResponse() function
   - Add ValidationErrorResponse types

6. **`collect/local_test.go`** - Add tests (or create if missing)
   - Test LocalScan.Validate()

7. **`collect/gmail_test.go`** - Add tests (or create if missing)
   - Test GMailScan.Validate()

8. **`collect/drive_test.go`** - Add tests (or create if missing)
   - Test GDriveScan.Validate()

9. **`collect/photos_test.go`** - Add tests (or create if missing)
   - Test GPhotosScan.Validate()

10. **`web/api_test.go`** - Add tests (or create if missing)
    - Test DoScanRequest.Validate()

---

## Appendix B: Validation Rules Reference

### String Length Limits

| Field | Max Length | Rationale |
|-------|------------|-----------|
| Path (LocalScan) | 1000 chars | Typical max path length in most filesystems |
| RefreshToken (all scans) | 800 chars | Google OAuth tokens typically <600 chars, 800 is safe |
| Filter (GMailScan) | 500 chars | Gmail search queries rarely exceed this |
| QueryString (GDriveScan) | 500 chars | Drive queries similar to Gmail |
| ClientKey | 100 chars | Generated keys are 12 chars, 100 allows future growth |
| Username | 100 chars | Email addresses typically <100 chars |
| AlbumId (GPhotosScan) | 100 chars | Google Photos album IDs are short |

### Error Codes

| Code | Meaning | Example |
|------|---------|---------|
| `REQUIRED` | Field is required but missing | `Path is required and cannot be empty` |
| `EMPTY` | Field is empty or whitespace only | `Path cannot be empty or whitespace only` |
| `TOO_LONG` | Field exceeds maximum length | `Path exceeds maximum length of 1000 characters` |
| `INVALID_VALUE` | Field has invalid enum value | `ScanType must be one of: Local, GDrive, GMail, GPhotos` |
| `INVALID_CHARS` | Field contains invalid characters | `Path contains invalid null bytes` |
| `INVALID_FORMAT` | Field has wrong format | `Token does not match expected format` |

---

## Appendix C: Troubleshooting Guide

### Problem: Validation errors not appearing in logs

**Diagnosis:**
```bash
# Check log level
grep "slog.SetLogLoggerLevel" main.go
# Should be at least LevelWarn
```

**Solution:**
```go
// In main.go
slog.SetLogLoggerLevel(slog.LevelWarn) // or LevelInfo for more verbose
```

### Problem: Valid requests being rejected

**Diagnosis:**
```bash
# Check validation error response
curl -v -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/test"}}'

# Look at response body for specific validation error
```

**Common Causes:**
1. Path contains hidden null bytes
2. Token has extra whitespace
3. Field name mismatch (case-sensitive)

**Solution:**
- Check exact error code and field in response
- Verify input doesn't have hidden characters
- Ensure JSON field names match exactly

### Problem: Unicode strings rejected as "too long"

**Diagnosis:**
- Unicode characters count as multiple bytes
- Validation uses `utf8.RuneCountInString()` which is correct

**Solution:**
- This is expected behavior
- Each emoji/unicode char counts as 1 character
- If issue persists, check if client is sending already-encoded string

### Problem: Performance degradation after validation

**Diagnosis:**
```bash
# Benchmark before and after
go test -bench=. ./web/
```

**Solution:**
- Validation should be negligible (<1ms per request)
- If slow, check for expensive operations in custom validators
- Profile with pprof if needed

---

**END OF DOCUMENT**
