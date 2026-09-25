# Comprehensive Testing Plan - Bhandaar Backend

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** High - Currently 0% Test Coverage

---

## Executive Summary

This document provides a comprehensive testing strategy for the Bhandaar backend codebase. Currently, the application has **0% test coverage**, which poses significant risks for production deployment and future maintenance.

**Goals:**
- Achieve 60%+ code coverage across all packages
- Establish testing infrastructure and best practices
- Enable confident refactoring and feature development
- Prevent regressions through automated testing
- Document testing patterns for future development

**Estimated Effort:**
- Infrastructure Setup: 1-2 days
- Unit Tests: 3-4 weeks
- Integration Tests: 2-3 weeks
- E2E Tests: 1-2 weeks
- **Total**: 7-10 weeks (can be done incrementally)

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Testing Strategy](#2-testing-strategy)
3. [Testing Infrastructure](#3-testing-infrastructure)
4. [Unit Testing Plan](#4-unit-testing-plan)
5. [Integration Testing Plan](#5-integration-testing-plan)
6. [End-to-End Testing Plan](#6-end-to-end-testing-plan)
7. [Test Data Management](#7-test-data-management)
8. [CI/CD Integration](#8-cicd-integration)
9. [Performance Testing](#9-performance-testing)
10. [Security Testing](#10-security-testing)
11. [Implementation Roadmap](#11-implementation-roadmap)

---

## 1. Current State Analysis

### 1.1 Current Test Coverage

```bash
$ go test -cover ./...
# No test files found

$ find . -name "*_test.go"
# Returns empty
```

**Current Coverage: 0%**

### 1.2 Codebase Structure

| Package | LOC | Complexity | Priority | Target Coverage |
|---------|-----|------------|----------|-----------------|
| `db/` | ~834 | High | Critical | 80% |
| `web/` | ~450 | Medium | High | 70% |
| `collect/` | ~600 | High | High | 70% |
| `auth/` (new) | ~200 | Medium | Critical | 90% |
| `notification/` | ~100 | Medium | Medium | 60% |
| `constants/` | ~10 | Low | Low | N/A |

**Total Lines to Test: ~2,200**

### 1.3 Testing Challenges

| Challenge | Impact | Mitigation |
|-----------|--------|------------|
| External API dependencies (Google) | High | Mocking, test credentials |
| Database interactions | High | Test database, transactions |
| File system operations | Medium | Temp directories, cleanup |
| Concurrent operations | High | Race detector, deterministic tests |
| OAuth flow | High | Mock OAuth server |
| SSE connections | Medium | Test WebSocket connections |
| Long-running scans | Medium | Smaller test datasets |

---

## 2. Testing Strategy

### 2.1 Testing Pyramid

```
        /\
       /  \      E2E Tests (10%)
      /    \     - Full user workflows
     /------\    - Real browser automation
    /        \
   /  Integration Tests (30%)
  /   - API endpoints
 /    - Database operations
/-----\- Service interactions
|      |
|      | Unit Tests (60%)
|      | - Individual functions
|______|  - Business logic
         - Data transformations
```

### 2.2 Test Categories

#### Unit Tests (60% of test effort)
- **Scope**: Individual functions, methods, data structures
- **Dependencies**: Mocked or stubbed
- **Speed**: Very fast (<1ms per test)
- **Examples**:
  - JWT token generation/validation
  - Data validation functions
  - Utility functions
  - Struct methods

#### Integration Tests (30% of test effort)
- **Scope**: Multiple components working together
- **Dependencies**: Real database, mocked external APIs
- **Speed**: Fast (10-100ms per test)
- **Examples**:
  - API endpoints with database
  - OAuth flow with mock Google
  - Scan operations with test files
  - SSE event delivery

#### End-to-End Tests (10% of test effort)
- **Scope**: Complete user workflows
- **Dependencies**: Full stack (may use staging environment)
- **Speed**: Slow (seconds to minutes)
- **Examples**:
  - OAuth login → Create scan → View results
  - Complete Gmail scan workflow
  - Multi-user data isolation

### 2.3 Test Quality Standards

Every test should be:
- ✅ **Fast**: Unit tests <10ms, integration tests <100ms
- ✅ **Isolated**: No dependency on other tests
- ✅ **Repeatable**: Same input = same output, always
- ✅ **Self-checking**: Automatic pass/fail (no manual verification)
- ✅ **Timely**: Written before or immediately after code

---

## 3. Testing Infrastructure

### 3.1 Required Tools and Libraries

```go
// go.mod additions
require (
    github.com/stretchr/testify v1.8.4         // Assertions and mocks
    github.com/DATA-DOG/go-sqlmock v1.5.0      // Database mocking
    github.com/golang/mock v1.6.0              // Interface mocking
    github.com/google/uuid v1.5.0              // UUID generation
    github.com/jarcoal/httpmock v1.3.1         // HTTP mocking
    github.com/ory/dockertest/v3 v3.10.0       // Docker containers for tests
    github.com/testcontainers/testcontainers-go v0.27.0 // Alternative to dockertest
)
```

### 3.2 Test Database Setup

**Option 1: In-Memory SQLite (Fast, Limited)**
```go
// test/testdb/sqlite.go
package testdb

import (
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
)

func NewTestDB() (*sql.DB, error) {
    db, err := sql.Open("sqlite3", ":memory:")
    if err != nil {
        return nil, err
    }

    // Run migrations
    if err := runMigrations(db); err != nil {
        return nil, err
    }

    return db, nil
}
```

**PostgreSQL Test Database Setup**

This approach uses a dedicated PostgreSQL database for testing. Requires PostgreSQL 15+ running locally or in CI environment.

**Prerequisites:**
```bash
# Install PostgreSQL locally (macOS)
brew install postgresql@15

# Start PostgreSQL service
brew services start postgresql@15

# Create test database and user
psql postgres -c "CREATE DATABASE hdd_test;"
psql postgres -c "CREATE USER hddb_test WITH PASSWORD 'testpass';"
psql postgres -c "GRANT ALL PRIVILEGES ON DATABASE hdd_test TO hddb_test;"
```

**For CI (GitHub Actions):**
```yaml
# .github/workflows/test.yml
services:
  postgres:
    image: postgres:15-alpine
    env:
      POSTGRES_USER: hddb_test
      POSTGRES_PASSWORD: testpass
      POSTGRES_DB: hdd_test
    options: >-
      --health-cmd pg_isready
      --health-interval 10s
      --health-timeout 5s
      --health-retries 5
    ports:
      - 5432:5432
```

**Test Database Helper:**
```go
// test/testdb/postgres.go
package testdb

import (
    "database/sql"
    "fmt"
    "os"

    "github.com/jmoiron/sqlx"
    _ "github.com/lib/pq"
)

// GetTestDatabaseURL returns the test database connection string
// Uses environment variable TEST_DATABASE_URL if set, otherwise uses default
func GetTestDatabaseURL() string {
    if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
        return url
    }
    return "postgres://hddb_test:testpass@localhost:5432/hdd_test?sslmode=disable"
}

// NewTestDB creates a test database connection and runs migrations
func NewTestDB() (*sqlx.DB, func(), error) {
    db, err := sqlx.Open("postgres", GetTestDatabaseURL())
    if err != nil {
        return nil, nil, fmt.Errorf("failed to open test database: %w", err)
    }

    // Verify connection
    if err := db.Ping(); err != nil {
        db.Close()
        return nil, nil, fmt.Errorf("failed to ping test database: %w", err)
    }

    // Run migrations
    if err := runMigrations(db); err != nil {
        db.Close()
        return nil, nil, fmt.Errorf("failed to run migrations: %w", err)
    }

    // Cleanup function to truncate all tables
    cleanup := func() {
        // Truncate all tables in reverse dependency order
        tables := []string{
            "scandata",
            "messagemetadata",
            "photometadata",
            "videometadata",
            "photosmediaitem",
            "scanmetadata",
            "scans",
            "privatetokens",
        }

        for _, table := range tables {
            db.Exec(fmt.Sprintf("TRUNCATE TABLE %s CASCADE", table))
        }

        db.Close()
    }

    return db, cleanup, nil
}

// runMigrations runs database migrations for tests
func runMigrations(db *sqlx.DB) error {
    // Use the same migration logic from db/database.go
    // This ensures test database schema matches production

    // For now, import and call the existing migrateDB() function
    // Alternatively, read migration SQL files from db/migrations/

    return nil // Implementation matches production migrations
}
```

### 3.3 Test Helpers and Fixtures

```go
// test/helpers/helpers.go
package helpers

import (
    "testing"

    "github.com/google/uuid"
    "github.com/jyothri/hdd/db"
)

// CreateTestUser creates a user for testing
func CreateTestUser(t *testing.T, email string) *db.User {
    t.Helper()

    user, err := db.CreateUser(email, email, "google-id-"+uuid.New().String())
    if err != nil {
        t.Fatalf("Failed to create test user: %v", err)
    }

    return user
}

// CreateTestScan creates a scan for testing
func CreateTestScan(t *testing.T, userID uuid.UUID, scanType string) int {
    t.Helper()

    scanID, err := db.LogStartScan(scanType, userID)
    if err != nil {
        t.Fatalf("Failed to create test scan: %v", err)
    }

    return scanID
}

// AssertNoError fails the test if err is not nil
func AssertNoError(t *testing.T, err error) {
    t.Helper()
    if err != nil {
        t.Fatalf("Expected no error, got: %v", err)
    }
}

// AssertError fails the test if err is nil
func AssertError(t *testing.T, err error) {
    t.Helper()
    if err == nil {
        t.Fatal("Expected error, got nil")
    }
}
```

### 3.4 Mock Factories

```go
// test/mocks/google.go
package mocks

import (
    "net/http"
    "net/http/httptest"

    "github.com/jarcoal/httpmock"
)

// MockGoogleOAuthServer creates a mock OAuth server
func MockGoogleOAuthServer() *httptest.Server {
    mux := http.NewServeMux()

    mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{
            "access_token": "mock-access-token",
            "refresh_token": "mock-refresh-token",
            "expires_in": 3600,
            "token_type": "Bearer",
            "scope": "https://www.googleapis.com/auth/drive.readonly"
        }`))
    })

    mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write([]byte(`{
            "email": "test@example.com",
            "id": "google-user-123"
        }`))
    })

    return httptest.NewServer(mux)
}

// MockGmailAPI mocks Gmail API responses
func MockGmailAPI(httpmock *httpmock.MockTransport) {
    // Mock list messages
    httpmock.RegisterResponder("GET", "https://gmail.googleapis.com/gmail/v1/users/me/messages",
        httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
            "messages": []map[string]interface{}{
                {"id": "msg1", "threadId": "thread1"},
                {"id": "msg2", "threadId": "thread2"},
            },
            "nextPageToken": "",
        }))

    // Mock get message
    httpmock.RegisterResponder("GET", `=~^https://gmail.googleapis.com/gmail/v1/users/me/messages/`,
        httpmock.NewJsonResponderOrPanic(200, map[string]interface{}{
            "id": "msg1",
            "threadId": "thread1",
            "sizeEstimate": 12345,
            "payload": map[string]interface{}{
                "headers": []map[string]interface{}{
                    {"name": "From", "value": "sender@example.com"},
                    {"name": "To", "value": "recipient@example.com"},
                    {"name": "Subject", "value": "Test Email"},
                    {"name": "Date", "value": "Mon, 21 Dec 2025 10:00:00 +0000"},
                },
            },
            "labelIds": []string{"INBOX", "UNREAD"},
        }))
}
```

---

## 4. Unit Testing Plan

### 4.1 Package: `auth/`

**Target Coverage: 90%**

#### Test Files to Create

**`auth/jwt_test.go`**
```go
package auth

import (
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestGenerateToken(t *testing.T) {
    tests := []struct {
        name        string
        userID      uuid.UUID
        email       string
        displayName string
        wantErr     bool
    }{
        {
            name:        "valid user",
            userID:      uuid.New(),
            email:       "test@example.com",
            displayName: "Test User",
            wantErr:     false,
        },
        {
            name:        "empty email",
            userID:      uuid.New(),
            email:       "",
            displayName: "Test User",
            wantErr:     false, // Email not validated in token generation
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            jwtManager := NewJWTManager("test-secret", "test-issuer")

            token, err := jwtManager.GenerateToken(tt.userID, tt.email, tt.displayName)

            if tt.wantErr {
                assert.Error(t, err)
                assert.Empty(t, token)
            } else {
                assert.NoError(t, err)
                assert.NotEmpty(t, token)
            }
        })
    }
}

func TestValidateToken_Success(t *testing.T) {
    jwtManager := NewJWTManager("test-secret", "test-issuer")
    userID := uuid.New()
    email := "test@example.com"
    displayName := "Test User"

    token, err := jwtManager.GenerateToken(userID, email, displayName)
    require.NoError(t, err)

    claims, err := jwtManager.ValidateToken(token)

    assert.NoError(t, err)
    assert.Equal(t, userID.String(), claims.UserID)
    assert.Equal(t, email, claims.Email)
    assert.Equal(t, displayName, claims.DisplayName)
    assert.Equal(t, "test-issuer", claims.Issuer)
}

func TestValidateToken_Expired(t *testing.T) {
    // This test requires time manipulation - skip for now
    // Consider using github.com/benbjohnson/clock for time mocking
    t.Skip("Requires time mocking library")
}

func TestValidateToken_InvalidFormat(t *testing.T) {
    jwtManager := NewJWTManager("test-secret", "test-issuer")

    tests := []struct {
        name  string
        token string
    }{
        {"empty token", ""},
        {"malformed token", "not.a.jwt"},
        {"random string", "foobar"},
        {"missing parts", "header.payload"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            _, err := jwtManager.ValidateToken(tt.token)
            assert.Error(t, err)
        })
    }
}

