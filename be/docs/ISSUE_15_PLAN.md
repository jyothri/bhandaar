# Issue #15 Implementation Plan: No Rate Limiting

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P1 - High Priority (Security & Availability)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #15: No Rate Limiting**. The current system has no protection against API abuse or DoS attacks, allowing unlimited requests from any client.

**Selected Approach:**
- **Strategy**: Per-IP global rate limiting
- **Limits**: Conservative - 10 req/sec, burst 20 (global); 5 req/min for POST /api/scans
- **Critical Protection**: Stricter limit on scan creation endpoint
- **Storage**: In-memory only (simple, sufficient for single instance)
- **Headers**: Standard X-RateLimit-* headers
- **IP Extraction**: X-Real-IP or X-Forwarded-For (configurable priority)
- **Whitelist**: Support for whitelisting specific IPs
- **Future**: IP-based now, migrate to user-based after Issue #3 (Authentication)
- **Monitoring**: Minimal - just respond with 429
- **Testing**: Manual testing with curl

**Estimated Effort:** 6-8 hours

**Impact:**
- Prevents API abuse and DoS attacks
- Protects expensive scan operations
- Maintains service availability under load
- Provides clear feedback to clients via headers
- Supports reverse proxy deployments

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

### 1.1 Current State - No Protection

**web/web_server.go:**
```go
func Server() {
	slog.Info("Starting web server.")
	r := mux.NewRouter()

	// Apply global default size limit to all routes (512 KB)
	r.Use(RequestSizeLimitMiddleware(DefaultMaxBodySize))  // ✅ Issue #7

	api(r)
	oauth(r)
	sse(r)

	// ❌ NO RATE LIMITING!
	// Any IP can make unlimited requests

	// ...
}
```

### 1.2 Attack Scenarios

**Scenario 1: Scan Spam Attack**

```bash
# Attacker creates 1000 scans in 1 minute
for i in {1..1000}; do
  curl -X POST http://api.example.com/api/scans \
    -H "Content-Type: application/json" \
    -d '{"ScanType":"Local","LocalScan":{"Path":"/"}}'
done

# Result:
# - 1000 scans created, consuming resources
# - Database fills with scan records
# - Background workers overwhelmed
# - Legitimate users can't create scans
# - Server performance degrades
```

**Scenario 2: Accidental Infinite Loop**

```javascript
// Frontend bug causes infinite retry loop
async function fetchScans() {
  try {
    const response = await fetch('/api/scans');
    // ... handle response ...
  } catch (error) {
    // ❌ Bug: immediately retry on error
    fetchScans();  // Infinite recursion!
  }
}
```

**Result:**
- Thousands of requests per second
- Server CPU/memory exhaustion
- Other users affected
- No automatic protection

**Scenario 3: Brute Force OAuth**

```bash
# Try to brute force OAuth tokens
while true; do
  curl "http://api.example.com/api/glink?code=attempt_$RANDOM"
  sleep 0.1
done

# Result:
# - Hundreds of OAuth requests
# - Google API quota exhausted
# - Legitimate OAuth flows fail
```

**Scenario 4: SSE Connection Spam**

```bash
# Open 1000 SSE connections
for i in {1..1000}; do
  curl -N http://api.example.com/sse/scanprogress &
done

# Result:
# - 1000 goroutines (from Issue #13)
# - Memory exhaustion
# - Server becomes unresponsive
```

### 1.3 Vulnerability Assessment

| Vulnerability | Severity | Exploitability | Impact |
|---------------|----------|----------------|--------|
| **Unlimited scan creation** | CRITICAL | Easy (curl loop) | Resource exhaustion, DoS |
| **API flooding** | HIGH | Easy (script) | Service degradation |
| **OAuth endpoint abuse** | HIGH | Medium (requires knowledge) | Quota exhaustion |
| **SSE connection spam** | HIGH | Easy (curl loop) | Memory exhaustion |
| **No client accountability** | MEDIUM | N/A | Can't identify/block abusers |

### 1.4 Current Traffic Patterns (Assumptions)

**Legitimate Usage:**
- Health checks: 1-2 req/sec (monitoring)
- User browsing: 5-10 req/min (viewing scans, data)
- Scan creation: 1-2 per user session (infrequent)
- SSE connections: 1 per active user

