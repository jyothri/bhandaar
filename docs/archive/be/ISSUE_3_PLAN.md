# Issue #3 Implementation Plan: Authentication & Authorization

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P0 - Critical Security Issue

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #3: No Authentication/Authorization on API Endpoints**. The current system allows anyone to access, modify, or delete any user's data without authentication.

**Selected Approach:**
- **Authentication**: JWT tokens based on OAuth identity (Option C)
- **User Model**: New `users` table with proper user management (Option C)
- **Authorization**: User can only access their own scans/data (Option A)
- **Frontend Integration**: HTTP Authorization header with Bearer token (Option A)
- **Backward Compatibility**: Existing scans assigned to system user (Option A)
- **Timeline**: Phase 1 + Phase 2 comprehensive implementation

**Estimated Effort:**
- Phase 1: 2-3 days (Basic JWT + Ownership)
- Phase 2: 3-4 days (Comprehensive security + hardening)
- **Total**: 5-7 days

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Phase 1: Basic JWT Authentication & Ownership](#3-phase-1-basic-jwt-authentication--ownership)
4. [Phase 2: Comprehensive Security](#4-phase-2-comprehensive-security)
5. [Database Schema Changes](#5-database-schema-changes)
6. [Implementation Details](#6-implementation-details)
7. [Migration Strategy](#7-migration-strategy)
8. [Testing Strategy](#8-testing-strategy)
9. [Rollout Plan](#9-rollout-plan)
10. [Security Considerations](#10-security-considerations)

---

## 1. Current State Analysis

### 1.1 Vulnerabilities

| Endpoint | Vulnerability | Impact |
|----------|---------------|--------|
| `POST /api/scans` | No auth check | Anyone can create scans |
| `GET /api/scans` | No auth check | Anyone can list all scans |
| `DELETE /api/scans/{id}` | No ownership check | Anyone can delete any scan |
| `GET /api/scans/{id}` | No ownership check | Anyone can view any scan data |
| `GET /api/gmaildata/{id}` | No ownership check | Anyone can read any user's emails |
| `GET /api/photos/{id}` | No ownership check | Anyone can view any user's photos |
| `GET /api/accounts` | No auth check | Anyone can list all accounts |
| `DELETE /api/accounts/{key}` | Not implemented but vulnerable | Would allow deletion of any account |

### 1.2 Current Database Schema (Relevant Tables)

```sql
-- OAuth tokens with client_key as identifier
CREATE TABLE privatetokens (
    id serial PRIMARY KEY,
    access_token VARCHAR(800),
    refresh_token VARCHAR(800),
    display_name VARCHAR(100),
    client_key VARCHAR(100) NOT NULL UNIQUE,  -- Current user identifier
    created_on TIMESTAMP NOT NULL,
    scope VARCHAR(500),
    expires_in INT,
    token_type VARCHAR(100)
);

-- Scans have NO user ownership
CREATE TABLE scans (
    id serial PRIMARY KEY,
    scan_type VARCHAR(50) NOT NULL,
    created_on TIMESTAMP NOT NULL,
    scan_start_time TIMESTAMP NOT NULL,
    scan_end_time TIMESTAMP,
    status VARCHAR(50) DEFAULT 'Completed',
    error_msg TEXT,
    completed_at TIMESTAMP
);

-- Scan metadata has 'name' field (sometimes username)
CREATE TABLE scanmetadata (
    id serial PRIMARY KEY,
    name VARCHAR(200),              -- Sometimes contains username
    search_path VARCHAR(2000),
    search_filter VARCHAR(2000),
    scan_id INT NOT NULL,
    FOREIGN KEY (scan_id) REFERENCES scans(id)
);
```

### 1.3 Current OAuth Flow

1. User visits `/oauth/authorize` → redirected to Google
2. Google redirects to `/api/glink` callback with authorization code
3. Backend exchanges code for access_token + refresh_token
4. Backend generates random `client_key` (12 chars)
5. Tokens stored in `privatetokens` table with `client_key`
6. Frontend receives redirect to `/request` page

**Key Issue**: OAuth flow creates account but provides NO authentication token to frontend!

---

## 2. Target Architecture

### 2.1 Authentication Flow

```
┌─────────┐                 ┌─────────┐                ┌─────────┐
│ Browser │                 │ Backend │                │ Google  │
└────┬────┘                 └────┬────┘                └────┬────┘
     │                           │                          │
     │  1. GET /oauth/authorize  │                          │
     ├──────────────────────────>│                          │
     │                           │                          │
     │  2. Redirect to Google    │                          │
     │<──────────────────────────┤                          │
     │                           │                          │
     │  3. User approves         │                          │
     ├──────────────────────────────────────────────────────>│
     │                           │                          │
     │  4. Redirect to /api/glink with code                 │
     │<─────────────────────────────────────────────────────┤
     │                           │                          │
     │  5. GET /api/glink?code=...                          │
     ├──────────────────────────>│                          │
     │                           │                          │
     │                           │  6. Exchange code        │
     │                           ├─────────────────────────>│
     │                           │                          │
     │                           │  7. Return tokens        │
     │                           │<─────────────────────────┤
     │                           │                          │
     │                           │  8. Get user email       │
     │                           ├─────────────────────────>│
     │                           │<─────────────────────────┤
     │                           │                          │
     │                           │  9. Create/update user   │
     │                           │     in database          │
     │                           │                          │
     │                           │ 10. Generate JWT token   │
     │                           │                          │
     │  11. Redirect with JWT    │                          │
     │      in URL fragment      │                          │
     │<──────────────────────────┤                          │
     │                           │                          │
     │  12. Frontend extracts    │                          │
     │      JWT and stores it    │                          │
     │                           │                          │
     │  13. Subsequent API calls │                          │
     │      with Authorization:  │                          │
     │      Bearer <JWT>         │                          │
     ├──────────────────────────>│                          │
     │                           │                          │
     │                           │ 14. Validate JWT         │
     │                           │     Extract user_id      │
     │                           │     Check ownership      │
     │                           │                          │
     │  15. Response             │                          │
     │<──────────────────────────┤                          │
```

### 2.2 Authorization Model

```
User
  |
  └── owns many Scans
         |
         └── contains ScanData, MessageMetadata, PhotosMediaItem, etc.
```

**Rule**: Users can only access resources they own.

### 2.3 JWT Token Structure

```json
{
  "sub": "user-uuid",           // User ID (UUID from users table)
  "email": "user@gmail.com",    // User email
  "display_name": "use****er@gmail.com",
  "iat": 1703174400,            // Issued at (Unix timestamp)
  "exp": 1703260800,            // Expiration (24 hours)
  "iss": "bhandaar-backend"     // Issuer
}
```

---

## 3. Phase 1: Basic JWT Authentication & Ownership

**Goal**: Prevent unauthorized access and enforce ownership
**Timeline**: 2-3 days

### 3.1 Tasks Overview

| # | Task | Files | Effort |
|---|------|-------|--------|
| 1 | Create users table and migration | `db/database.go` | 2 hours |
| 2 | Implement JWT utilities | `auth/jwt.go` (new) | 3 hours |
| 3 | Create authentication middleware | `web/middleware.go` (new) | 2 hours |
| 4 | Update OAuth callback to issue JWT | `web/oauth.go` | 2 hours |
| 5 | Add user_id to scans table | `db/database.go` | 1 hour |
| 6 | Implement ownership verification | `db/database.go`, `web/api.go` | 4 hours |
| 7 | Update all API handlers | `web/api.go` | 4 hours |
| 8 | Data migration for existing scans | `db/migration.go` (new) | 2 hours |
| 9 | Manual testing | All | 3 hours |

**Total Effort**: ~23 hours (2-3 days)

### 3.2 Success Criteria

- ✅ All API endpoints require valid JWT token
- ✅ Users can only access their own scans
- ✅ OAuth flow issues JWT token to frontend
- ✅ Existing scans associated with system user
- ✅ Application compiles and runs
- ✅ No security regressions

---

## 4. Phase 2: Comprehensive Security

**Goal**: Production-grade security hardening
**Timeline**: 3-4 days

### 4.1 Tasks Overview

| # | Task | Files | Effort |
|---|------|-------|--------|
| 1 | Token refresh mechanism | `auth/jwt.go`, `web/api.go` | 4 hours |
| 2 | Token revocation/blacklist | `db/database.go`, `auth/jwt.go` | 3 hours |
| 3 | Rate limiting per user | `web/middleware.go` | 3 hours |
| 4 | Audit logging | `db/database.go`, `web/middleware.go` | 4 hours |
| 5 | CSRF protection | `web/middleware.go` | 2 hours |
| 6 | Input validation enhancement | `web/api.go` | 3 hours |
| 7 | Security headers | `web/middleware.go` | 2 hours |
| 8 | API versioning | `web/web_server.go`, `web/api.go` | 3 hours |
| 9 | Comprehensive testing | All | 6 hours |

**Total Effort**: ~30 hours (3-4 days)

### 4.2 Success Criteria

- ✅ JWT tokens auto-refresh before expiration
- ✅ Revoked tokens cannot be used
- ✅ Rate limiting prevents abuse
- ✅ All sensitive operations logged
- ✅ CSRF protection on state-changing operations
- ✅ All inputs validated
- ✅ Security headers set correctly
- ✅ API versioned for future changes

---

## 5. Database Schema Changes

### 5.1 New Tables

#### users table

```sql
CREATE TABLE users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) NOT NULL UNIQUE,
    display_name VARCHAR(100),
    google_id VARCHAR(255) UNIQUE,           -- Google user ID (from OAuth)
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_login TIMESTAMP,
    is_active BOOLEAN DEFAULT true,
    is_system_user BOOLEAN DEFAULT false     -- For migration of existing scans
);

CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_google_id ON users(google_id);
```

#### token_blacklist table (Phase 2)

```sql
CREATE TABLE token_blacklist (
    id serial PRIMARY KEY,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_jti VARCHAR(255) NOT NULL UNIQUE,  -- JWT ID (jti claim)
    blacklisted_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL
);

CREATE INDEX idx_token_blacklist_jti ON token_blacklist(token_jti);
CREATE INDEX idx_token_blacklist_expires ON token_blacklist(expires_at);
```

#### audit_log table (Phase 2)

```sql
CREATE TABLE audit_log (
    id serial PRIMARY KEY,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action VARCHAR(50) NOT NULL,             -- CREATE_SCAN, DELETE_SCAN, etc.
    resource_type VARCHAR(50) NOT NULL,      -- scan, account, etc.
    resource_id VARCHAR(100),
    ip_address VARCHAR(50),
    user_agent TEXT,
    details JSONB,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_audit_log_user ON audit_log(user_id);
CREATE INDEX idx_audit_log_created ON audit_log(created_at);
CREATE INDEX idx_audit_log_action ON audit_log(action);
```

### 5.2 Modified Tables

#### scans table - Add user ownership

```sql
ALTER TABLE scans
    ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX idx_scans_user_id ON scans(user_id);
```

#### privatetokens table - Link to users

```sql
ALTER TABLE privatetokens
    ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;

CREATE INDEX idx_privatetokens_user_id ON privatetokens(user_id);
```

### 5.3 Migration Sequence

```sql
-- Step 1: Create users table
-- Step 2: Create system user
INSERT INTO users (id, email, display_name, is_system_user)
VALUES (
    '00000000-0000-0000-0000-000000000000',
    'system@bhandaar.local',
    'System User',
    true
);

-- Step 3: Add user_id column to scans (nullable initially)
ALTER TABLE scans ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE SET NULL;

-- Step 4: Assign all existing scans to system user
UPDATE scans SET user_id = '00000000-0000-0000-0000-000000000000' WHERE user_id IS NULL;

-- Step 5: Add user_id column to privatetokens
ALTER TABLE privatetokens ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;

-- Step 6: Create indices
CREATE INDEX idx_scans_user_id ON scans(user_id);
CREATE INDEX idx_privatetokens_user_id ON privatetokens(user_id);
```

---

## 6. Implementation Details

### 6.1 New Package: `auth/`

Create `be/auth/jwt.go`:

```go
package auth

import (
    "errors"
    "time"

    "github.com/golang-jwt/jwt/v5"
    "github.com/google/uuid"
)

// JWT configuration
const (
    TokenExpiration = 1 * time.Hour
    RefreshWindow   = 10 * time.Minute  // Refresh if less than 10 minutes remaining
)

// Claims represents JWT claims
type Claims struct {
    UserID      string `json:"sub"`
    Email       string `json:"email"`
    DisplayName string `json:"display_name"`
    jwt.RegisteredClaims
}

// JWTManager handles JWT operations
type JWTManager struct {
    secretKey []byte
    issuer    string
}

// NewJWTManager creates a new JWT manager
func NewJWTManager(secretKey string, issuer string) *JWTManager {
    return &JWTManager{
        secretKey: []byte(secretKey),
        issuer:    issuer,
    }
}

// GenerateToken generates a new JWT token
func (m *JWTManager) GenerateToken(userID uuid.UUID, email, displayName string) (string, error) {
    now := time.Now()
    claims := &Claims{
        UserID:      userID.String(),
        Email:       email,
        DisplayName: displayName,
        RegisteredClaims: jwt.RegisteredClaims{
            ExpiresAt: jwt.NewNumericDate(now.Add(TokenExpiration)),
            IssuedAt:  jwt.NewNumericDate(now),
            Issuer:    m.issuer,
            ID:        uuid.New().String(), // jti for blacklisting
        },
    }

    token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
    return token.SignedString(m.secretKey)
}

// ValidateToken validates and parses a JWT token
func (m *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
    token, err := jwt.ParseWithClaims(
        tokenString,
        &Claims{},
        func(token *jwt.Token) (interface{}, error) {
            // Verify signing method
            if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
                return nil, errors.New("unexpected signing method")
            }
            return m.secretKey, nil
        },
    )

    if err != nil {
        return nil, err
    }

    claims, ok := token.Claims.(*Claims)
    if !ok || !token.Valid {
        return nil, errors.New("invalid token")
    }

    return claims, nil
}

// ShouldRefresh checks if token should be refreshed
func (m *JWTManager) ShouldRefresh(claims *Claims) bool {
    now := time.Now()
    expiresAt := claims.ExpiresAt.Time
    return expiresAt.Sub(now) < RefreshWindow
}
```

Create `be/auth/context.go`:

```go
package auth

import (
    "context"
    "errors"

    "github.com/google/uuid"
)

type contextKey string

const (
    userIDKey   contextKey = "user_id"
    claimsKey   contextKey = "claims"
)

// SetUserID sets the user ID in context
func SetUserID(ctx context.Context, userID uuid.UUID) context.Context {
    return context.WithValue(ctx, userIDKey, userID)
}

// GetUserID retrieves the user ID from context
func GetUserID(ctx context.Context) (uuid.UUID, error) {
    userID, ok := ctx.Value(userIDKey).(uuid.UUID)
    if !ok {
        return uuid.Nil, errors.New("user ID not found in context")
    }
    return userID, nil
}

// SetClaims sets the JWT claims in context
func SetClaims(ctx context.Context, claims *Claims) context.Context {
    return context.WithValue(ctx, claimsKey, claims)
}

// GetClaims retrieves the JWT claims from context
func GetClaims(ctx context.Context) (*Claims, error) {
    claims, ok := ctx.Value(claimsKey).(*Claims)
    if !ok {
        return nil, errors.New("claims not found in context")
    }
    return claims, nil
}
```

### 6.2 Middleware: `web/middleware.go`

```go
package web

import (
    "log/slog"
    "net/http"
    "strings"

    "github.com/google/uuid"
    "github.com/jyothri/hdd/auth"
)

// AuthMiddleware validates JWT token and sets user context
func AuthMiddleware(jwtManager *auth.JWTManager) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            // Extract token from Authorization header
            authHeader := r.Header.Get("Authorization")
            if authHeader == "" {
                slog.Warn("Missing Authorization header",
                    "path", r.URL.Path,
                    "remote_addr", r.RemoteAddr)
                http.Error(w, "Unauthorized: Missing token", http.StatusUnauthorized)
                return
            }

            // Expect: "Bearer <token>"
            parts := strings.Split(authHeader, " ")
            if len(parts) != 2 || parts[0] != "Bearer" {
                slog.Warn("Invalid Authorization header format",
                    "path", r.URL.Path,
                    "remote_addr", r.RemoteAddr)
                http.Error(w, "Unauthorized: Invalid token format", http.StatusUnauthorized)
                return
            }

            tokenString := parts[1]

            // Validate token
            claims, err := jwtManager.ValidateToken(tokenString)
            if err != nil {
                slog.Warn("Invalid JWT token",
                    "error", err,
                    "path", r.URL.Path,
                    "remote_addr", r.RemoteAddr)
                http.Error(w, "Unauthorized: Invalid token", http.StatusUnauthorized)
                return
            }

            // Parse user ID
            userID, err := uuid.Parse(claims.UserID)
            if err != nil {
                slog.Error("Invalid user ID in token",
                    "user_id", claims.UserID,
                    "error", err)
                http.Error(w, "Unauthorized: Invalid token", http.StatusUnauthorized)
                return
            }

            // Set user context
            ctx := auth.SetUserID(r.Context(), userID)
            ctx = auth.SetClaims(ctx, claims)

            // Continue with authenticated context
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}

// OptionalAuthMiddleware is like AuthMiddleware but doesn't require auth
// Useful for endpoints that work differently with/without auth
func OptionalAuthMiddleware(jwtManager *auth.JWTManager) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            authHeader := r.Header.Get("Authorization")
            if authHeader == "" {
                // No auth provided, continue without user context
                next.ServeHTTP(w, r)
                return
            }

            parts := strings.Split(authHeader, " ")
            if len(parts) != 2 || parts[0] != "Bearer" {
                next.ServeHTTP(w, r)
                return
            }

            tokenString := parts[1]
            claims, err := jwtManager.ValidateToken(tokenString)
            if err != nil {
                // Invalid token, continue without user context
                next.ServeHTTP(w, r)
                return
            }

            userID, err := uuid.Parse(claims.UserID)
            if err != nil {
                next.ServeHTTP(w, r)
                return
            }

            ctx := auth.SetUserID(r.Context(), userID)
            ctx = auth.SetClaims(ctx, claims)
            next.ServeHTTP(w, r.WithContext(ctx))
        })
    }
}
```

### 6.3 Database Updates: `db/database.go`

Add to structs:

```go
import "github.com/google/uuid"

type User struct {
    ID           uuid.UUID    `db:"id"`
    Email        string       `db:"email"`
    DisplayName  string       `db:"display_name"`
    GoogleID     sql.NullString `db:"google_id"`
    CreatedAt    time.Time    `db:"created_at"`
    UpdatedAt    time.Time    `db:"updated_at"`
    LastLogin    sql.NullTime `db:"last_login"`
    IsActive     bool         `db:"is_active"`
    IsSystemUser bool         `db:"is_system_user"`
}

// Update Scan struct
type Scan struct {
    Id            int             `db:"id" json:"scan_id"`
    UserId        uuid.NullUUID   `db:"user_id" json:"user_id"` // NEW
    ScanType      string          `db:"scan_type"`
    CreatedOn     time.Time       `db:"created_on"`
    ScanStartTime time.Time       `db:"scan_start_time"`
    ScanEndTime   sql.NullTime    `db:"scan_end_time"`
    Metadata      string          `db:"metadata"`
    Duration      string          `db:"duration"`
    Status        string          `db:"status"`
    ErrorMsg      sql.NullString  `db:"error_msg"`
    CompletedAt   sql.NullTime    `db:"completed_at"`
}
```

Add user management functions:

```go
// CreateUser creates a new user or returns existing
func CreateUser(email, displayName, googleID string) (*User, error) {
    // Check if user exists
    existingUser, err := GetUserByEmail(email)
    if err == nil {
        // Update last login
        updateQuery := `UPDATE users SET last_login = CURRENT_TIMESTAMP WHERE id = $1`
        _, err = db.Exec(updateQuery, existingUser.ID)
        if err != nil {
            slog.Warn("Failed to update last login", "user_id", existingUser.ID, "error", err)
        }
        return existingUser, nil
    }

    // Create new user
    insertQuery := `
        INSERT INTO users (email, display_name, google_id, created_at, updated_at, last_login, is_active)
        VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, true)
        RETURNING id, email, display_name, google_id, created_at, updated_at, last_login, is_active, is_system_user
    `

    var user User
    err = db.QueryRow(insertQuery, email, displayName, googleID).Scan(
        &user.ID,
        &user.Email,
        &user.DisplayName,
        &user.GoogleID,
        &user.CreatedAt,
        &user.UpdatedAt,
        &user.LastLogin,
        &user.IsActive,
        &user.IsSystemUser,
    )

    if err != nil {
        return nil, fmt.Errorf("failed to create user: %w", err)
    }

    slog.Info("Created new user", "user_id", user.ID, "email", user.Email)
    return &user, nil
}

// GetUserByEmail retrieves user by email
func GetUserByEmail(email string) (*User, error) {
    query := `SELECT id, email, display_name, google_id, created_at, updated_at,
                     last_login, is_active, is_system_user
              FROM users WHERE email = $1`

    var user User
    err := db.QueryRow(query, email).Scan(
        &user.ID,
        &user.Email,
        &user.DisplayName,
        &user.GoogleID,
        &user.CreatedAt,
        &user.UpdatedAt,
        &user.LastLogin,
        &user.IsActive,
        &user.IsSystemUser,
    )

    if err != nil {
        return nil, fmt.Errorf("failed to get user by email: %w", err)
    }

    return &user, nil
}

// GetUserByID retrieves user by ID
func GetUserByID(userID uuid.UUID) (*User, error) {
    query := `SELECT id, email, display_name, google_id, created_at, updated_at,
                     last_login, is_active, is_system_user
              FROM users WHERE id = $1`

    var user User
    err := db.QueryRow(query, userID).Scan(
        &user.ID,
        &user.Email,
        &user.DisplayName,
        &user.GoogleID,
        &user.CreatedAt,
        &user.UpdatedAt,
        &user.LastLogin,
        &user.IsActive,
        &user.IsSystemUser,
    )

    if err != nil {
        return nil, fmt.Errorf("failed to get user by ID: %w", err)
    }

    return &user, nil
}

// UserOwnsScan checks if a user owns a specific scan
func UserOwnsScan(userID uuid.UUID, scanID int) (bool, error) {
    query := `SELECT COUNT(*) FROM scans WHERE id = $1 AND user_id = $2`
    var count int
    err := db.QueryRow(query, scanID, userID).Scan(&count)
    if err != nil {
        return false, fmt.Errorf("failed to check scan ownership: %w", err)
    }
    return count > 0, nil
}
```

Update `LogStartScan` to accept user ID:

```go
func LogStartScan(scanType string, userID uuid.UUID) (int, error) {
    insert_row := `insert into scans
                        (scan_type, user_id, created_on, scan_start_time)
                    values
                        ($1, $2, current_timestamp, current_timestamp) RETURNING id`
    lastInsertId := 0
    err := db.QueryRow(insert_row, scanType, userID).Scan(&lastInsertId)
    if err != nil {
        return 0, fmt.Errorf("failed to insert scan for type %s: %w", scanType, err)
    }
    return lastInsertId, nil
}
```

Update queries to filter by user_id:

```go
// GetScansFromDb - filter by user
func GetScansFromDb(userID uuid.UUID, pageNo int) ([]Scan, int, error) {
    limit := 10
    offset := limit * (pageNo - 1)
    count_rows := `select count(*) from scans WHERE user_id = $1`
    read_row := `select S.id, S.user_id, scan_type,
         created_on AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles' as created_on,
         scan_start_time AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles' as scan_start_time,
         scan_end_time, CONCAT(search_path, search_filter) as metadata,
         date_trunc('millisecond', COALESCE(scan_end_time,current_timestamp)-scan_start_time) as duration,
         status, error_msg, completed_at
       from scans S LEFT JOIN scanmetadata SM
         ON S.id = SM.scan_id
         WHERE S.user_id = $1
         order by S.id DESC limit $2 OFFSET $3
        `
    scans := []Scan{}
    var count int
    err := db.Select(&scans, read_row, userID, limit, offset)
    if err != nil {
        return nil, 0, fmt.Errorf("failed to get scans for user %s, page %d: %w", userID, pageNo, err)
    }
    err = db.Get(&count, count_rows, userID)
    if err != nil {
        return nil, 0, fmt.Errorf("failed to get scan count for user %s: %w", userID, err)
    }
    return scans, count, nil
}
```

### 6.4 OAuth Handler Updates: `web/oauth.go`

```go
import (
    "github.com/jyothri/hdd/auth"
    "github.com/google/uuid"
)

// Global JWT manager (initialized in main or web_server)
var jwtManager *auth.JWTManager

func GoogleAccountLinkingHandler(w http.ResponseWriter, r *http.Request) {
    const googleTokenUrl = "https://oauth2.googleapis.com/token"
    const grantType = "authorization_code"
    var redirectUri = r.FormValue("redirectUri")

    if redirectUri == "" {
        http.Error(w, "redirectUri not found in request", http.StatusBadRequest)
        return
    }

    var clientId = constants.OauthClientId
    var clientSecret = constants.OauthClientSecret

    // Parse form
    err := r.ParseForm()
    if err != nil {
        slog.Error("Failed to parse OAuth form", "error", err)
        http.Error(w, "Invalid request format", http.StatusBadRequest)
        return
    }
    code := r.FormValue("code")

    // Exchange code for tokens
    reqURL := fmt.Sprintf("%s?client_id=%s&client_secret=%s&code=%s&grant_type=%s&redirect_uri=%s",
        googleTokenUrl, clientId, clientSecret, code, grantType, redirectUri)
    req, err := http.NewRequest(http.MethodPost, reqURL, nil)
    if err != nil {
        slog.Error("Failed to create HTTP request", "error", err)
        http.Error(w, "Failed to create OAuth request", http.StatusBadRequest)
        return
    }
    req.Header.Set("accept", "application/json")

    httpClient := http.Client{}
    res, err := httpClient.Do(req)
    if err != nil {
        slog.Warn("Could not send HTTP request", "error", err)
        http.Error(w, "Failed to exchange authorization code", http.StatusInternalServerError)
        return
    }
    defer res.Body.Close()

    var t OAuthAccessResponse
    if err := json.NewDecoder(res.Body).Decode(&t); err != nil {
        slog.Warn("Could not parse JSON response", "error", err)
        http.Error(w, "Failed to parse OAuth response", http.StatusBadRequest)
        return
    }

    if t.AccessToken == "" || t.RefreshToken == "" {
        slog.Warn("Access or Refresh token could not be obtained")
        http.Error(w, "Access or Refresh token could not be obtained", http.StatusBadRequest)
        return
    }

    // Get user email and Google ID from Google
    email, googleID, err := getUserInfoFromGoogle(t.AccessToken)
    if err != nil {
        slog.Error("Failed to get user identity", "error", err)
        http.Error(w, "Failed to verify account", http.StatusInternalServerError)
        return
    }

    // Create or get user
    user, err := db.CreateUser(email, getDisplayName(email, ""), googleID)
    if err != nil {
        slog.Error("Failed to create user", "email", email, "error", err)
        http.Error(w, "Failed to create user account", http.StatusInternalServerError)
        return
    }

    // Generate client_key for backward compatibility with privatetokens
    client_key := generateRandomString(12)

    // Save OAuth tokens (now linked to user)
    err = db.SaveOAuthToken(t.AccessToken, t.RefreshToken, user.DisplayName,
                            client_key, user.ID, t.Scope, t.ExpiresIn, t.TokenType)
    if err != nil {
        slog.Error("Failed to save OAuth token",
            "client_key", client_key,
            "user_id", user.ID,
            "error", err)
        http.Error(w, "Failed to save account information", http.StatusInternalServerError)
        return
    }

    // Generate JWT token
    jwtToken, err := jwtManager.GenerateToken(user.ID, user.Email, user.DisplayName)
    if err != nil {
        slog.Error("Failed to generate JWT token",
            "user_id", user.ID,
            "error", err)
        http.Error(w, "Failed to generate authentication token", http.StatusInternalServerError)
        return
    }

    // Redirect with JWT in URL fragment (not accessible to server logs)
    u, err := url.Parse(redirectUri)
    if err != nil {
        slog.Error("Failed to parse redirect URI",
            "redirect_uri", redirectUri,
            "error", err)
        http.Error(w, "Invalid redirect URI", http.StatusBadRequest)
        return
    }

    // Use URL fragment to pass JWT (safer than query param)
    returnUrl := fmt.Sprintf("%s://%s/request#token=%s", u.Scheme, u.Host, jwtToken)

    w.Header().Set("Location", returnUrl)
    w.WriteHeader(http.StatusFound)
}

// getUserInfoFromGoogle retrieves user email and Google ID from Google OAuth userinfo endpoint
func getUserInfoFromGoogle(accessToken string) (email, googleID string, err error) {
    req, err := http.NewRequest("GET", "https://www.googleapis.com/oauth2/v2/userinfo", nil)
    if err != nil {
        return "", "", fmt.Errorf("failed to create userinfo request: %w", err)
    }

    req.Header.Set("Authorization", "Bearer "+accessToken)

    client := &http.Client{Timeout: 10 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return "", "", fmt.Errorf("failed to fetch userinfo: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return "", "", fmt.Errorf("userinfo request failed with status: %d", resp.StatusCode)
    }

    var userInfo struct {
        Email string `json:"email"`
        ID    string `json:"id"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
        return "", "", fmt.Errorf("failed to decode userinfo: %w", err)
    }

    return userInfo.Email, userInfo.ID, nil
}
```

Update `SaveOAuthToken` signature:

```go
func SaveOAuthToken(accessToken string, refreshToken string, displayName string,
                    clientKey string, userID uuid.UUID, scope string,
                    expiresIn int16, tokenType string) error {
    insert_row := `insert into privatetokens
            (access_token, refresh_token, display_name, client_key, user_id, scope, expires_in, token_type, created_on)
        values
            ($1, $2, $3, $4, $5, $6, $7, $8, current_timestamp) RETURNING id`
    _, err := db.Exec(insert_row, accessToken, refreshToken, displayName, clientKey, userID, scope, expiresIn, tokenType)
    if err != nil {
        return fmt.Errorf("failed to save OAuth token for user %s: %w", userID, err)
    }
    return nil
}
```

### 6.5 API Handler Updates: `web/api.go`

Update all handlers to:
1. Extract user ID from context
2. Pass user ID to database functions
3. Verify ownership before operations

Example - `DoScansHandler`:

```go
func DoScansHandler(w http.ResponseWriter, r *http.Request) {
    // Extract user ID from context (set by middleware)
    userID, err := auth.GetUserID(r.Context())
    if err != nil {
        slog.Error("User ID not found in context", "error", err)
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    decoder := json.NewDecoder(r.Body)
    var doScanRequest DoScanRequest
    err = decoder.Decode(&doScanRequest)
    if err != nil {
        slog.Error("Failed to decode scan request", "error", err)
        http.Error(w, "Invalid request body", http.StatusBadRequest)
        return
    }

    slog.Info("Received scan request",
        "user_id", userID,
        "scan_type", doScanRequest.ScanType)

    var scanId int
    switch doScanRequest.ScanType {
    case "Local":
        scanId, err = collect.LocalDrive(userID, doScanRequest.LocalScan)
    case "GDrive":
        scanId, err = collect.CloudDrive(userID, doScanRequest.GDriveScan)
    case "GMail":
        scanId, err = collect.Gmail(userID, doScanRequest.GMailScan)
    case "GPhotos":
        scanId, err = collect.Photos(userID, doScanRequest.GPhotosScan)
    default:
        slog.Error("Unknown scan type", "scan_type", doScanRequest.ScanType)
        http.Error(w, fmt.Sprintf("Unknown scan type: %s", doScanRequest.ScanType), http.StatusBadRequest)
        return
    }

    if err != nil {
        slog.Error("Failed to start scan",
            "user_id", userID,
            "scan_type", doScanRequest.ScanType,
            "error", err)
        http.Error(w, fmt.Sprintf("Failed to start scan: %v", err), http.StatusInternalServerError)
        return
    }

    body := DoScanResponse{ScanId: scanId}
    writeJSONResponse(w, body, http.StatusOK)
}
```

Example - `DeleteScanHandler` with ownership check:

```go
func DeleteScanHandler(w http.ResponseWriter, r *http.Request) {
    userID, err := auth.GetUserID(r.Context())
    if err != nil {
        slog.Error("User ID not found in context", "error", err)
        http.Error(w, "Unauthorized", http.StatusUnauthorized)
        return
    }

    vars := mux.Vars(r)
    scanId, ok := getIntFromMap(vars, "scan_id")
    if !ok {
        http.Error(w, "Invalid scan ID", http.StatusBadRequest)
        return
    }

    // Verify ownership
    owns, err := db.UserOwnsScan(userID, scanId)
    if err != nil {
        slog.Error("Failed to check scan ownership",
            "user_id", userID,
            "scan_id", scanId,
            "error", err)
        http.Error(w, "Failed to verify ownership", http.StatusInternalServerError)
        return
    }

    if !owns {
        slog.Warn("User attempted to delete scan they don't own",
            "user_id", userID,
            "scan_id", scanId)
        http.Error(w, "Forbidden: You don't own this scan", http.StatusForbidden)
        return
    }

    if err := db.DeleteScan(scanId); err != nil {
        slog.Error("Failed to delete scan",
            "user_id", userID,
            "scan_id", scanId,
            "error", err)
        http.Error(w, "Failed to delete scan", http.StatusInternalServerError)
        return
    }

    slog.Info("Scan deleted successfully",
        "user_id", userID,
        "scan_id", scanId)
    w.WriteHeader(http.StatusOK)
}
```

### 6.6 Router Updates: `web/web_server.go`

```go
import (
    "os"
    "github.com/jyothri/hdd/auth"
)

func Server() {
    slog.Info("Starting web server.")

    // Initialize JWT manager
    jwtSecret := os.Getenv("JWT_SECRET")
    if jwtSecret == "" {
        slog.Error("JWT_SECRET environment variable not set")
        os.Exit(1)
    }

    jwtManager = auth.NewJWTManager(jwtSecret, "bhandaar-backend")

    r := mux.NewRouter()

    // Public routes (no auth required)
    publicRouter := r.PathPrefix("/api/").Subrouter()
    publicRouter.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
        json.NewEncoder(w).Encode(map[string]bool{"ok": true})
    })

    // OAuth routes (no auth required for these)
    oauth(r)

    // Protected routes (require auth)
    protectedRouter := r.PathPrefix("/api/").Subrouter()
    protectedRouter.Use(AuthMiddleware(jwtManager))

    // Apply protected routes
    protectedRouter.HandleFunc("/scans", DoScansHandler).Methods("POST")
    protectedRouter.HandleFunc("/scans/requests/{account_key}", GetScanRequestsHandler).Methods("GET")
    protectedRouter.HandleFunc("/scans/accounts", GetAccountsHandler).Methods("GET")
    protectedRouter.HandleFunc("/scans/{scan_id}", DeleteScanHandler).Methods("DELETE")
    protectedRouter.HandleFunc("/scans", ListScansHandler).Methods("GET").Queries("page", "{page}")
    protectedRouter.HandleFunc("/scans", ListScansHandler).Methods("GET")
    protectedRouter.HandleFunc("/accounts", GetRequestAccountsHandler).Methods("GET")
    protectedRouter.HandleFunc("/scans/{scan_id}", ListScanDataHandler).Methods("GET").Queries("page", "{page}")
    protectedRouter.HandleFunc("/scans/{scan_id}", ListScanDataHandler).Methods("GET")
    protectedRouter.HandleFunc("/gmaildata/{scan_id}", ListMessageMetaDataHandler).Methods("GET").Queries("page", "{page}")
    protectedRouter.HandleFunc("/gmaildata/{scan_id}", ListMessageMetaDataHandler).Methods("GET")
    protectedRouter.HandleFunc("/photos/albums", ListAlbumsHandler).Methods("GET").Queries("refresh_token", "{refresh_token}")
    protectedRouter.HandleFunc("/photos/{scan_id}", ListPhotosHandler).Methods("GET").Queries("page", "{page}")
    protectedRouter.HandleFunc("/photos/{scan_id}", ListPhotosHandler).Methods("GET")

    // SSE endpoint (requires auth)
    sseRouter := r.PathPrefix("/events").Subrouter()
    sseRouter.Use(AuthMiddleware(jwtManager))
    sse(sseRouter)

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

### 6.7 Collect Functions Updates

Update all collect functions to accept `userID`:

```go
// collect/local.go
func LocalDrive(userID uuid.UUID, localScan LocalScan) (int, error) {
    scanId, err := db.LogStartScan("local", userID)
    // ... rest of implementation
}

// collect/drive.go
func CloudDrive(userID uuid.UUID, driveScan GDriveScan) (int, error) {
    scanId, err := db.LogStartScan("drive", userID)
    // ... rest of implementation
}

// collect/gmail.go
func Gmail(userID uuid.UUID, gMailScan GMailScan) (int, error) {
    scanId, err := db.LogStartScan("gmail", userID)
    // ... rest of implementation
}

// collect/photos.go
func Photos(userID uuid.UUID, photosScan GPhotosScan) (int, error) {
    scanId, err := db.LogStartScan("photos", userID)
    // ... rest of implementation
}
```

---

## 7. Migration Strategy

### 7.1 Migration Script: `db/migration.go`

```go
package db

import (
    "database/sql"
    "fmt"
    "log/slog"

    "github.com/google/uuid"
)

// RunAuthMigration runs the authentication migration
func RunAuthMigration() error {
    slog.Info("Starting authentication migration")

    // Step 1: Create users table
    if err := createUsersTable(); err != nil {
        return fmt.Errorf("failed to create users table: %w", err)
    }

    // Step 2: Create system user
    systemUserID := uuid.MustParse("00000000-0000-0000-0000-000000000000")
    if err := createSystemUser(systemUserID); err != nil {
        return fmt.Errorf("failed to create system user: %w", err)
    }

    // Step 3: Add user_id to scans table
    if err := addUserIdToScans(); err != nil {
        return fmt.Errorf("failed to add user_id to scans: %w", err)
    }

    // Step 4: Assign existing scans to system user
    if err := assignScansToSystemUser(systemUserID); err != nil {
        return fmt.Errorf("failed to assign scans to system user: %w", err)
    }

    // Step 5: Add user_id to privatetokens table
    if err := addUserIdToPrivateTokens(); err != nil {
        return fmt.Errorf("failed to add user_id to privatetokens: %w", err)
    }

    // Step 6: Create indices
    if err := createIndices(); err != nil {
        return fmt.Errorf("failed to create indices: %w", err)
    }

    slog.Info("Authentication migration completed successfully")
    return nil
}

func createUsersTable() error {
    query := `
    CREATE TABLE IF NOT EXISTS users (
        id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
        email VARCHAR(255) NOT NULL UNIQUE,
        display_name VARCHAR(100),
        google_id VARCHAR(255) UNIQUE,
        created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
        updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
        last_login TIMESTAMP,
        is_active BOOLEAN DEFAULT true,
        is_system_user BOOLEAN DEFAULT false
    )`

    _, err := db.Exec(query)
    if err != nil {
        return err
    }

    slog.Info("Created users table")
    return nil
}

func createSystemUser(systemUserID uuid.UUID) error {
    // Check if system user already exists
    var count int
    checkQuery := `SELECT COUNT(*) FROM users WHERE id = $1`
    err := db.QueryRow(checkQuery, systemUserID).Scan(&count)
    if err != nil {
        return err
    }

    if count > 0 {
        slog.Info("System user already exists")
        return nil
    }

    insertQuery := `
    INSERT INTO users (id, email, display_name, is_system_user, created_at, updated_at)
    VALUES ($1, $2, $3, $4, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
    `

    _, err = db.Exec(insertQuery,
        systemUserID,
        "system@bhandaar.local",
        "System User",
        true,
    )

    if err != nil {
        return err
    }

    slog.Info("Created system user", "user_id", systemUserID)
    return nil
}

func addUserIdToScans() error {
    // Check if column already exists
    checkQuery := `
    SELECT column_name
    FROM information_schema.columns
    WHERE table_name='scans' AND column_name='user_id'
    `

    var columnName string
    err := db.QueryRow(checkQuery).Scan(&columnName)

    // Column doesn't exist (sql.ErrNoRows), so add it
    if err == sql.ErrNoRows {
        alterQuery := `ALTER TABLE scans ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE SET NULL`
        _, err = db.Exec(alterQuery)
        if err != nil {
            return err
        }
        slog.Info("Added user_id column to scans table")
    } else if err != nil {
        return err
    } else {
        slog.Info("user_id column already exists in scans table")
    }

    return nil
}

func assignScansToSystemUser(systemUserID uuid.UUID) error {
    updateQuery := `UPDATE scans SET user_id = $1 WHERE user_id IS NULL`
    result, err := db.Exec(updateQuery, systemUserID)
    if err != nil {
        return err
    }

    rowsAffected, _ := result.RowsAffected()
    slog.Info("Assigned existing scans to system user",
        "rows_affected", rowsAffected,
        "system_user_id", systemUserID)

    return nil
}

func addUserIdToPrivateTokens() error {
    // Check if column already exists
    checkQuery := `
    SELECT column_name
    FROM information_schema.columns
    WHERE table_name='privatetokens' AND column_name='user_id'
    `

    var columnName string
    err := db.QueryRow(checkQuery).Scan(&columnName)

    // Column doesn't exist, so add it
    if err == sql.ErrNoRows {
        alterQuery := `ALTER TABLE privatetokens ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE`
        _, err = db.Exec(alterQuery)
        if err != nil {
            return err
        }
        slog.Info("Added user_id column to privatetokens table")
    } else if err != nil {
        return err
    } else {
        slog.Info("user_id column already exists in privatetokens table")
    }

    return nil
}

func createIndices() error {
    indices := []struct {
        name  string
        query string
    }{
        {"idx_users_email", "CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)"},
        {"idx_users_google_id", "CREATE INDEX IF NOT EXISTS idx_users_google_id ON users(google_id)"},
        {"idx_scans_user_id", "CREATE INDEX IF NOT EXISTS idx_scans_user_id ON scans(user_id)"},
        {"idx_privatetokens_user_id", "CREATE INDEX IF NOT EXISTS idx_privatetokens_user_id ON privatetokens(user_id)"},
    }

    for _, idx := range indices {
        _, err := db.Exec(idx.query)
        if err != nil {
            return fmt.Errorf("failed to create index %s: %w", idx.name, err)
        }
        slog.Info("Created index", "index", idx.name)
    }

    return nil
}
```

Update `migrateDB()` in `database.go`:

```go
func migrateDB() error {
    var count int
    has_table_query := `select count(*)
        from information_schema.tables
        where table_name = $1`
    err := db.Get(&count, has_table_query, "version")
    if err != nil {
        return fmt.Errorf("failed to check for version table: %w", err)
    }
    if count == 0 {
        if err := migrateDBv0(); err != nil {
            return err
        }
    }

    // Add migration for status column if needed
    if err := migrateAddStatusColumn(); err != nil {
        return err
    }

    // Run authentication migration
    if err := RunAuthMigration(); err != nil {
        return fmt.Errorf("authentication migration failed: %w", err)
    }

    return nil
}
```

### 7.2 Rollback Plan

If migration fails, rollback script:

```sql
-- Rollback authentication migration

-- Remove indices
DROP INDEX IF EXISTS idx_privatetokens_user_id;
DROP INDEX IF EXISTS idx_scans_user_id;
DROP INDEX IF EXISTS idx_users_google_id;
DROP INDEX IF EXISTS idx_users_email;

-- Remove user_id columns
ALTER TABLE privatetokens DROP COLUMN IF EXISTS user_id;
ALTER TABLE scans DROP COLUMN IF EXISTS user_id;

-- Drop users table (cascades to foreign keys)
DROP TABLE IF EXISTS users;
```

---

## 8. Testing Strategy

### 8.1 Unit Tests

Create `be/auth/jwt_test.go`:

```go
package auth

import (
    "testing"
    "time"

    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
)

func TestGenerateToken(t *testing.T) {
    jwtManager := NewJWTManager("test-secret-key", "test-issuer")
    userID := uuid.New()
    email := "test@example.com"
    displayName := "Test User"

    token, err := jwtManager.GenerateToken(userID, email, displayName)

    assert.NoError(t, err)
    assert.NotEmpty(t, token)
}

func TestValidateToken(t *testing.T) {
    jwtManager := NewJWTManager("test-secret-key", "test-issuer")
    userID := uuid.New()
    email := "test@example.com"
    displayName := "Test User"

    token, err := jwtManager.GenerateToken(userID, email, displayName)
    assert.NoError(t, err)

    claims, err := jwtManager.ValidateToken(token)

    assert.NoError(t, err)
    assert.Equal(t, userID.String(), claims.UserID)
    assert.Equal(t, email, claims.Email)
    assert.Equal(t, displayName, claims.DisplayName)
}

func TestValidateToken_Invalid(t *testing.T) {
    jwtManager := NewJWTManager("test-secret-key", "test-issuer")

    _, err := jwtManager.ValidateToken("invalid-token")

    assert.Error(t, err)
}

func TestValidateToken_WrongSecret(t *testing.T) {
    jwtManager1 := NewJWTManager("secret1", "test-issuer")
    jwtManager2 := NewJWTManager("secret2", "test-issuer")

    userID := uuid.New()
    token, _ := jwtManager1.GenerateToken(userID, "test@example.com", "Test")

    _, err := jwtManager2.ValidateToken(token)

    assert.Error(t, err)
}
```

### 8.2 Integration Tests

Create `be/web/api_test.go`:

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
    "github.com/stretchr/testify/assert"
)

func TestDoScansHandler_Unauthorized(t *testing.T) {
    req := httptest.NewRequest("POST", "/api/scans", nil)
    w := httptest.NewRecorder()

    DoScansHandler(w, req)

    assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDoScansHandler_WithAuth(t *testing.T) {
    // Setup
    jwtManager := auth.NewJWTManager("test-secret", "test-issuer")
    userID := uuid.New()
    token, _ := jwtManager.GenerateToken(userID, "test@example.com", "Test User")

    scanRequest := DoScanRequest{
        ScanType: "Local",
        LocalScan: collect.LocalScan{
            Source: "/test/path",
        },
    }
    body, _ := json.Marshal(scanRequest)

    req := httptest.NewRequest("POST", "/api/scans", bytes.NewBuffer(body))
    req.Header.Set("Authorization", "Bearer "+token)
    req.Header.Set("Content-Type", "application/json")

    // Add auth context
    ctx := auth.SetUserID(req.Context(), userID)
    req = req.WithContext(ctx)

    w := httptest.NewRecorder()

    DoScansHandler(w, req)

    assert.Equal(t, http.StatusOK, w.Code)
}

func TestDeleteScanHandler_NotOwner(t *testing.T) {
    // Setup: User 1 creates scan, User 2 tries to delete
    jwtManager := auth.NewJWTManager("test-secret", "test-issuer")
    user1ID := uuid.New()
    user2ID := uuid.New()

    // Create scan as user1 (assume scan ID = 1)
    // ...

    // Try to delete as user2
    token, _ := jwtManager.GenerateToken(user2ID, "user2@example.com", "User 2")

    req := httptest.NewRequest("DELETE", "/api/scans/1", nil)
    req.Header.Set("Authorization", "Bearer "+token)
    req = mux.SetURLVars(req, map[string]string{"scan_id": "1"})

    ctx := auth.SetUserID(req.Context(), user2ID)
    req = req.WithContext(ctx)

    w := httptest.NewRecorder()

    DeleteScanHandler(w, req)

    assert.Equal(t, http.StatusForbidden, w.Code)
}
```

### 8.3 Manual Testing Checklist

#### Phase 1 Testing

- [ ] **OAuth Flow**
  - [ ] Start OAuth flow from frontend
  - [ ] Complete Google authorization
  - [ ] Verify JWT token received in redirect
  - [ ] Verify user created in database
  - [ ] Verify OAuth tokens saved with user_id

- [ ] **Authentication**
  - [ ] API call without token returns 401
  - [ ] API call with invalid token returns 401
  - [ ] API call with expired token returns 401
  - [ ] API call with valid token succeeds

- [ ] **Authorization - Create Scan**
  - [ ] Authenticated user can create scan
  - [ ] Scan is linked to correct user_id
  - [ ] Unauthenticated request fails

- [ ] **Authorization - List Scans**
  - [ ] User only sees their own scans
  - [ ] User cannot see other users' scans
  - [ ] Empty list for new user

- [ ] **Authorization - View Scan**
  - [ ] User can view their own scan details
  - [ ] User cannot view other users' scans (403)

- [ ] **Authorization - Delete Scan**
  - [ ] User can delete their own scan
  - [ ] User cannot delete other users' scans (403)
  - [ ] Deleted scan removes all child data

- [ ] **Migration**
  - [ ] System user created with correct UUID
  - [ ] Existing scans assigned to system user
  - [ ] New scans assigned to correct user

#### Phase 2 Testing

- [ ] **Token Refresh**
  - [ ] Token refreshes automatically before expiration
  - [ ] Refreshed token has updated expiration
  - [ ] Old token still valid until expiration

- [ ] **Token Revocation**
  - [ ] Logout revokes token
  - [ ] Revoked token returns 401
  - [ ] New token can be issued after revocation

- [ ] **Rate Limiting**
  - [ ] Excessive requests return 429
  - [ ] Rate limit resets after time window
  - [ ] Different users have separate limits

- [ ] **Audit Logging**
  - [ ] Scan creation logged
  - [ ] Scan deletion logged
  - [ ] Failed auth attempts logged
  - [ ] Logs contain user_id, action, timestamp

- [ ] **CSRF Protection**
  - [ ] POST without CSRF token fails
  - [ ] Valid CSRF token succeeds
  - [ ] GET requests don't require CSRF token

---

## 9. Rollout Plan

### 9.1 Pre-Deployment Checklist

- [ ] All unit tests passing
- [ ] All integration tests passing
- [ ] Manual testing completed
- [ ] Database backup created
- [ ] Migration script tested on staging
- [ ] Rollback procedure documented and tested
- [ ] JWT_SECRET environment variable set
- [ ] Frontend updated to handle JWT tokens
- [ ] Documentation updated

### 9.2 Deployment Steps

1. **Backup Database**
   ```bash
   pg_dump -h localhost -U hddb hdd_db > backup_pre_auth_$(date +%Y%m%d_%H%M%S).sql
   ```

2. **Deploy Backend**
   ```bash
   # Set JWT_SECRET environment variable
   export JWT_SECRET="your-secure-random-secret-key-here"

   # Build
   cd be
   go build -o hdd

   # Run (migration runs automatically on startup)
   ./hdd
   ```

3. **Verify Migration**
   ```bash
   # Check users table created
   psql -U hddb -d hdd_db -c "SELECT * FROM users;"

   # Check system user exists
   psql -U hddb -d hdd_db -c "SELECT * FROM users WHERE is_system_user = true;"

   # Check existing scans assigned to system user
   psql -U hddb -d hdd_db -c "SELECT COUNT(*) FROM scans WHERE user_id = '00000000-0000-0000-0000-000000000000';"
   ```

4. **Deploy Frontend**
   ```bash
   cd ui
   # Update to extract JWT from OAuth redirect
   # Update to send Authorization header with all API calls
   npm run build
   ```

5. **Smoke Tests**
   - Test OAuth flow
   - Test creating a scan
   - Test listing scans
   - Test deleting a scan

### 9.3 Rollback Procedure

If issues are encountered:

1. **Stop Application**
   ```bash
   killall hdd
   ```

2. **Restore Database**
   ```bash
   psql -U hddb -d hdd_db < backup_pre_auth_YYYYMMDD_HHMMSS.sql
   ```

3. **Deploy Previous Version**
   ```bash
   git checkout <previous-commit>
   cd be
   go build -o hdd
   ./hdd
   ```

### 9.4 Post-Deployment Monitoring

Monitor for:
- Increased 401 errors (auth issues)
- Increased 403 errors (ownership issues)
- Failed logins
- Database query performance on new indices
- JWT token generation/validation performance

---

## 10. Security Considerations

### 10.1 JWT Secret Management

**DO NOT hardcode JWT secret!**

```go
// ❌ BAD
jwtSecret := "my-secret-key"

// ✅ GOOD
jwtSecret := os.Getenv("JWT_SECRET")
if jwtSecret == "" {
    log.Fatal("JWT_SECRET environment variable must be set")
}
```

**Recommendations:**
- Use strong random secret (min 256 bits)
- Different secret for dev/staging/prod
- Rotate secret periodically
- Store in secure secrets manager (AWS Secrets Manager, HashiCorp Vault, etc.)

Generate strong secret:
```bash
openssl rand -base64 64
```

### 10.2 Token Expiration

**Setting: 1 hour**

This short expiration provides good security while the auto-refresh mechanism (Phase 2) prevents user disruption. Tokens refresh automatically when less than 10 minutes remaining.

**Benefits:**
- ✅ Limits window for stolen token abuse
- ✅ Forces periodic re-validation
- ✅ Auto-refresh prevents user interruption

### 10.3 HTTPS Requirement

**JWT tokens MUST be transmitted over HTTPS in production!**

Add middleware to enforce HTTPS:

```go
func httpsRedirectMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        if r.Header.Get("X-Forwarded-Proto") != "https" && !strings.Contains(r.Host, "localhost") {
            http.Redirect(w, r, "https://"+r.Host+r.RequestURI, http.StatusMovedPermanently)
            return
        }
        next.ServeHTTP(w, r)
    })
}
```

### 10.4 OAuth Token Storage Security

Current: Plaintext in database (Issue #4)

**This plan does NOT fix Issue #4!**

After implementing authentication, also implement **Issue #4: Plaintext Storage of OAuth Tokens** by:
- Encrypting refresh tokens at rest
- Using envelope encryption with key rotation
- Storing encryption key in secrets manager

### 10.5 Rate Limiting (Phase 2)

Prevent brute force and abuse:

```go
import "golang.org/x/time/rate"

type RateLimiter struct {
    visitors map[string]*rate.Limiter
    mu       sync.RWMutex
    rate     rate.Limit
    burst    int
}

// Per-user rate limiting
func (rl *RateLimiter) GetLimiter(userID string) *rate.Limiter {
    rl.mu.Lock()
    defer rl.mu.Unlock()

    limiter, exists := rl.visitors[userID]
    if !exists {
        limiter = rate.NewLimiter(rl.rate, rl.burst)
        rl.visitors[userID] = limiter
    }

    return limiter
}
```

Apply rate limits:
- Login: 5 attempts per 15 minutes
- API calls: 100 requests per minute per user
- Scan creation: 10 scans per hour per user

### 10.6 Security Headers (Phase 2)

```go
func securityHeadersMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-XSS-Protection", "1; mode=block")
        w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
        w.Header().Set("Content-Security-Policy", "default-src 'self'")

        next.ServeHTTP(w, r)
    })
}
```

### 10.7 Input Validation

All user inputs MUST be validated:

```go
func validateScanRequest(req *DoScanRequest) error {
    if req.ScanType == "" {
        return errors.New("scan_type is required")
    }

    validTypes := map[string]bool{"Local": true, "GDrive": true, "GMail": true, "GPhotos": true}
    if !validTypes[req.ScanType] {
        return fmt.Errorf("invalid scan_type: %s", req.ScanType)
    }

    switch req.ScanType {
    case "Local":
        if req.LocalScan.Source == "" {
            return errors.New("source is required for local scan")
        }
        if len(req.LocalScan.Source) > 2000 {
            return errors.New("source path too long")
        }
    // ... validate other scan types
    }

    return nil
}
```

---

## Appendix A: Environment Variables

Required environment variables:

```bash
# JWT Configuration
JWT_SECRET="your-256-bit-secret-here"              # Required: JWT signing secret