func TestValidateToken_WrongSecret(t *testing.T) {
    jwtManager1 := NewJWTManager("secret1", "issuer")
    jwtManager2 := NewJWTManager("secret2", "issuer")

    userID := uuid.New()
    token, err := jwtManager1.GenerateToken(userID, "test@example.com", "Test")
    require.NoError(t, err)

    _, err = jwtManager2.ValidateToken(token)
    assert.Error(t, err)
}

func TestShouldRefresh(t *testing.T) {
    // Test refresh logic
    jwtManager := NewJWTManager("test-secret", "test-issuer")

    tests := []struct {
        name           string
        expiresAt      time.Time
        shouldRefresh  bool
    }{
        {
            name:          "just issued - no refresh",
            expiresAt:     time.Now().Add(1 * time.Hour),
            shouldRefresh: false,
        },
        {
            name:          "5 minutes left - should refresh",
            expiresAt:     time.Now().Add(5 * time.Minute),
            shouldRefresh: true,
        },
        {
            name:          "already expired",
            expiresAt:     time.Now().Add(-1 * time.Minute),
            shouldRefresh: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            claims := &Claims{
                RegisteredClaims: jwt.RegisteredClaims{
                    ExpiresAt: jwt.NewNumericDate(tt.expiresAt),
                },
            }

            result := jwtManager.ShouldRefresh(claims)
            assert.Equal(t, tt.shouldRefresh, result)
        })
    }
}
```

**`auth/context_test.go`**
```go
package auth