**Expected Peaks:**
- Multiple users creating scans simultaneously: 5-10 req/sec
- Dashboard refreshes: 20-30 req/sec (multiple endpoints)

**Abuse Patterns to Block:**
- >100 requests in 10 seconds
- >10 scan creations per minute
- Sustained high request rate

---

## 2. Target Architecture

### 2.1 Rate Limiting Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. HTTP Request Arrives                                     │
│    GET/POST /api/...                                        │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Extract Client IP                                        │
│    - Check X-Real-IP header (priority 1)                   │
│    - Check X-Forwarded-For header (priority 2)             │
│    - Fall back to r.RemoteAddr (priority 3)                │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Check IP Whitelist                                       │
│    - If IP in whitelist → Skip rate limiting              │
│    - Otherwise → Continue to rate check                    │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Get/Create Rate Limiter for IP                          │
│    - Look up limiter in map                                 │
│    - If not exists, create new limiter                      │
│    - Return limiter for IP                                  │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Check Global Rate Limit                                  │
│    - Limit: 10 req/sec, burst 20                           │
│    - Check if token available                               │
└─────────────────────────────────────────────────────────────┘
                            │
                ┌───────────┴──────────┐
                │                      │
               No                     Yes
                │                      │
                ▼                      ▼
    ┌─────────────────────┐  ┌─────────────────────┐
    │ 6a. Rate Limited    │  │ 6b. Check Endpoint  │
    │ Return HTTP 429     │  │ Specific Limit      │
    │ + Headers           │  │ (if POST /scans)    │
    └─────────────────────┘  └─────────────────────┘
                                        │
                            ┌───────────┴──────────┐
                            │                      │
                           No                     Yes
                            │                      │
                            ▼                      ▼
                ┌─────────────────────┐  ┌─────────────────────┐
                │ 7a. Allow Request   │  │ 7b. Rate Limited    │
                │ Add headers         │  │ Return HTTP 429     │
                │ Call next handler   │  │ + Headers           │
                └─────────────────────┘  └─────────────────────┘
```

### 2.2 Rate Limiter Architecture

```
┌─────────────────────────────────────────────────────────────┐
│ RateLimiter                                                  │
│                                                              │
│ Fields:                                                      │
│ - visitors: map[string]*rate.Limiter                        │
│ - mu: sync.RWMutex                                          │
│ - globalRate: 10 req/sec                                    │
│ - globalBurst: 20                                           │
│ - scanRate: 5 req/min                                       │
│ - whitelist: map[string]bool                                │
│                                                              │
│ Methods:                                                     │
│ - GetLimiter(ip) → *rate.Limiter                           │
│ - CheckGlobalLimit(ip) → allowed bool                      │
│ - CheckScanLimit(ip) → allowed bool                        │
│ - IsWhitelisted(ip) → bool                                  │
│ - AddToWhitelist(ip)                                        │
│ - RemoveFromWhitelist(ip)                                   │
│ - CleanupStaleEntries()                                     │
└─────────────────────────────────────────────────────────────┘
```

### 2.3 Rate Limit Configuration

| Limit Type | Rate | Burst | Applied To | Rationale |
|------------|------|-------|------------|-----------|
| **Global** | 10/sec | 20 | All endpoints | Conservative, prevents flooding |
| **Scan Creation** | 5/min | 1 | POST /api/scans | Expensive operation, prevent spam |

**Token Bucket Algorithm:**
- Each IP gets a bucket that refills at `rate` per second
- Bucket capacity is `burst`
- Each request consumes 1 token
- If no tokens available → 429 Too Many Requests

**Example Timeline:**
```
Time  | Tokens | Action              | Result
------|--------|---------------------|--------
0s    | 20     | 5 requests          | 15 tokens remain, all allowed
0.5s  | 20     | 25 requests         | First 20 allowed, next 5 → 429
1.0s  | 10     | (refill +10)        | 10 tokens available
1.5s  | 15     | (refill +5)         | 15 tokens available
2.0s  | 20     | (refill +5, capped) | 20 tokens (max burst)
```

### 2.4 HTTP Response Headers

**Successful Request (200 OK):**
```
X-RateLimit-Limit: 10
X-RateLimit-Remaining: 7
X-RateLimit-Reset: 1640000005
```

**Rate Limited Request (429 Too Many Requests):**
```
HTTP/1.1 429 Too Many Requests
X-RateLimit-Limit: 10
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1640000005
Content-Type: application/json

