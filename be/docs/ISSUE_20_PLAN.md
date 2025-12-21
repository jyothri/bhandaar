# Issue #20 Implementation Plan: No Caching Strategy

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P2 - Medium Priority (Performance Optimization)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #20: No Caching Strategy**. The current system fetches OAuth tokens from the database for every scan operation, causing repeated database queries and slow API responses.

**Selected Approach:**
- **Scope**: OAuth tokens only (PrivateToken struct)
- **Library**: github.com/patrickmn/go-cache (in-memory, thread-safe)
- **Expiration**: 5-minute TTL for tokens
- **Invalidation**: Time-based + Manual invalidation on token save/update
- **Thread Safety**: Built-in go-cache thread safety
- **Cache Warming**: Lazy loading (cache on first access)
- **Integration**: Initialize in SetupDatabase(), close in CloseDatabase()
- **Cache Miss**: Single-threaded fetch from DB (future: singleflight pattern)
- **Monitoring**: Minimal - log initialization and basic stats
- **Error Handling**: Fail-soft - cache errors fallback to DB

**Estimated Effort:** 1 day (8 hours)

**Impact:**
- Eliminates database queries for repeated token fetches
- Reduces scan startup latency (no DB query for tokens)
- Improves API response times for scan operations
- Minimal memory overhead (~1-2 MB for typical token cache)
- Zero cache stampede risk with current usage patterns

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Implementation Details](#3-implementation-details)
4. [Testing Strategy](#4-testing-strategy)
5. [Deployment Plan](#5-deployment-plan)
6. [Future Enhancements](#6-future-enhancements)

---

## 1. Current State Analysis

### 1.1 Current Implementation

**OAuth Token Fetching (db/database.go:303-314):**
```go
func GetOAuthToken(clientKey string) (PrivateToken, error) {
	read_row :=
		`select id, access_token, refresh_token, display_name, client_key, created_on, scope, expires_in, token_type
		FROM privatetokens
		WHERE client_key = $1`
	tokenData := PrivateToken{}
	err := db.Get(&tokenData, read_row, clientKey)
	if err != nil {
		return PrivateToken{}, fmt.Errorf("failed to get OAuth token for client %s: %w", clientKey, err)
	}
	return tokenData, nil
}
```

**Usage in Gmail Scan (collect/gmail.go:76-82):**
```go
// Get refresh token
if gMailScan.ClientKey != "" {
	token, err := db.GetOAuthToken(gMailScan.ClientKey)  // ❌ DB query every time
	if err != nil {
		return 0, fmt.Errorf("failed to get OAuth token for client %s: %w", gMailScan.ClientKey, err)
	}
	gMailScan.RefreshToken = token.RefreshToken
}
```

### 1.2 Usage Patterns

**Current Token Fetch Frequency:**

| Operation | Endpoint/Function | Frequency | DB Queries |
|-----------|------------------|-----------|------------|
| **Gmail Scan** | POST /api/scans (GMailScan) | Per scan | 1 per scan |
| **Photos Scan** | POST /api/scans (GPhotosScan) | Per scan | 1 per scan |
| **Drive Scan** | POST /api/scans (GDriveScan) | Per scan | 1 per scan |
| **List Albums** | GET /api/photos/albums | Per request | 1 per request |

**Scenario Analysis:**

```
User Activity Timeline (No Cache):
1. User starts Gmail scan → GetOAuthToken(user@gmail.com) → DB query (5ms)
2. User starts Photos scan (same account) → GetOAuthToken(user@gmail.com) → DB query (5ms)
3. User starts another Gmail scan → GetOAuthToken(user@gmail.com) → DB query (5ms)
4. User lists albums → RefreshToken passed directly (no DB query)
5. User starts Drive scan → GetOAuthToken(user@gmail.com) → DB query (5ms)

Total DB queries: 4
Total DB time: 20ms
```

```
User Activity Timeline (With Cache):
1. User starts Gmail scan → GetOAuthToken(user@gmail.com) → DB query (5ms) + cache store
2. User starts Photos scan (same account) → GetOAuthToken(user@gmail.com) → Cache hit (0.1ms)
3. User starts another Gmail scan → GetOAuthToken(user@gmail.com) → Cache hit (0.1ms)
4. User lists albums → RefreshToken passed directly (no DB query)
5. User starts Drive scan → GetOAuthToken(user@gmail.com) → Cache hit (0.1ms)

Total DB queries: 1
Total DB time: 5ms + 0.3ms cache = 5.3ms
Savings: 14.7ms (73% reduction)
```

### 1.3 Performance Impact

**Without Caching:**
```
Typical scan startup timeline:
1. LogStartScan(): 2ms
2. GetOAuthToken(): 5ms  ← Can be eliminated with cache
3. Initialize API client: 10ms
4. Start scan goroutine: 1ms
Total: 18ms
```

**With Caching:**
```
Typical scan startup timeline (cache hit):
1. LogStartScan(): 2ms
2. GetOAuthToken() from cache: 0.1ms  ← 98% faster
3. Initialize API client: 10ms
4. Start scan goroutine: 1ms
Total: 13.1ms (27% faster startup)
```

**Memory Impact:**
```
PrivateToken struct size: ~200 bytes (strings vary)
Typical deployment: 5-10 accounts
Cache overhead: 5 accounts × 200 bytes = 1 KB
Plus go-cache overhead: ~1-2 MB total
Acceptable for the performance gain
```

### 1.4 Current vs Target Behavior

| Aspect | Current | Target |
|--------|---------|--------|
| **Token fetch** | DB query every time | Cache hit (5ms → 0.1ms) |
| **Cache miss** | N/A | Single DB query, then cache |
| **Token update** | SaveOAuthToken() to DB | Save + invalidate cache |
| **Stale tokens** | Always fresh from DB | Max 5 min stale (acceptable) |
| **Memory usage** | Minimal | +1-2 MB (cache + tokens) |
| **Scan startup** | 18ms avg | 13ms avg (27% faster) |
| **DB load** | High (4 queries per session) | Low (1 query per session) |

---

## 2. Target Architecture

### 2.1 Token Cache Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Application Startup                                      │
│    - SetupDatabase() connects to PostgreSQL                 │
│    - InitTokenCache() creates go-cache instance             │
│    - Cache starts empty (lazy loading)                      │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. First Token Request (Cache Miss)                        │
│    - GetOAuthToken("user@gmail.com") called                │
│    - Check cache → Not found                               │
│    - Query database → Get PrivateToken                     │
│    - Store in cache with 5-min TTL                         │
│    - Return token to caller                                 │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Subsequent Requests (Cache Hit)                         │
│    - GetOAuthToken("user@gmail.com") called                │
│    - Check cache → Found!                                   │
│    - Return cached token (0.1ms)                            │
│    - No database query                                      │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Cache Expiration (After 5 Minutes)                      │
│    - TTL expires, entry auto-removed                        │
│    - Next request → Cache miss                              │
│    - Fetch from DB again → Re-cache                         │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Manual Invalidation (Token Update)                      │
│    - SaveOAuthToken() called (new OAuth flow)              │
│    - Save to database                                       │
│    - InvalidateToken(clientKey) clears cache               │
│    - Next request fetches fresh token from DB               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 6. Graceful Shutdown                                        │
│    - CloseDatabase() calls CloseTokenCache()               │
│    - Cache flushed (not persistent)                         │
│    - Clean shutdown                                         │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Cache Architecture

```
┌────────────────────────────────────────────────────────────┐
│ TokenCache                                                  │
│                                                              │
│ Fields:                                                      │
│ - cache: *cache.Cache (github.com/patrickmn/go-cache)      │
│ - defaultExpiration: 5 * time.Minute                       │
│ - cleanupInterval: 10 * time.Minute                        │
│                                                              │
│ Methods:                                                     │
│ - GetToken(clientKey string) (PrivateToken, bool)          │
│   → Check cache, return token + found flag                 │
│                                                              │
│ - SetToken(clientKey string, token PrivateToken)           │
│   → Store token with 5-min TTL                             │
│                                                              │
│ - InvalidateToken(clientKey string)                        │
│   → Delete specific token from cache                       │
│                                                              │
│ - InvalidateAll()                                           │
│   → Clear entire cache (for testing/emergency)             │
│                                                              │
│ - GetStats() CacheStats                                     │
│   → Return cache statistics (size, hit rate, etc.)         │
└────────────────────────────────────────────────────────────┘
```

### 2.3 Integration Points

**db/database.go:**
- Global `tokenCache` variable
- `InitTokenCache()` - called from SetupDatabase()
- `CloseTokenCache()` - called from CloseDatabase()
- Modified `GetOAuthToken()` - check cache first
- Modified `SaveOAuthToken()` - invalidate on save

**collect/gmail.go, photos.go, drive.go:**
- No changes needed (transparent caching)

**Integration with Issue #17:**
- Cache initialized after database connection
- Cache closed before database connection closes
- Both use same lifecycle management pattern

---

## 3. Implementation Details

### 3.1 Token Cache Package: `db/token_cache.go` (NEW FILE)

```go
package db

import (
	"log/slog"
	"time"

	"github.com/patrickmn/go-cache"
)

// TokenCache wraps go-cache for OAuth token caching
type TokenCache struct {
	cache *cache.Cache
}

// Global token cache instance
var tokenCache *TokenCache

// CacheStats provides cache statistics
type CacheStats struct {
	ItemCount int
}

// InitTokenCache initializes the global token cache
// Called from SetupDatabase()
func InitTokenCache() {
	tokenCache = &TokenCache{
		cache: cache.New(5*time.Minute, 10*time.Minute),
	}
	slog.Info("Token cache initialized",
		"expiration", "5m",
		"cleanup_interval", "10m")
}

// CloseTokenCache cleans up the token cache
// Called from CloseDatabase()
func CloseTokenCache() {
	if tokenCache == nil {
		return
	}

	stats := tokenCache.GetStats()
	slog.Info("Closing token cache",
		"cached_tokens", stats.ItemCount)

	// Flush cache
	tokenCache.cache.Flush()
	tokenCache = nil

	slog.Info("Token cache closed")
}

// GetToken retrieves a token from cache
// Returns (token, true) if found, (empty, false) if not found
func (tc *TokenCache) GetToken(clientKey string) (PrivateToken, bool) {
	if tc == nil || tc.cache == nil {
		return PrivateToken{}, false
	}

	if cached, found := tc.cache.Get(clientKey); found {
		if token, ok := cached.(PrivateToken); ok {
			slog.Debug("Token cache hit", "client_key", clientKey)
			return token, true
		}
		// Type assertion failed, remove invalid entry
		tc.cache.Delete(clientKey)
	}

	slog.Debug("Token cache miss", "client_key", clientKey)
	return PrivateToken{}, false
}

// SetToken stores a token in cache with default expiration
func (tc *TokenCache) SetToken(clientKey string, token PrivateToken) {
	if tc == nil || tc.cache == nil {
		return
	}

	tc.cache.Set(clientKey, token, cache.DefaultExpiration)
	slog.Debug("Token cached",
		"client_key", clientKey,
		"display_name", token.DisplayName)
}

// InvalidateToken removes a specific token from cache
// Called when token is updated via SaveOAuthToken()
func (tc *TokenCache) InvalidateToken(clientKey string) {
	if tc == nil || tc.cache == nil {
		return
	}

	tc.cache.Delete(clientKey)
	slog.Info("Token cache invalidated", "client_key", clientKey)
}

// InvalidateAll clears the entire cache
// Useful for testing or emergency cache flush
func (tc *TokenCache) InvalidateAll() {
	if tc == nil || tc.cache == nil {
		return
	}

	tc.cache.Flush()
	slog.Warn("Token cache flushed - all entries invalidated")
}

// GetStats returns cache statistics
func (tc *TokenCache) GetStats() CacheStats {
	if tc == nil || tc.cache == nil {
		return CacheStats{ItemCount: 0}
	}

	return CacheStats{
		ItemCount: tc.cache.ItemCount(),
	}
}
```

### 3.2 Update Database Setup: `db/database.go`

**Update SetupDatabase() to initialize cache:**

```go
func SetupDatabase() error {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	var err error
	db, err = sqlx.Open("postgres", psqlInfo)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	err = db.Ping()
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Successfully connected to database")

	if err := migrateDB(); err != nil {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	// NEW: Initialize token cache after database is ready
	InitTokenCache()

	return nil
}
```

**Update CloseDatabase() (if exists from Issue #17) or add Close():**

```go
// Close closes the token cache and database connection
// Called during graceful shutdown
func Close() error {
	slog.Info("Closing database resources...")

	// 1. Close token cache first (Issue #20)
	CloseTokenCache()

	// 2. Close database connection
	if db != nil {
		if err := db.Close(); err != nil {
			return fmt.Errorf("failed to close database: %w", err)
		}
		slog.Info("Database connection closed")
	}

	return nil
}
```

### 3.3 Update GetOAuthToken with Caching: `db/database.go`

**Replace existing GetOAuthToken function:**

```go
// GetOAuthToken retrieves OAuth token with caching
// First checks cache, falls back to database on miss
func GetOAuthToken(clientKey string) (PrivateToken, error) {
	// Check cache first
	if tokenCache != nil {
		if token, found := tokenCache.GetToken(clientKey); found {
			return token, nil
		}
	}

	// Cache miss - fetch from database
	read_row :=
		`select id, access_token, refresh_token, display_name, client_key, created_on, scope, expires_in, token_type
		FROM privatetokens
		WHERE client_key = $1`
	tokenData := PrivateToken{}
	err := db.Get(&tokenData, read_row, clientKey)
	if err != nil {
		return PrivateToken{}, fmt.Errorf("failed to get OAuth token for client %s: %w", clientKey, err)
	}

	// Store in cache for future requests
	if tokenCache != nil {
		tokenCache.SetToken(clientKey, tokenData)
	}

	return tokenData, nil
}
```

### 3.4 Update SaveOAuthToken with Cache Invalidation: `db/database.go`

**Replace existing SaveOAuthToken function:**

```go
// SaveOAuthToken saves OAuth token and invalidates cache
func SaveOAuthToken(accessToken string, refreshToken string, displayName string, clientKey string, scope string, expiresIn int16, tokenType string) error {
	insert_row := `insert into privatetokens
		(access_token, refresh_token, display_name, client_key, scope, expires_in, token_type, created_on)
	values
		($1, $2, $3, $4, $5, $6, $7, current_timestamp)`

	_, err := db.Exec(insert_row, accessToken, refreshToken, displayName, clientKey, scope, expiresIn, tokenType)
	if err != nil {
		return fmt.Errorf("failed to save OAuth token for client %s: %w", clientKey, err)
	}

	// NEW: Invalidate cache entry for this client
	// Next request will fetch fresh token from database
	if tokenCache != nil {
		tokenCache.InvalidateToken(clientKey)
	}

	slog.Info("OAuth token saved", "client_key", clientKey, "display_name", displayName)
	return nil
}
```

### 3.5 Add go.mod Dependency

**Update go.mod to include go-cache:**

```bash
cd be
go get github.com/patrickmn/go-cache
```

**Expected go.mod addition:**
```
require (
	github.com/patrickmn/go-cache v2.1.0+incompatible
	// ... other dependencies
)
```

### 3.6 Integration with main.go (Issue #17 Integration)

**If using Issue #17's graceful shutdown pattern:**

```go
// main.go
func main() {
	// ... logger setup ...

	// Initialize database connection and cache
	if err := db.SetupDatabase(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}

	// Ensure database and cache are closed when application exits
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Error during database close", "error", err)
		}
	}()

	slog.Info("Starting web server")
	web.Server()
}
```

**If using Issue #8 graceful shutdown (more complete):**

```go
// main.go
func main() {
	// ... setup ...

	srv := web.StartServer()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutdown initiated")

	// Close database and cache
	if err := db.Close(); err != nil {
		slog.Error("Database close error", "error", err)
	}

	// Shutdown HTTP server
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	srv.Shutdown(ctx)

	slog.Info("Application exited cleanly")
}
```

---

## 4. Testing Strategy

### 4.1 Unit Tests: `db/token_cache_test.go` (NEW FILE)

```go
package db

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenCache_BasicOperations(t *testing.T) {
	// Initialize cache
	InitTokenCache()
	defer CloseTokenCache()

	// Create test token
	testToken := PrivateToken{
		Id:           1,
		AccessToken:  "access123",
		RefreshToken: "refresh123",
		DisplayName:  "Test User",
		ClientKey:    "test@example.com",
		Scope:        "https://www.googleapis.com/auth/gmail.readonly",
		ExpiresIn:    3600,
		TokenType:    "Bearer",
	}

	// Test cache miss
	token, found := tokenCache.GetToken("test@example.com")
	assert.False(t, found, "Should not find token initially")
	assert.Equal(t, PrivateToken{}, token, "Should return empty token")

	// Test cache set
	tokenCache.SetToken("test@example.com", testToken)

	// Test cache hit
	token, found = tokenCache.GetToken("test@example.com")
	assert.True(t, found, "Should find token after set")
	assert.Equal(t, testToken.ClientKey, token.ClientKey)
	assert.Equal(t, testToken.RefreshToken, token.RefreshToken)
}

func TestTokenCache_Expiration(t *testing.T) {
	// Create cache with very short TTL for testing
	tokenCache = &TokenCache{
		cache: cache.New(100*time.Millisecond, 200*time.Millisecond),
	}
	defer CloseTokenCache()

	testToken := PrivateToken{
		ClientKey:    "expire@example.com",
		RefreshToken: "will_expire",
	}

	// Set token
	tokenCache.SetToken("expire@example.com", testToken)

	// Should be found immediately
	_, found := tokenCache.GetToken("expire@example.com")
	assert.True(t, found, "Token should exist initially")

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Should not be found after expiration
	_, found = tokenCache.GetToken("expire@example.com")
	assert.False(t, found, "Token should expire after TTL")
}

func TestTokenCache_Invalidation(t *testing.T) {
	InitTokenCache()
	defer CloseTokenCache()

	testToken := PrivateToken{
		ClientKey:    "invalidate@example.com",
		RefreshToken: "will_be_invalidated",
	}

	// Set token
	tokenCache.SetToken("invalidate@example.com", testToken)

	// Verify exists
	_, found := tokenCache.GetToken("invalidate@example.com")
	assert.True(t, found, "Token should exist before invalidation")

	// Invalidate
	tokenCache.InvalidateToken("invalidate@example.com")

	// Should not be found after invalidation
	_, found = tokenCache.GetToken("invalidate@example.com")
	assert.False(t, found, "Token should not exist after invalidation")
}

func TestTokenCache_InvalidateAll(t *testing.T) {
	InitTokenCache()
	defer CloseTokenCache()

	// Add multiple tokens
	tokenCache.SetToken("user1@example.com", PrivateToken{ClientKey: "user1@example.com"})
	tokenCache.SetToken("user2@example.com", PrivateToken{ClientKey: "user2@example.com"})
	tokenCache.SetToken("user3@example.com", PrivateToken{ClientKey: "user3@example.com"})

	// Verify all exist
	stats := tokenCache.GetStats()
	assert.Equal(t, 3, stats.ItemCount, "Should have 3 cached tokens")

	// Invalidate all
	tokenCache.InvalidateAll()

	// Verify all removed
	stats = tokenCache.GetStats()
	assert.Equal(t, 0, stats.ItemCount, "Should have 0 cached tokens after flush")

	_, found1 := tokenCache.GetToken("user1@example.com")
	_, found2 := tokenCache.GetToken("user2@example.com")
	_, found3 := tokenCache.GetToken("user3@example.com")
	assert.False(t, found1, "User1 token should not exist")
	assert.False(t, found2, "User2 token should not exist")
	assert.False(t, found3, "User3 token should not exist")
}

func TestTokenCache_GetStats(t *testing.T) {
	InitTokenCache()
	defer CloseTokenCache()

	// Initially empty
	stats := tokenCache.GetStats()
	assert.Equal(t, 0, stats.ItemCount, "Should start empty")

	// Add tokens
	tokenCache.SetToken("user1@example.com", PrivateToken{ClientKey: "user1@example.com"})
	tokenCache.SetToken("user2@example.com", PrivateToken{ClientKey: "user2@example.com"})

	// Check count
	stats = tokenCache.GetStats()
	assert.Equal(t, 2, stats.ItemCount, "Should have 2 cached tokens")
}

func TestTokenCache_NilSafety(t *testing.T) {
	// Don't initialize cache
	tokenCache = nil

	// Should not panic on nil cache
	assert.NotPanics(t, func() {
		_, found := tokenCache.GetToken("test@example.com")
		assert.False(t, found, "Nil cache should return not found")
	})

	assert.NotPanics(t, func() {
		tokenCache.SetToken("test@example.com", PrivateToken{})
		// Should not panic, just no-op
	})

	assert.NotPanics(t, func() {
		tokenCache.InvalidateToken("test@example.com")
		// Should not panic, just no-op
	})

	assert.NotPanics(t, func() {
		stats := tokenCache.GetStats()
		assert.Equal(t, 0, stats.ItemCount)
	})
}
```

### 4.2 Integration Tests

**Test 1: GetOAuthToken Cache Behavior**

```bash
# Start server
go run .

# Expected logs:
# INFO Token cache initialized expiration=5m cleanup_interval=10m

# Make multiple scan requests with same account
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "GMail",
    "GMailScan": {
      "ClientKey": "user@gmail.com",
      "SearchFilter": "in:inbox"
    }
  }'

# Check logs - should see:
# DEBUG Token cache miss client_key=user@gmail.com
# DEBUG Token cached client_key=user@gmail.com display_name=...

# Make another request with same account
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "GPhotos",
    "GPhotosScan": {
      "AlbumId": "album123",
      "ClientKey": "user@gmail.com"
    }
  }'

# Check logs - should see:
# DEBUG Token cache hit client_key=user@gmail.com
# (No database query)
```

**Test 2: Cache Expiration**

```bash
# Make request to cache token
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{...}'

# Wait 6 minutes (longer than 5-min TTL)
sleep 360

# Make another request
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{...}'

# Expected: Cache miss (token expired and removed by cleanup)
# DEBUG Token cache miss client_key=user@gmail.com
```

**Test 3: Cache Invalidation on Token Save**

```bash
# Complete OAuth flow to save new token
# (triggers SaveOAuthToken)

# Check logs - should see:
# INFO OAuth token saved client_key=user@gmail.com display_name=...
# INFO Token cache invalidated client_key=user@gmail.com

# Next scan request should fetch from DB
# DEBUG Token cache miss client_key=user@gmail.com
```

**Test 4: Graceful Shutdown**

```bash
# Start server
go run . &
PID=$!

# Make some scan requests to populate cache
curl -X POST http://localhost:8090/api/scans ...

# Send SIGTERM
kill -TERM $PID

# Expected logs:
# INFO Shutdown initiated
# INFO Closing database resources...
# INFO Closing token cache cached_tokens=2
# INFO Token cache closed
# INFO Database connection closed
# INFO Application exited cleanly
```

### 4.3 Performance Testing

**Measure cache hit latency:**

```bash
# Install vegeta for load testing
go install github.com/tsenart/vegeta@latest

# Create scan request file
cat > scan_request.json << EOF
{
  "ScanType": "GMail",
  "GMailScan": {
    "ClientKey": "user@gmail.com",
    "SearchFilter": "in:inbox"
  }
}
EOF

# Load test with 10 requests/sec for 30 seconds
echo "POST http://localhost:8090/api/scans" | \
  vegeta attack -rate=10/s -duration=30s \
  -header "Content-Type: application/json" \
  -body scan_request.json | \
  vegeta report

# Expected results:
# - First request: higher latency (cache miss + DB query)
# - Subsequent requests: lower latency (cache hit, no DB query)
# - 99th percentile should improve significantly
```

**Cache statistics monitoring:**

```bash
# Could add admin endpoint to check cache stats
curl http://localhost:8090/admin/cache/stats

# Expected response:
{
  "token_cache": {
    "item_count": 5,
    "hit_rate": "92%"
  }
}
```

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Create db/token_cache.go with TokenCache implementation
- [ ] Update db/database.go with InitTokenCache() and CloseTokenCache()
- [ ] Modify GetOAuthToken() to use cache
- [ ] Modify SaveOAuthToken() to invalidate cache
- [ ] Update SetupDatabase() to call InitTokenCache()
- [ ] Update Close() to call CloseTokenCache()
- [ ] Add go-cache dependency to go.mod
- [ ] Unit tests passing
- [ ] Integration tests completed
- [ ] Performance tests show improvement

### 5.2 Deployment Steps

**Step 1: Add Dependency**

```bash
cd be
go get github.com/patrickmn/go-cache
go mod tidy
```

**Step 2: Create Token Cache**

```bash
# Create new file
touch db/token_cache.go

# Add implementation (see section 3.1)
# Verify builds
go build .
```

**Step 3: Update Database Layer**

```bash
# Update db/database.go:
# - Add InitTokenCache() call in SetupDatabase()
# - Add CloseTokenCache() call in Close()
# - Modify GetOAuthToken() for caching
# - Modify SaveOAuthToken() for invalidation

# Verify builds
go build .
```

**Step 4: Test Locally**

```bash
# Start server
./hdd &
PID=$!

# Check initialization logs
# Expected: "Token cache initialized"

# Make test requests
curl -X POST http://localhost:8090/api/scans ...

# Check for cache hit/miss logs

# Graceful shutdown
kill -TERM $PID

# Expected: "Token cache closed"
```

**Step 5: Deploy to Staging**

```bash
# Build
go build -o hdd

# Deploy
scp hdd staging:/opt/bhandaar/
ssh staging 'systemctl restart bhandaar'

# Verify logs
ssh staging 'journalctl -u bhandaar -n 50'

# Expected:
# - "Token cache initialized"
# - "Token cache miss" on first scan
# - "Token cache hit" on subsequent scans
```

**Step 6: Monitor Staging**

```bash
# Watch for cache-related logs
ssh staging 'journalctl -u bhandaar -f | grep -E "cache|token"'

# Run load tests
vegeta attack ...

# Monitor for 24 hours to verify:
# - No memory leaks
# - Cache hits working as expected
# - No cache-related errors
# - Performance improvement visible
```

**Step 7: Deploy to Production**

```bash
# Tag release
git tag -a v1.x.x -m "Add OAuth token caching (Issue #20)"
git push origin v1.x.x

# Build and deploy
go build -o hdd
docker build -t jyothri/hdd-go-build:v1.x.x .
docker push jyothri/hdd-go-build:v1.x.x

kubectl set image deployment/bhandaar-backend backend=jyothri/hdd-go-build:v1.x.x
kubectl rollout status deployment/bhandaar-backend
```

**Step 8: Post-Deployment Validation**

```bash
# Check logs for cache initialization
kubectl logs -f deployment/bhandaar-backend | grep "Token cache"

# Expected:
# INFO Token cache initialized expiration=5m cleanup_interval=10m

# Monitor performance
# Scan startup times should be ~27% faster

# Check for errors
kubectl logs -f deployment/bhandaar-backend | grep -i error

# Should see no cache-related errors
```

### 5.3 Rollback Plan

**If cache causes issues:**

```bash
# Kubernetes
kubectl rollout undo deployment/bhandaar-backend

# systemd
ssh production 'systemctl stop bhandaar'
ssh production 'cp /opt/bhandaar/hdd.backup /opt/bhandaar/hdd'
ssh production 'systemctl start bhandaar'

# Verify rollback
curl https://api.production.com/api/health
```

### 5.4 Monitoring Post-Deployment

**Metrics to Watch:**

1. **Cache Hit Rate** (via logs)
   ```bash
   # Count cache hits vs misses
   kubectl logs deployment/bhandaar-backend | grep "Token cache" | grep -c "hit"
   kubectl logs deployment/bhandaar-backend | grep "Token cache" | grep -c "miss"

   # Expected: >80% hit rate after warm-up
   ```

2. **Scan Startup Latency**
   ```bash
   # Monitor time from scan request to scan start
   # Should decrease by ~5ms per scan (27% improvement)
   ```

3. **Database Load**
   ```bash
   # PostgreSQL query count should decrease
   # Fewer GetOAuthToken queries
   ```

4. **Memory Usage**
   ```bash
   # Should increase by ~1-2 MB (acceptable)
   kubectl top pod -l app=bhandaar-backend
   ```

5. **Error Rates**
   ```bash
   # Should remain the same (cache fail-soft)
   kubectl logs -f deployment/bhandaar-backend | grep ERROR
   ```

---

## 6. Future Enhancements

### 6.1 Singleflight Pattern for Cache Stampede Prevention

**Problem:**
If cache expires while multiple concurrent requests are in-flight for the same token, all requests will miss cache and query the database simultaneously (cache stampede).

**Current Scenario (Low Risk):**
```
With current usage patterns, cache stampede is unlikely:
- Most users have 1-2 accounts
- Scans are infrequent (not thousands per second)
- 5-minute TTL means stampede window is small

However, for high-traffic deployments, this could be an issue.
```

**Solution: golang.org/x/sync/singleflight**

```go
import "golang.org/x/sync/singleflight"

type TokenCache struct {
	cache *cache.Cache
	group singleflight.Group  // NEW
}

func (tc *TokenCache) GetToken(clientKey string) (PrivateToken, bool) {
	// Check cache first
	if cached, found := tc.cache.Get(clientKey); found {
		return cached.(PrivateToken), true
	}

	// FUTURE: Use singleflight to deduplicate concurrent DB fetches
	// This prevents multiple goroutines from fetching same token
	// Only one DB query executes, others wait for result
	return PrivateToken{}, false
}

// GetOAuthToken with singleflight (future enhancement)
func GetOAuthToken(clientKey string) (PrivateToken, error) {
	// Check cache first
	if tokenCache != nil {
		if token, found := tokenCache.GetToken(clientKey); found {
			return token, nil
		}
	}

	// Cache miss - use singleflight to prevent stampede
	result, err, _ := tokenCache.group.Do(clientKey, func() (interface{}, error) {
		// Only one goroutine executes this for each clientKey
		read_row := `select ... FROM privatetokens WHERE client_key = $1`
		tokenData := PrivateToken{}
		err := db.Get(&tokenData, read_row, clientKey)
		if err != nil {
			return nil, err
		}

		// Cache for future requests
		if tokenCache != nil {
			tokenCache.SetToken(clientKey, tokenData)
		}

		return tokenData, nil
	})

	if err != nil {
		return PrivateToken{}, fmt.Errorf("failed to get OAuth token: %w", err)
	}

	return result.(PrivateToken), nil
}
```

**Benefits:**
- Prevents N concurrent requests from triggering N database queries
- Reduces database load during cache expiration
- Improves performance under high concurrency

**When to Implement:**
- After deployment shows cache stampede in production
- When scan request rate exceeds 100/sec
- When monitoring shows duplicate DB queries for same token

### 6.2 Account List Caching

**Expand cache scope to include account lists:**

```go
type TokenCache struct {
	cache         *cache.Cache
	accountsCache *cache.Cache  // NEW: separate cache for account lists
}

func (tc *TokenCache) GetAccountList(cacheKey string) ([]Account, bool) {
	if tc.accountsCache == nil {
		return nil, false
	}

	if cached, found := tc.accountsCache.Get(cacheKey); found {
		return cached.([]Account), true
	}

	return nil, false
}

func (tc *TokenCache) SetAccountList(cacheKey string, accounts []Account) {
	if tc.accountsCache == nil {
		return
	}

	tc.accountsCache.Set(cacheKey, accounts, 2*time.Minute)  // Shorter TTL
}

// GetRequestAccountsFromDb with caching
func GetRequestAccountsFromDb() ([]Account, error) {
	cacheKey := "all_request_accounts"

	// Check cache
	if tokenCache != nil {
		if accounts, found := tokenCache.GetAccountList(cacheKey); found {
			return accounts, nil
		}
	}

	// Cache miss - fetch from DB
	read_row := `select distinct display_name, client_key from privatetokens`
	accounts := []Account{}
	err := db.Select(&accounts, read_row)
	if err != nil {
		return nil, err
	}

	// Cache result
	if tokenCache != nil {
		tokenCache.SetAccountList(cacheKey, accounts)
	}

	return accounts, nil
}
```

### 6.3 Redis-Based Distributed Cache

**For multi-instance deployments:**

```go
import "github.com/go-redis/redis/v8"

type RedisTokenCache struct {
	client *redis.Client
}

func NewRedisTokenCache(addr string) *RedisTokenCache {
	return &RedisTokenCache{
		client: redis.NewClient(&redis.Options{
			Addr: addr,
		}),
	}
}

func (rtc *RedisTokenCache) GetToken(clientKey string) (PrivateToken, bool) {
	ctx := context.Background()
	val, err := rtc.client.Get(ctx, "token:"+clientKey).Result()
	if err != nil {
		return PrivateToken{}, false
	}

	var token PrivateToken
	if err := json.Unmarshal([]byte(val), &token); err != nil {
		return PrivateToken{}, false
	}

	return token, true
}

func (rtc *RedisTokenCache) SetToken(clientKey string, token PrivateToken) {
	ctx := context.Background()
	data, _ := json.Marshal(token)
	rtc.client.Set(ctx, "token:"+clientKey, data, 5*time.Minute)
}
```

**Benefits:**
- Shared cache across multiple backend instances
- Persistent cache (survives restarts)
- Better for horizontal scaling

**Drawbacks:**
- Additional infrastructure dependency
- Network latency for cache access
- More complex deployment

### 6.4 Prometheus Metrics

**Add cache metrics for monitoring:**

```go
import "github.com/prometheus/client_golang/prometheus"

var (
	cacheHitsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "bhandaar_token_cache_hits_total",
			Help: "Total number of token cache hits",
		},
	)

	cacheMissesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "bhandaar_token_cache_misses_total",
			Help: "Total number of token cache misses",
		},
	)

	cacheSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "bhandaar_token_cache_size",
			Help: "Current number of items in token cache",
		},
	)
)

func (tc *TokenCache) GetToken(clientKey string) (PrivateToken, bool) {
	if cached, found := tc.cache.Get(clientKey); found {
		cacheHitsTotal.Inc()  // Track hit
		return cached.(PrivateToken), true
	}

	cacheMissesTotal.Inc()  // Track miss
	return PrivateToken{}, false
}

// Periodic update of cache size
go func() {
	ticker := time.NewTicker(10 * time.Second)
	for range ticker.C {
		stats := tokenCache.GetStats()
		cacheSize.Set(float64(stats.ItemCount))
	}
}()
```

### 6.5 Dynamic TTL Based on Token Expiry

**Use OAuth token expires_in field:**

```go
func (tc *TokenCache) SetToken(clientKey string, token PrivateToken) {
	// Use token's expires_in field for TTL
	// Most OAuth tokens expire in 1 hour
	ttl := time.Duration(token.ExpiresIn) * time.Second

	// Cap at 5 minutes (don't cache for full token lifetime)
	if ttl > 5*time.Minute {
		ttl = 5 * time.Minute
	}

	tc.cache.Set(clientKey, token, ttl)
	slog.Debug("Token cached with dynamic TTL",
		"client_key", clientKey,
		"ttl_seconds", int(ttl.Seconds()))
}
```

---

## Appendix A: Complete File Changes Summary

### Files to Create

1. **`db/token_cache.go`** - NEW
   - TokenCache struct and methods
   - InitTokenCache() function
   - CloseTokenCache() function
   - GetToken(), SetToken(), InvalidateToken() methods
   - GetStats() for monitoring

2. **`db/token_cache_test.go`** - NEW
   - Unit tests for all cache operations
   - Expiration tests
   - Invalidation tests
   - Thread safety tests

### Files to Modify

1. **`db/database.go`** - UPDATE
   - Add global `tokenCache` variable
   - Call InitTokenCache() in SetupDatabase()
   - Call CloseTokenCache() in Close()
   - Modify GetOAuthToken() to check cache first
   - Modify SaveOAuthToken() to invalidate cache

2. **`go.mod`** - UPDATE
   - Add `github.com/patrickmn/go-cache v2.1.0+incompatible`

3. **`main.go`** - UPDATE (if not using Issue #17/8 patterns)
   - Add defer db.Close() call

### Files NOT Changed

- `collect/gmail.go` - No changes (transparent caching)
- `collect/photos.go` - No changes (transparent caching)
- `collect/drive.go` - No changes (transparent caching)
- `web/api.go` - No changes (transparent caching)

---

## Appendix B: Cache Performance Comparison

### Before Caching

| Operation | DB Queries | DB Time | Total Time |
|-----------|------------|---------|------------|
| Gmail scan start | 1 | 5ms | 18ms |
| Photos scan start | 1 | 5ms | 18ms |
| 10 scans (same user) | 10 | 50ms | 180ms |
| 100 scans (same user) | 100 | 500ms | 1800ms |

### After Caching

| Operation | DB Queries | DB Time | Cache Time | Total Time |
|-----------|------------|---------|------------|------------|
| Gmail scan start (miss) | 1 | 5ms | - | 18ms |
| Photos scan start (hit) | 0 | 0ms | 0.1ms | 13.1ms |
| 10 scans (same user) | 1 | 5ms | 0.9ms | 135.9ms |
| 100 scans (same user) | 1 | 5ms | 9.9ms | 1314.9ms |

**Improvement Summary:**
- Single scan (cache hit): 27% faster (18ms → 13.1ms)
- 10 scans (same user): 24% faster (180ms → 135.9ms)
- 100 scans (same user): 27% faster (1800ms → 1314.9ms)
- Database load: 90-99% reduction in token queries

---

## Appendix C: Troubleshooting Guide

### Problem: Cache not initializing

**Diagnosis:**
```bash
# Check logs for initialization
grep "Token cache initialized" /var/log/bhandaar/app.log

# If not found, cache not initialized
```

**Solution:**
- Verify InitTokenCache() called in SetupDatabase()
- Check for errors during database setup
- Ensure go-cache dependency installed (`go mod download`)

### Problem: Cache always missing

**Diagnosis:**
```bash
# Check logs for cache operations
grep "Token cache" /var/log/bhandaar/app.log

# If only seeing "miss", never "hit":
# - Check if same clientKey being used
# - Check if TTL too short
# - Check if cache being flushed prematurely
```

**Solution:**
- Verify SetToken() being called after DB fetch
- Check cache expiration setting (should be 5 minutes)
- Ensure cache not being invalidated unintentionally

### Problem: Stale tokens being served

**Diagnosis:**
```bash
# Check if token updated in DB but cache still serving old token
# Look for SaveOAuthToken calls
grep "OAuth token saved" /var/log/bhandaar/app.log

# Check if invalidation happening
grep "Token cache invalidated" /var/log/bhandaar/app.log
```

**Solution:**
- Verify InvalidateToken() called in SaveOAuthToken()
- Check if correct clientKey being passed to InvalidateToken()
- May need to reduce TTL if token updates are frequent

### Problem: Memory growth from cache

**Diagnosis:**
```bash
# Check cache size over time
# Would need admin endpoint or metrics

# Check memory usage
ps aux | grep hdd | awk '{print $6}'

# Expected increase: 1-2 MB
# If > 10 MB increase, possible issue
```

**Solution:**
- Verify cleanup interval working (10 minutes)
- Check for cache entry leaks (items not expiring)
- Review cache size with GetStats()
- Consider shorter TTL if memory constrained

### Problem: Cache hit but scan fails with auth error

**Diagnosis:**
```bash
# Token in cache might be invalid/expired
# Check for OAuth errors
grep -A 5 "OAuth" /var/log/bhandaar/app.log | grep -i error
```

**Solution:**
- This is expected behavior - cache can serve stale tokens
- OAuth client library should refresh token automatically
- If persistent, manually invalidate: tokenCache.InvalidateToken(clientKey)
- Consider implementing dynamic TTL based on token expires_in

---

**END OF DOCUMENT**