import (
    "context"
    "testing"

    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
)

func TestUserIDContext(t *testing.T) {
    ctx := context.Background()
    userID := uuid.New()

    // Set user ID
    ctx = SetUserID(ctx, userID)

    // Get user ID
    retrievedID, err := GetUserID(ctx)
    assert.NoError(t, err)
    assert.Equal(t, userID, retrievedID)
}

func TestUserIDContext_NotSet(t *testing.T) {
    ctx := context.Background()

    _, err := GetUserID(ctx)
    assert.Error(t, err)
    assert.Contains(t, err.Error(), "not found in context")
}

func TestClaimsContext(t *testing.T) {
    ctx := context.Background()
    claims := &Claims{
        UserID: uuid.New().String(),
        Email:  "test@example.com",
    }

    // Set claims
    ctx = SetClaims(ctx, claims)

    // Get claims
    retrievedClaims, err := GetClaims(ctx)
    assert.NoError(t, err)
    assert.Equal(t, claims.UserID, retrievedClaims.UserID)
    assert.Equal(t, claims.Email, retrievedClaims.Email)
}
```

**Tests to Write:**
- ✅ JWT generation with valid data
- ✅ JWT validation with valid token
- ✅ JWT validation with expired token
- ✅ JWT validation with invalid signature
- ✅ JWT validation with malformed token
- ✅ Refresh logic
- ✅ Context get/set operations
- ✅ Error cases for missing context values

**Estimated Effort**: 1 day

### 4.2 Package: `db/`

**Target Coverage: 80%**

#### Test Files to Create

**`db/database_test.go`**
```go
package db