{
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Rate limit exceeded. Please try again later.",
    "retry_after": 5
  }
}
```

---

## 3. Implementation Details

### 3.1 Rate Limiter Package: `ratelimit/limiter.go` (NEW FILE)

```go
package ratelimit

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter manages rate limiting for clients by IP
type RateLimiter struct {
	// Map of IP address to rate limiter
	visitors map[string]*visitor

	// Mutex for thread-safe map access
	mu sync.RWMutex

	// Global rate limit settings
	globalRate  rate.Limit
	globalBurst int

	// Scan-specific rate limit settings
	scanRate  rate.Limit
	scanBurst int

	// IP whitelist (exempt from rate limiting)
	whitelist map[string]bool
}

// visitor tracks rate limiters for a single IP
type visitor struct {
	globalLimiter *rate.Limiter
	scanLimiter   *rate.Limiter
	lastSeen      time.Time
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter() *RateLimiter {
	rl := &RateLimiter{
		visitors:    make(map[string]*visitor),
		globalRate:  rate.Limit(10), // 10 requests per second
		globalBurst: 20,
		scanRate:    rate.Every(12 * time.Second), // 5 per minute
		scanBurst:   1,
		whitelist:   make(map[string]bool),
	}

	// Start cleanup goroutine
	go rl.cleanupStaleEntries()

	return rl
}

// getVisitor returns the visitor for an IP, creating if needed
func (rl *RateLimiter) getVisitor(ip string) *visitor {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		v = &visitor{
			globalLimiter: rate.NewLimiter(rl.globalRate, rl.globalBurst),
			scanLimiter:   rate.NewLimiter(rl.scanRate, rl.scanBurst),
			lastSeen:      time.Now(),
		}
		rl.visitors[ip] = v
	}

	v.lastSeen = time.Now()
	return v
}

// AllowGlobal checks if request is allowed under global rate limit
func (rl *RateLimiter) AllowGlobal(ip string) bool {
	// Check whitelist first
	if rl.IsWhitelisted(ip) {
		return true
	}

	v := rl.getVisitor(ip)
	return v.globalLimiter.Allow()
}

// AllowScan checks if scan request is allowed
func (rl *RateLimiter) AllowScan(ip string) bool {
	// Check whitelist first
	if rl.IsWhitelisted(ip) {
		return true
	}

	v := rl.getVisitor(ip)
	return v.scanLimiter.Allow()
}

// GetLimitInfo returns current limit information for headers
func (rl *RateLimiter) GetLimitInfo(ip string) (limit, remaining int, reset time.Time) {
	if rl.IsWhitelisted(ip) {
		// Whitelisted IPs have no limit
		return 0, 999999, time.Time{}
	}

	v := rl.getVisitor(ip)

	limit = rl.globalBurst
	reservation := v.globalLimiter.Reserve()
	if !reservation.OK() {
		remaining = 0
		reset = time.Now().Add(time.Second)
	} else {
		reservation.Cancel() // Don't actually consume token

		// Calculate remaining tokens (approximate)
		// This is a simplification - actual implementation would track more precisely
		remaining = rl.globalBurst - 1 // Rough estimate

		// Reset time is when bucket refills to burst capacity
		delay := reservation.Delay()
		reset = time.Now().Add(delay)
	}

	return limit, remaining, reset
}

// IsWhitelisted checks if IP is whitelisted
func (rl *RateLimiter) IsWhitelisted(ip string) bool {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.whitelist[ip]
}

// AddToWhitelist adds IP to whitelist
func (rl *RateLimiter) AddToWhitelist(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.whitelist[ip] = true
}

// RemoveFromWhitelist removes IP from whitelist
func (rl *RateLimiter) RemoveFromWhitelist(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.whitelist, ip)
}

// cleanupStaleEntries removes visitors not seen in 3 minutes
func (rl *RateLimiter) cleanupStaleEntries() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()

		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 3*time.Minute {
				delete(rl.visitors, ip)
			}
		}

		rl.mu.Unlock()
	}
}