# Database Configuration (existing)
DB_HOST="localhost"
DB_PORT="5432"
DB_USER="hddb"
DB_PASSWORD="hddb"
DB_NAME="hdd_db"

# OAuth Configuration (existing)
OAUTH_CLIENT_ID="your-google-client-id"
OAUTH_CLIENT_SECRET="your-google-client-secret"
FRONTEND_URL="http://localhost:5173"
```

---

## Appendix B: Frontend Changes Required

The frontend needs to be updated to handle JWT authentication:

### B.1 Extract JWT from OAuth Redirect

```typescript
// src/routes/oauth/glink.tsx
import { useEffect } from 'react';
import { useNavigate } from '@tanstack/react-router';

function OAuthCallback() {
  const navigate = useNavigate();

  useEffect(() => {
    // Extract JWT from URL fragment
    const hash = window.location.hash.substring(1);
    const params = new URLSearchParams(hash);
    const token = params.get('token');

    if (token) {
      // Store token in localStorage
      localStorage.setItem('jwt_token', token);

      // Redirect to main app
      navigate({ to: '/request' });
    } else {
      // Handle error
      console.error('No token received from OAuth');
      navigate({ to: '/' });
    }
  }, [navigate]);

  return <div>Completing authentication...</div>;
}
```

### B.2 Include JWT in API Requests

```typescript
// src/api/index.ts
const API_BASE_URL = 'https://sm.jkurapati.com';