import (
    "testing"

    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "github.com/jyothri/hdd/test/testdb"
)

func setupTestDB(t *testing.T) (*sql.DB, func()) {
    t.Helper()

    db, cleanup, err := testdb.NewTestPostgres()
    require.NoError(t, err)

    return db, cleanup
}

func TestCreateUser(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    email := "test@example.com"
    displayName := "Test User"
    googleID := "google-123"

    user, err := CreateUser(email, displayName, googleID)

    require.NoError(t, err)
    assert.NotEqual(t, uuid.Nil, user.ID)
    assert.Equal(t, email, user.Email)
    assert.Equal(t, displayName, user.DisplayName)
    assert.Equal(t, googleID, user.GoogleID.String)
    assert.True(t, user.IsActive)
    assert.False(t, user.IsSystemUser)
}

func TestCreateUser_Duplicate(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    email := "test@example.com"

    // Create first user
    user1, err := CreateUser(email, "User 1", "google-1")
    require.NoError(t, err)

    // Create duplicate (should return existing user)
    user2, err := CreateUser(email, "User 2", "google-2")
    require.NoError(t, err)

    assert.Equal(t, user1.ID, user2.ID)
    assert.Equal(t, user1.Email, user2.Email)
}

func TestGetUserByEmail(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    email := "test@example.com"
    createdUser, err := CreateUser(email, "Test", "google-123")
    require.NoError(t, err)

    retrievedUser, err := GetUserByEmail(email)

    require.NoError(t, err)
    assert.Equal(t, createdUser.ID, retrievedUser.ID)
    assert.Equal(t, createdUser.Email, retrievedUser.Email)
}