// GetVisitorCount returns number of active visitors (for monitoring)
func (rl *RateLimiter) GetVisitorCount() int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.visitors)
}
```

### 3.2 IP Extraction Utility: `ratelimit/ip.go` (NEW FILE)

```go
package ratelimit

import (
	"net"
	"net/http"
	"strings"
)

// GetClientIP extracts the real client IP from the request
// Checks headers in order: X-Real-IP, X-Forwarded-For, RemoteAddr
func GetClientIP(r *http.Request) string {
	// Priority 1: X-Real-IP header (set by nginx, Cloudflare, etc.)
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		if parsedIP := net.ParseIP(ip); parsedIP != nil {
			return ip
		}
	}

	// Priority 2: X-Forwarded-For header (standard for proxies)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs: "client, proxy1, proxy2"
		// Take the first one (original client)
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			ip := strings.TrimSpace(ips[0])
			if parsedIP := net.ParseIP(ip); parsedIP != nil {
				return ip
			}
		}
	}

	// Priority 3: RemoteAddr (direct connection)
	// RemoteAddr includes port, need to strip it
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// If SplitHostPort fails, might be just IP without port
		return r.RemoteAddr
	}

	return ip
}
```

### 3.3 Rate Limit Middleware: `web/middleware.go` (UPDATE)

**Add to existing middleware.go:**

```go
package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jyothri/hdd/ratelimit"
)

// Global rate limiter instance
var rateLimiter *ratelimit.RateLimiter

// InitRateLimiter initializes the global rate limiter
func InitRateLimiter() {
	rateLimiter = ratelimit.NewRateLimiter()

	// Add whitelisted IPs if any (from environment or config)
	// Example: whitelist localhost for testing
	rateLimiter.AddToWhitelist("127.0.0.1")
	rateLimiter.AddToWhitelist("::1")

	slog.Info("Rate limiter initialized",
		"global_rate", "10/sec",
		"global_burst", 20,
		"scan_rate", "5/min")
}

// RateLimitMiddleware applies global rate limiting
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ratelimit.GetClientIP(r)

		// Check global rate limit
		if !rateLimiter.AllowGlobal(ip) {
			handleRateLimitExceeded(w, r, ip, "global")
			return
		}

		// Add rate limit headers
		addRateLimitHeaders(w, ip)

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}

// ScanRateLimitMiddleware applies scan-specific rate limiting
func ScanRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := ratelimit.GetClientIP(r)

		// Check scan-specific rate limit
		if !rateLimiter.AllowScan(ip) {
			handleRateLimitExceeded(w, r, ip, "scan")
			return
		}

		// Continue to next handler
		next.ServeHTTP(w, r)
	})
}