async function fetchWithAuth(url: string, options: RequestInit = {}) {
  const token = localStorage.getItem('jwt_token');

  const headers = {
    'Content-Type': 'application/json',
    ...(token && { 'Authorization': `Bearer ${token}` }),
    ...options.headers,
  };

  const response = await fetch(`${API_BASE_URL}${url}`, {
    ...options,
    headers,
  });

  // Handle 401 - token expired or invalid
  if (response.status === 401) {
    localStorage.removeItem('jwt_token');
    window.location.href = '/';
    throw new Error('Unauthorized');
  }

  return response;
}

export async function createScan(scanRequest: ScanRequest) {
  const response = await fetchWithAuth('/api/scans', {
    method: 'POST',
    body: JSON.stringify(scanRequest),
  });

  return response.json();
}

export async function getScans(page: number = 1) {
  const response = await fetchWithAuth(`/api/scans?page=${page}`);
  return response.json();
}

// ... update all other API functions
```

### B.3 Logout Function

```typescript
export function logout() {
  localStorage.removeItem('jwt_token');
  window.location.href = '/';
}
```

---

## Appendix C: Useful Commands

### Generate JWT Secret
```bash
openssl rand -base64 64
```

### Test JWT Token Locally
```bash
# Generate token (using jwt.io or custom script)
# Test API with token
curl -H "Authorization: Bearer <token>" http://localhost:8090/api/scans
```

### Check Database State
```bash
# Count users
psql -U hddb -d hdd_db -c "SELECT COUNT(*) FROM users;"

# View scans with ownership
psql -U hddb -d hdd_db -c "SELECT s.id, s.scan_type, u.email FROM scans s JOIN users u ON s.user_id = u.id;"

# Find orphaned scans
psql -U hddb -d hdd_db -c "SELECT COUNT(*) FROM scans WHERE user_id IS NULL;"
```

### Monitor Auth Errors
```bash
# Watch logs for auth failures
tail -f /var/log/bhandaar/app.log | grep -i "unauthorized\|forbidden"
```

---

**END OF DOCUMENT**