func TestGetUserByEmail_NotFound(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    _, err := GetUserByEmail("nonexistent@example.com")
    assert.Error(t, err)
}

func TestLogStartScan(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    user, err := CreateUser("test@example.com", "Test", "google-123")
    require.NoError(t, err)

    scanID, err := LogStartScan("gmail", user.ID)

    require.NoError(t, err)
    assert.Greater(t, scanID, 0)
}

func TestUserOwnsScan(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    user1, _ := CreateUser("user1@example.com", "User 1", "google-1")
    user2, _ := CreateUser("user2@example.com", "User 2", "google-2")

    scanID, err := LogStartScan("local", user1.ID)
    require.NoError(t, err)

    // User 1 owns the scan
    owns, err := UserOwnsScan(user1.ID, scanID)
    assert.NoError(t, err)
    assert.True(t, owns)

    // User 2 does not own the scan
    owns, err = UserOwnsScan(user2.ID, scanID)
    assert.NoError(t, err)
    assert.False(t, owns)
}

func TestDeleteScan_Transaction(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    user, _ := CreateUser("test@example.com", "Test", "google-123")
    scanID, _ := LogStartScan("local", user.ID)

    // Add some scan data
    SaveScanMetadata("test-scan", "/path", "*", scanID)
    // ... add more test data

    err := DeleteScan(scanID)
    require.NoError(t, err)

    // Verify scan is deleted
    scan, err := GetScanById(scanID)
    assert.Error(t, err) // Should not exist

    // Verify cascade delete worked
    // ... check child tables
}

func TestMarkScanCompleted(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    user, _ := CreateUser("test@example.com", "Test", "google-123")
    scanID, _ := LogStartScan("local", user.ID)

    err := MarkScanCompleted(scanID)
    require.NoError(t, err)

    scan, err := GetScanById(scanID)
    require.NoError(t, err)
    assert.Equal(t, "Completed", scan.Status)
    assert.NotNil(t, scan.ScanEndTime)
}

func TestMarkScanFailed(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    user, _ := CreateUser("test@example.com", "Test", "google-123")
    scanID, _ := LogStartScan("local", user.ID)

    errorMsg := "Test error message"
    err := MarkScanFailed(scanID, errorMsg)
    require.NoError(t, err)

    scan, err := GetScanById(scanID)
    require.NoError(t, err)
    assert.Equal(t, "Failed", scan.Status)
    assert.Equal(t, errorMsg, scan.ErrorMsg.String)
}
```

**Tests to Write:**
- User management (CRUD)
- Scan operations (create, list, delete)
- Ownership verification
- Scan status tracking
- Message metadata operations
- Photos metadata operations
- Transaction handling
- Migration functions
- Error cases

**Estimated Effort**: 1 week

### 4.3 Package: `web/`

**Target Coverage: 70%**

**`web/middleware_test.go`**
```go
package web

import (
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/google/uuid"
    "github.com/jyothri/hdd/auth"
    "github.com/stretchr/testify/assert"
)

func TestAuthMiddleware_ValidToken(t *testing.T) {
    jwtManager := auth.NewJWTManager("test-secret", "test-issuer")
    userID := uuid.New()
    token, _ := jwtManager.GenerateToken(userID, "test@example.com", "Test User")

    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Verify user ID is in context
        retrievedID, err := auth.GetUserID(r.Context())
        assert.NoError(t, err)
        assert.Equal(t, userID, retrievedID)

        w.WriteHeader(http.StatusOK)
    })

    middleware := AuthMiddleware(jwtManager)
    wrapped := middleware(handler)

    req := httptest.NewRequest("GET", "/test", nil)
    req.Header.Set("Authorization", "Bearer "+token)
    w := httptest.NewRecorder()

    wrapped.ServeHTTP(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_MissingToken(t *testing.T) {
    jwtManager := auth.NewJWTManager("test-secret", "test-issuer")

    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        t.Fatal("Handler should not be called")
    })

    middleware := AuthMiddleware(jwtManager)
    wrapped := middleware(handler)

    req := httptest.NewRequest("GET", "/test", nil)
    w := httptest.NewRecorder()

    wrapped.ServeHTTP(w, req)

    assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
    jwtManager := auth.NewJWTManager("test-secret", "test-issuer")

    handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        t.Fatal("Handler should not be called")
    })

    middleware := AuthMiddleware(jwtManager)
    wrapped := middleware(handler)

    req := httptest.NewRequest("GET", "/test", nil)
    req.Header.Set("Authorization", "Bearer invalid-token")
    w := httptest.NewRecorder()

    wrapped.ServeHTTP(w, req)

    assert.Equal(t, http.StatusUnauthorized, w.Code)
}
```

**Tests to Write:**
- Middleware authentication
- Middleware authorization
- Request/response helpers
- Error handling
- JSON serialization

**Estimated Effort**: 3-4 days

### 4.4 Package: `collect/`

**Target Coverage: 70%**

**`collect/common_test.go`**
```go
package collect