// handleRateLimitExceeded sends 429 response
func handleRateLimitExceeded(w http.ResponseWriter, r *http.Request, ip, limitType string) {
	slog.Warn("Rate limit exceeded",
		"ip", ip,
		"path", r.URL.Path,
		"method", r.Method,
		"limit_type", limitType)

	// Add rate limit headers
	addRateLimitHeaders(w, ip)

	// Calculate retry after (rough estimate)
	retryAfter := 60 // seconds
	if limitType == "global" {
		retryAfter = 6 // ~1/10th of a second * 60 = 6 seconds
	}

	// Send 429 response
	errorResponse := map[string]interface{}{
		"error": map[string]interface{}{
			"code":        "RATE_LIMIT_EXCEEDED",
			"message":     "Rate limit exceeded. Please try again later.",
			"retry_after": retryAfter,
			"limit_type":  limitType,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	w.WriteHeader(http.StatusTooManyRequests)

	json.NewEncoder(w).Encode(errorResponse)
}

// addRateLimitHeaders adds standard rate limit headers
func addRateLimitHeaders(w http.ResponseWriter, ip string) {
	limit, remaining, reset := rateLimiter.GetLimitInfo(ip)

	w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
	w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
	w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", reset.Unix()))
}
```

### 3.4 Update Web Server: `web/web_server.go`

**Apply rate limiting middleware:**

```go
package web

import (
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/constants"
	"github.com/rs/cors"
)

func Server() {
	slog.Info("Starting web server.")

	// Initialize rate limiter
	InitRateLimiter()

	r := mux.NewRouter()

	// Apply global middlewares in order:
	// 1. Rate limiting (protect against floods)
	r.Use(RateLimitMiddleware)

	// 2. Size limit (protect against large payloads)
	r.Use(RequestSizeLimitMiddleware(DefaultMaxBodySize))

	// Setup routes
	api(r)
	oauth(r)
	sse(r)

	// CORS
	cors := cors.New(cors.Options{
		AllowedOrigins:   []string{constants.FrontendUrl},
		AllowCredentials: true,
	})
	handler := cors.Handler(r)

	srv := &http.Server{
		Handler:      handler,
		Addr:         ":8090",
		WriteTimeout: 10 * time.Second,
		ReadTimeout:  10 * time.Second,
	}

	log.Fatal(srv.ListenAndServe())
}
```

### 3.5 Update API Routes: `web/api.go`

**Add scan-specific rate limiting:**

```go
func api(r *mux.Router) {
	// Handle API routes
	api := r.PathPrefix("/api/").Subrouter()

	// Health check endpoint (no additional rate limiting)
	api.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		setJsonHeader(w)
		json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})

	// Scan POST endpoint with BOTH size limit AND scan rate limit
	scanPostRouter := api.PathPrefix("/scans").Subrouter()
	scanPostRouter.Use(RequestSizeLimitMiddleware(ScanRequestMaxBodySize)) // 1 MB
	scanPostRouter.Use(ScanRateLimitMiddleware) // ✅ NEW: 5 scans per minute
	scanPostRouter.HandleFunc("", DoScansHandler).Methods("POST")

	// Other scan endpoints (GET, DELETE) use global rate limit only
	api.HandleFunc("/scans/requests/{account_key}", GetScanRequestsHandler).Methods("GET")
	api.HandleFunc("/scans/accounts", GetAccountsHandler).Methods("GET")
	api.HandleFunc("/scans/{scan_id}", DeleteScanHandler).Methods("DELETE")
	api.HandleFunc("/scans", ListScansHandler).Methods("GET").Queries("page", "{page}")
	api.HandleFunc("/scans", ListScansHandler).Methods("GET")

	// ... rest of endpoints (all protected by global rate limit)
}
```

### 3.6 Whitelist Management (Optional Admin Endpoint)

**Add admin endpoint to manage whitelist:**

```go
// Add to api.go (protected by authentication after Issue #3)

func AddWhitelistHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: Require admin authentication (Issue #3)

	var req struct {
		IP string `json:"ip"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	rateLimiter.AddToWhitelist(req.IP)
	slog.Info("IP added to whitelist", "ip", req.IP)

	writeJSONResponseOK(w, map[string]string{
		"message": "IP added to whitelist",
		"ip":      req.IP,
	})
}

func RemoveWhitelistHandler(w http.ResponseWriter, r *http.Request) {
	// TODO: Require admin authentication (Issue #3)

	var req struct {
		IP string `json:"ip"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	rateLimiter.RemoveFromWhitelist(req.IP)
	slog.Info("IP removed from whitelist", "ip", req.IP)

	writeJSONResponseOK(w, map[string]string{
		"message": "IP removed from whitelist",
		"ip":      req.IP,
	})
}

// Add to api() function:
// api.HandleFunc("/admin/whitelist", AddWhitelistHandler).Methods("POST")
// api.HandleFunc("/admin/whitelist", RemoveWhitelistHandler).Methods("DELETE")
```

---

## 4. Testing Strategy

### 4.1 Manual Testing with curl

**Test 1: Normal Usage (Under Limit)**

```bash
# Make 5 requests (under burst of 20)
for i in {1..5}; do
  echo "Request $i:"
  curl -i http://localhost:8090/api/health 2>/dev/null | grep -E "HTTP|X-RateLimit"
  echo
done

# Expected:
# HTTP/1.1 200 OK
# X-RateLimit-Limit: 20
# X-RateLimit-Remaining: 19, 18, 17, 16, 15
# X-RateLimit-Reset: <timestamp>
```

**Test 2: Burst Limit (Exceed Burst)**

```bash
# Make 25 requests rapidly (burst is 20)
for i in {1..25}; do
  curl -s -o /dev/null -w "Request $i: %{http_code}\n" http://localhost:8090/api/health
done

# Expected:
# Request 1-20: 200
# Request 21-25: 429 (rate limited)
```

**Test 3: Scan Rate Limit**

```bash
# Try to create 10 scans (limit is 5/min)
for i in {1..10}; do
  echo "Scan $i:"
  curl -s -o /dev/null -w "%{http_code}\n" \
    -X POST http://localhost:8090/api/scans \
    -H "Content-Type: application/json" \
    -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp"}}'
  sleep 1
done

# Expected:
# Scans 1-5: 200 (or appropriate response)
# Scans 6-10: 429 (rate limited)

# Wait 60 seconds, try again
sleep 60
curl -s -o /dev/null -w "%{http_code}\n" \
  -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp"}}'

# Expected: 200 (limit reset)
```

**Test 4: IP Extraction (with Proxy Headers)**

```bash
# Test X-Real-IP header
curl -i http://localhost:8090/api/health \
  -H "X-Real-IP: 192.168.1.100" 2>/dev/null | grep X-RateLimit

# Test X-Forwarded-For header
curl -i http://localhost:8090/api/health \
  -H "X-Forwarded-For: 192.168.1.100, 10.0.0.1" 2>/dev/null | grep X-RateLimit

# Each should be rate limited independently
# (192.168.1.100 has separate limit from your real IP)
```

**Test 5: Whitelist**

```bash
# Add your IP to whitelist (via code or admin endpoint)
# Then make unlimited requests

for i in {1..100}; do
  curl -s -o /dev/null -w "%{http_code} " http://localhost:8090/api/health
done
echo

# Expected: All 200 (no 429s)
```

**Test 6: Rate Limit Headers**

```bash
# Check headers on normal request
curl -i http://localhost:8090/api/health 2>/dev/null | grep X-RateLimit

# Expected:
# X-RateLimit-Limit: 20
# X-RateLimit-Remaining: 19
# X-RateLimit-Reset: 1640000005

# Check headers on rate limited request
# (after exceeding burst)
for i in {1..25}; do
  curl -s http://localhost:8090/api/health > /dev/null
done

curl -i http://localhost:8090/api/health 2>/dev/null | grep -E "HTTP|X-RateLimit|Retry-After"

# Expected:
# HTTP/1.1 429 Too Many Requests
# X-RateLimit-Limit: 20
# X-RateLimit-Remaining: 0
# X-RateLimit-Reset: <timestamp>
# Retry-After: 6
```

**Test 7: Error Response Format**

```bash
# Trigger rate limit and check JSON error
for i in {1..25}; do curl -s http://localhost:8090/api/health > /dev/null; done

curl -s http://localhost:8090/api/health | jq

# Expected JSON:
{
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Rate limit exceeded. Please try again later.",
    "retry_after": 6,
    "limit_type": "global"
  }
}
```

### 4.2 Cleanup Testing

**Test visitor cleanup (3-minute stale entry removal):**

```bash
# Make request to create visitor entry
curl http://localhost:8090/api/health

# Check visitor count (would need admin endpoint or logs)
# Wait 4 minutes
# Check again - should be removed
```

### 4.3 Load Testing (Optional)

If desired, can use vegeta or ab:

```bash
# Install vegeta
go install github.com/tsenart/vegeta@latest

# Test sustained load
echo "GET http://localhost:8090/api/health" | \
  vegeta attack -duration=30s -rate=20/s | \
  vegeta report

# Expected: Some 429 responses after burst exhausted
# Should see mix of 200 and 429

# Test scan endpoint
echo "POST http://localhost:8090/api/scans" | \
  vegeta attack -duration=60s -rate=1/s \
  -header "Content-Type: application/json" \
  -body scan_request.json | \
  vegeta report

# Expected: ~5 successful per minute, rest 429
```

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Create ratelimit/limiter.go
- [ ] Create ratelimit/ip.go
- [ ] Update web/middleware.go with rate limit middleware
- [ ] Update web/web_server.go to apply middleware
- [ ] Update web/api.go with scan rate limiting
- [ ] Manual testing completed
- [ ] Build succeeds
- [ ] No performance degradation

### 5.2 Deployment Steps

**Step 1: Create Rate Limit Package**

```bash
mkdir -p ratelimit
# Create limiter.go and ip.go

# Verify builds
go build .
```

**Step 2: Update Middleware**

```bash
# Update web/middleware.go
# Add RateLimitMiddleware
# Add ScanRateLimitMiddleware