import (
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestIsRetryError(t *testing.T) {
    tests := []struct {
        name     string
        err      error
        expected bool
    }{
        // Add test cases based on actual implementation
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := isRetryError(tt.err)
            assert.Equal(t, tt.expected, result)
        })
    }
}
```

**Tests to Write:**
- Gmail service creation
- Drive service creation
- Photos service creation
- Retry logic
- Error handling
- Data transformation

**Estimated Effort**: 1 week

---

## 5. Integration Testing Plan

### 5.1 API Endpoint Tests

**`web/api_integration_test.go`**
```go
package web

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/google/uuid"
    "github.com/gorilla/mux"
    "github.com/jyothri/hdd/auth"
    "github.com/jyothri/hdd/db"
    "github.com/jyothri/hdd/test/testdb"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestAPI_CreateScan_EndToEnd(t *testing.T) {
    // Setup test database
    testDB, cleanup := testdb.NewTestPostgres()
    defer cleanup()

    // Create test user
    user, err := db.CreateUser("test@example.com", "Test User", "google-123")
    require.NoError(t, err)

    // Generate JWT token
    jwtManager := auth.NewJWTManager("test-secret", "test-issuer")
    token, err := jwtManager.GenerateToken(user.ID, user.Email, user.DisplayName)
    require.NoError(t, err)

    // Create scan request
    scanRequest := DoScanRequest{
        ScanType: "Local",
        LocalScan: collect.LocalScan{
            Source: "/tmp/test",
        },
    }
    body, _ := json.Marshal(scanRequest)

    // Make request
    req := httptest.NewRequest("POST", "/api/scans", bytes.NewBuffer(body))
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Content-Type", "application/json")

    w := httptest.NewRecorder()

    // Setup router
    router := mux.NewRouter()
    router.Use(AuthMiddleware(jwtManager))
    router.HandleFunc("/api/scans", DoScansHandler).Methods("POST")

    router.ServeHTTP(w, req)

    // Assert response
    assert.Equal(t, http.StatusOK, w.Code)

    var response DoScanResponse
    err = json.Unmarshal(w.Body.Bytes(), &response)
    require.NoError(t, err)
    assert.Greater(t, response.ScanId, 0)

    // Verify scan was created in database
    scan, err := db.GetScanById(response.ScanId)
    require.NoError(t, err)
    assert.Equal(t, user.ID, scan.UserId.UUID)
}

func TestAPI_DeleteScan_Unauthorized(t *testing.T) {
    testDB, cleanup := testdb.NewTestPostgres()
    defer cleanup()

    // Create two users
    user1, _ := db.CreateUser("user1@example.com", "User 1", "google-1")
    user2, _ := db.CreateUser("user2@example.com", "User 2", "google-2")

    // User 1 creates a scan
    scanID, _ := db.LogStartScan("local", user1.ID)

    // User 2 tries to delete it
    jwtManager := auth.NewJWTManager("test-secret", "test-issuer")
    token, _ := jwtManager.GenerateToken(user2.ID, user2.Email, user2.DisplayName)

    req := httptest.NewRequest("DELETE", "/api/scans/"+string(scanID), nil)
    req.Header.Set("Authorization", "Bearer "+token)
    req = mux.SetURLVars(req, map[string]string{"scan_id": string(scanID)})

    w := httptest.NewRecorder()

    router := mux.NewRouter()
    router.Use(AuthMiddleware(jwtManager))
    router.HandleFunc("/api/scans/{scan_id}", DeleteScanHandler).Methods("DELETE")

    router.ServeHTTP(w, req)

    // Should be forbidden
    assert.Equal(t, http.StatusForbidden, w.Code)
}
```

**Tests to Cover:**
- ✅ Create scan with auth
- ✅ List scans (user sees only their own)
- ✅ Get scan data with ownership check
- ✅ Delete scan with ownership check
- ✅ Unauthorized access attempts
- ✅ OAuth flow integration
- ✅ SSE event delivery

**Estimated Effort**: 1 week

### 5.2 Database Integration Tests

**`db/integration_test.go`**
```go
package db

import (
    "testing"

    "github.com/stretchr/testify/assert"
)

func TestCompleteGmailScanWorkflow(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    // Create user
    user, _ := CreateUser("test@example.com", "Test", "google-123")

    // Start scan
    scanID, err := LogStartScan("gmail", user.ID)
    require.NoError(t, err)

    // Simulate scan progress
    messageMetaData := make(chan MessageMetadata, 10)
    go SaveMessageMetadataToDb(scanID, user.Email, messageMetaData)

    // Send test messages
    messageMetaData <- MessageMetadata{
        MessageId: "msg1",
        ThreadId:  "thread1",
        From:      "sender@example.com",
        To:        "recipient@example.com",
        Subject:   "Test Email 1",
        SizeEstimate: 1024,
    }
    messageMetaData <- MessageMetadata{
        MessageId: "msg2",
        ThreadId:  "thread2",
        From:      "sender2@example.com",
        To:        "recipient2@example.com",
        Subject:   "Test Email 2",
        SizeEstimate: 2048,
    }
    close(messageMetaData)

    // Wait for processing
    time.Sleep(100 * time.Millisecond)

    // Mark scan complete
    err = MarkScanCompleted(scanID)
    require.NoError(t, err)

    // Verify results
    messages, count, err := GetMessageMetadataFromDb(scanID, 1)
    require.NoError(t, err)
    assert.Equal(t, 2, count)
    assert.Len(t, messages, 2)

    // Verify scan status
    scan, _ := GetScanById(scanID)
    assert.Equal(t, "Completed", scan.Status)
}
```

**Estimated Effort**: 4-5 days

---

## 6. End-to-End Testing Plan

### 6.1 E2E Test Framework

Use `httptest` for full HTTP server testing:

```go
// test/e2e/server_test.go
package e2e

import (
    "net/http/httptest"
    "testing"

    "github.com/jyothri/hdd/web"
)

func setupTestServer(t *testing.T) *httptest.Server {
    // Setup test database
    // Initialize server
    // Return test server
}

func TestE2E_CompleteUserJourney(t *testing.T) {
    server := setupTestServer(t)
    defer server.Close()

    // 1. OAuth login
    // 2. Create scan
    // 3. Wait for scan completion
    // 4. Retrieve scan results
    // 5. Delete scan
    // 6. Logout
}
```

**Estimated Effort**: 1 week

---

## 7. Test Data Management

### 7.1 Test Fixtures

Create reusable test data:

```go
// test/fixtures/users.go
package fixtures

var (
    TestUser1 = User{
        Email:       "user1@example.com",
        DisplayName: "Test User 1",
        GoogleID:    "google-user-1",
    }

    TestUser2 = User{
        Email:       "user2@example.com",
        DisplayName: "Test User 2",
        GoogleID:    "google-user-2",
    }

    SystemUser = User{
        Email:        "system@bhandaar.local",
        DisplayName:  "System User",
        IsSystemUser: true,
    }
)
```

### 7.2 Test Data Cleanup

Ensure tests clean up after themselves:

```go
func TestWithCleanup(t *testing.T) {
    db, cleanup := setupTestDB(t)
    defer cleanup()

    // Or use t.Cleanup
    t.Cleanup(func() {
        // Clean up test data
    })
}
```

---

## 8. CI/CD Integration

### 8.1 GitHub Actions Workflow

```yaml
# .github/workflows/tests.yml
name: Tests

on:
  push:
    branches: [ main ]
  pull_request:
    branches: [ main ]

jobs:
  test:
    runs-on: ubuntu-latest

    services:
      postgres:
        image: postgres:15-alpine
        env:
          POSTGRES_PASSWORD: testpass
          POSTGRES_USER: testuser
          POSTGRES_DB: testdb
        options: >-
          --health-cmd pg_isready
          --health-interval 10s
          --health-timeout 5s
          --health-retries 5
        ports:
          - 5432:5432

    steps:
    - uses: actions/checkout@v3

    - name: Set up Go
      uses: actions/setup-go@v4
      with:
        go-version: '1.21'

    - name: Install dependencies
      run: |
        cd be
        go mod download

    - name: Run tests
      env:
        DB_HOST: localhost
        DB_PORT: 5432
        DB_USER: testuser
        DB_PASSWORD: testpass
        DB_NAME: testdb
        JWT_SECRET: test-secret-for-ci
      run: |
        cd be
        go test -v -race -coverprofile=coverage.out ./...

    - name: Upload coverage
      uses: codecov/codecov-action@v3
      with:
        files: ./be/coverage.out
        flags: backend

    - name: Check coverage threshold
      run: |
        cd be
        go tool cover -func=coverage.out | grep total | awk '{print $3}' | sed 's/%//' | awk '{if ($1 < 60) exit 1}'