go build .
```

**Step 3: Apply to Web Server**

```bash
# Update web/web_server.go
# Update web/api.go

go build .
```

**Step 4: Test Locally**

```bash
# Start server
./hdd &

# Run manual tests
./test_rate_limits.sh

# Expected: All tests pass
```

**Step 5: Deploy to Staging**

```bash
# Build
go build -o hdd

# Deploy
scp hdd staging:/opt/bhandaar/
ssh staging 'systemctl restart bhandaar'

# Test on staging
curl -i https://staging.example.com/api/health | grep X-RateLimit

# Load test
vegeta attack ...
```

**Step 6: Monitor Staging**

```bash
# Watch for rate limit logs
ssh staging 'journalctl -u bhandaar -f | grep "Rate limit"'

# Check for false positives (legitimate users rate limited)
# Adjust limits if needed
```

**Step 7: Deploy to Production**

```bash
# Tag release
git tag -a v1.x.x -m "Add rate limiting (Issue #15)"
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
# Test rate limit headers present
curl -i https://api.production.com/api/health | grep X-RateLimit

# Monitor logs for rate limit events
kubectl logs -f deployment/bhandaar-backend | grep "Rate limit"

# Watch for abuse patterns
# Should see 429 responses blocking rapid requests
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
```

### 5.4 Configuration Tuning

**If limits too strict:**

```go
// In ratelimit/limiter.go NewRateLimiter():
globalRate:  rate.Limit(20), // Increase from 10 to 20
globalBurst: 40,              // Increase from 20 to 40
```

**If limits too lenient:**

```go
globalRate:  rate.Limit(5),  // Decrease from 10 to 5
globalBurst: 10,             // Decrease from 20 to 10
```

---

## 6. Future Enhancements

### 6.1 User-Based Rate Limiting (After Issue #3)

Once authentication is implemented:

```go
// In middleware, extract user ID instead of/in addition to IP
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Get user from context (set by auth middleware)
		userID := getUserIDFromContext(r.Context())

		if userID != "" {
			// Use user-based rate limiting
			if !rateLimiter.AllowUser(userID) {
				handleRateLimitExceeded(w, r, userID, "user")
				return
			}
		} else {
			// Fall back to IP-based for unauthenticated requests
			ip := ratelimit.GetClientIP(r)
			if !rateLimiter.AllowGlobal(ip) {
				handleRateLimitExceeded(w, r, ip, "global")
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
```

### 6.2 Redis-Based Rate Limiting (Multi-Instance)

For distributed deployments:

```go
// Use Redis to store rate limit state
import "github.com/go-redis/redis_rate/v10"

type RedisRateLimiter struct {
	client *redis.Client
	limiter *redis_rate.Limiter
}

func (rl *RedisRateLimiter) Allow(key string) bool {
	result, err := rl.limiter.Allow(context.Background(), key, redis_rate.PerSecond(10))
	if err != nil {
		// Log error, fail open or closed depending on requirements
		return true // Fail open
	}
	return result.Allowed > 0
}
```

### 6.3 Prometheus Metrics

```go
var (
	rateLimitExceededTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bhandaar_rate_limit_exceeded_total",
			Help: "Total number of rate limit exceeded responses",
		},
		[]string{"limit_type", "endpoint"},
	)

	activeVisitorsGauge = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "bhandaar_rate_limit_active_visitors",
			Help: "Number of active visitors being tracked",
		},
	)
)

// In handleRateLimitExceeded:
rateLimitExceededTotal.WithLabelValues(limitType, r.URL.Path).Inc()

// Periodic update:
activeVisitorsGauge.Set(float64(rateLimiter.GetVisitorCount()))
```

### 6.4 Dynamic Rate Limits

```go
// Load limits from environment or config file
type RateLimitConfig struct {
	GlobalRate  float64 `json:"global_rate"`
	GlobalBurst int     `json:"global_burst"`
	ScanRate    float64 `json:"scan_rate"`
	ScanBurst   int     `json:"scan_burst"`
}

func loadConfigFromEnv() *RateLimitConfig {
	return &RateLimitConfig{
		GlobalRate:  getEnvFloat("RATE_LIMIT_GLOBAL", 10.0),
		GlobalBurst: getEnvInt("RATE_LIMIT_BURST", 20),
		// ...
	}
}
```

### 6.5 Endpoint-Specific Limits

```go
// Different limits for different endpoints
type EndpointLimits struct {
	GET  *rate.Limiter
	POST *rate.Limiter
}

// Apply based on method and path
func getEndpointLimit(method, path string) *rate.Limiter {
	switch {
	case method == "POST" && strings.Contains(path, "/scans"):
		return scanLimiter
	case method == "GET":
		return readLimiter  // Higher limit for reads
	case method == "POST":
		return writeLimiter // Lower limit for writes
	default:
		return globalLimiter
	}
}
```

---

## Appendix A: Complete File Changes Summary

### Files to Create

1. **`ratelimit/limiter.go`** - NEW
   - RateLimiter struct and methods
   - Visitor tracking
   - Whitelist management
   - Cleanup goroutine

2. **`ratelimit/ip.go`** - NEW
   - GetClientIP() function
   - X-Real-IP and X-Forwarded-For handling

### Files to Modify

1. **`web/middleware.go`** - UPDATE
   - Add InitRateLimiter()
   - Add RateLimitMiddleware()
   - Add ScanRateLimitMiddleware()
   - Add handleRateLimitExceeded()
   - Add addRateLimitHeaders()

2. **`web/web_server.go`** - UPDATE
   - Call InitRateLimiter() on startup
   - Apply r.Use(RateLimitMiddleware)

3. **`web/api.go`** - UPDATE
   - Apply ScanRateLimitMiddleware to scan POST endpoint

### Files to Create (Optional)

1. **`web/admin.go`** - NEW (for whitelist management)
   - AddWhitelistHandler
   - RemoveWhitelistHandler

---

## Appendix B: Rate Limit Response Examples

### Success Response (200 OK)

```http
GET /api/scans HTTP/1.1
Host: api.example.com

HTTP/1.1 200 OK
Content-Type: application/json
X-RateLimit-Limit: 20
X-RateLimit-Remaining: 15
X-RateLimit-Reset: 1640000005

{
  "scans": [...]
}
```

### Rate Limited Response (429)

```http
POST /api/scans HTTP/1.1
Host: api.example.com

HTTP/1.1 429 Too Many Requests
Content-Type: application/json
X-RateLimit-Limit: 5
X-RateLimit-Remaining: 0
X-RateLimit-Reset: 1640000065
Retry-After: 60

{
  "error": {
    "code": "RATE_LIMIT_EXCEEDED",
    "message": "Rate limit exceeded. Please try again later.",
    "retry_after": 60,
    "limit_type": "scan"
  }
}
```

---

## Appendix C: Troubleshooting Guide

### Problem: Legitimate users getting rate limited

**Diagnosis:**
```bash
# Check logs for frequent rate limits from same IP
grep "Rate limit exceeded" /var/log/bhandaar/app.log | grep <ip> | wc -l

# Check if automated tool (monitoring, etc.)
curl -H "User-Agent: something" http://api.example.com/api/health
```

**Solution:**
- Add IP to whitelist
- Or increase global rate limit
- Or identify and fix client-side issue (retry loop, etc.)

### Problem: Rate limits not working

**Diagnosis:**
```bash
# Check if middleware applied
curl -i http://localhost:8090/api/health | grep X-RateLimit

# If no headers, middleware not applied
```

**Solution:**
- Verify InitRateLimiter() called
- Verify r.Use(RateLimitMiddleware) in web_server.go
- Check logs for initialization

### Problem: Wrong IP being rate limited

**Diagnosis:**
```bash
# Check what IP is being extracted
# Add logging in GetClientIP()

# Test with headers
curl -H "X-Real-IP: 1.2.3.4" http://localhost:8090/api/health

# Check server logs for which IP was used
```

**Solution:**
- Verify proxy headers configured correctly
- Check nginx/load balancer passing X-Real-IP or X-Forwarded-For
- Adjust IP extraction logic if needed

### Problem: Memory growth from visitor map

**Diagnosis:**
```bash
# Check visitor count (would need monitoring endpoint)
# Or check memory usage over time

# Expected: stable (cleanup working)
# Problem: growing (cleanup not working)
```

**Solution:**
- Verify cleanupStaleEntries goroutine running
- Check cleanup interval (1 minute)
- Check stale threshold (3 minutes)
- Adjust if needed

---

**END OF DOCUMENT**