```

### 8.2 Pre-commit Hooks

```bash
# .git/hooks/pre-commit
#!/bin/bash

cd be
go test ./... || exit 1
go vet ./... || exit 1
```

---

## 9. Performance Testing

### 9.1 Benchmark Tests

```go
// db/database_benchmark_test.go
package db

import (
    "testing"
)

func BenchmarkLogStartScan(b *testing.B) {
    db, cleanup := setupTestDB(b)
    defer cleanup()

    user, _ := CreateUser("bench@example.com", "Bench", "google-bench")

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        LogStartScan("local", user.ID)
    }
}

func BenchmarkGetScansFromDb(b *testing.B) {
    db, cleanup := setupTestDB(b)
    defer cleanup()

    user, _ := CreateUser("bench@example.com", "Bench", "google-bench")

    // Create test data
    for i := 0; i < 100; i++ {
        LogStartScan("local", user.ID)
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        GetScansFromDb(user.ID, 1)
    }
}
```

### 9.2 Load Testing

Use `k6` or `vegeta` for HTTP load testing:

```javascript
// test/load/api_load.js
import http from 'k6/http';
import { check } from 'k6';

export let options = {
  vus: 10,
  duration: '30s',
};

export default function() {
  const token = 'YOUR_JWT_TOKEN';
  const params = {
    headers: {
      'Authorization': `Bearer ${token}`,
    },
  };

  let res = http.get('http://localhost:8090/api/scans', params);
  check(res, {
    'status is 200': (r) => r.status === 200,
    'response time < 200ms': (r) => r.timings.duration < 200,
  });
}
```

---

## 10. Security Testing

### 10.1 Security Test Cases

```go
// test/security/auth_test.go
package security

func TestSQLInjection_Protection(t *testing.T) {
    // Test SQL injection attempts
    maliciousInputs := []string{
        "1' OR '1'='1",
        "'; DROP TABLE users; --",
        "1 UNION SELECT * FROM users",
    }

    for _, input := range maliciousInputs {
        // Attempt injection
        // Verify it's blocked/escaped
    }
}

func TestXSS_Protection(t *testing.T) {
    // Test XSS attempts in JSON responses
}

func TestBruteForce_RateLimit(t *testing.T) {
    // Test rate limiting prevents brute force
}
```

---

## 11. Implementation Roadmap

### Phase 1: Foundation (Week 1-2)
- [ ] Setup test infrastructure
- [ ] Create test database utilities
- [ ] Setup CI/CD pipeline
- [ ] Write test helpers and fixtures

### Phase 2: Unit Tests - Critical Packages (Week 3-5)
- [ ] `auth/` package tests (90% coverage)
- [ ] `db/` package tests (80% coverage)
- [ ] `web/middleware` tests

### Phase 3: Unit Tests - Remaining Packages (Week 6-7)
- [ ] `web/api` tests
- [ ] `collect/` package tests
- [ ] `notification/` package tests

### Phase 4: Integration Tests (Week 8-9)
- [ ] API endpoint integration tests
- [ ] Database integration tests
- [ ] OAuth flow integration tests

### Phase 5: E2E Tests (Week 10)
- [ ] Complete user journey tests
- [ ] Multi-user scenarios
- [ ] Error scenarios

### Phase 6: Documentation & Maintenance (Week 11)
- [ ] Test documentation
- [ ] Coverage reports
- [ ] Test maintenance guidelines

---

## Appendix A: Coverage Targets

| Package | Current | Target | Priority |
|---------|---------|--------|----------|
| `auth/` | 0% | 90% | Critical |
| `db/` | 0% | 80% | Critical |
| `web/` | 0% | 70% | High |
| `collect/` | 0% | 70% | High |
| `notification/` | 0% | 60% | Medium |
| **Overall** | **0%** | **60%+** | - |

---

## Appendix B: Test Commands

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run with detailed coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run with race detector
go test -race ./...

# Run specific package
go test ./db/...

# Run specific test
go test -run TestCreateUser ./db/...

# Run benchmarks
go test -bench=. ./...

# Verbose output
go test -v ./...

# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out

# Check coverage threshold
go tool cover -func=coverage.out | grep total
```

---

**END OF DOCUMENT**
