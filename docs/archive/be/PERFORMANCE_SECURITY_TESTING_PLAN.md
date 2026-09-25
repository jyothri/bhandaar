# Performance & Security Testing Plan

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P2 - Medium Priority (Quality Assurance)

---

## Executive Summary

This document outlines the performance and security testing strategy for the Bhandaar application. These tests complement the functional testing plan (TESTING_PLAN.md) and focus on non-functional requirements including performance, scalability, load handling, and security vulnerabilities.

**Scope:**
- **Performance Testing**: Benchmark tests, load tests, stress tests, endurance tests
- **Security Testing**: Vulnerability scanning, authentication/authorization testing, input validation, rate limiting verification

**Timeline:** 2-3 weeks after functional test coverage reaches 60%

**Prerequisites:**
- Functional tests passing (TESTING_PLAN.md Phase 1-5)
- Staging environment available
- Test data generation utilities

---

## Table of Contents

1. [Performance Testing](#1-performance-testing)
2. [Security Testing](#2-security-testing)
3. [Tools and Infrastructure](#3-tools-and-infrastructure)
4. [Test Scenarios](#4-test-scenarios)
5. [Metrics and Baselines](#5-metrics-and-baselines)
6. [Implementation Plan](#6-implementation-plan)

---

## 1. Performance Testing

### 1.1 Benchmark Tests

**Purpose:** Measure baseline performance of critical operations

**Tools:** `go test -bench`, `pprof`

**Benchmarks to Implement:**

```go
// db/database_benchmark_test.go
package db

import (
    "testing"
)

func BenchmarkGetOAuthToken(b *testing.B) {
    // Setup test database
    db, cleanup := setupTestDB(b)
    defer cleanup()

    // Create test token
    clientKey := "test@example.com"
    saveTestToken(db, clientKey)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := GetOAuthToken(clientKey)
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkGetOAuthTokenCached(b *testing.B) {
    // With Issue #20 cache implementation
    db, cleanup := setupTestDB(b)
    defer cleanup()

    clientKey := "test@example.com"
    saveTestToken(db, clientKey)

    // First call to populate cache
    GetOAuthToken(clientKey)

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := GetOAuthToken(clientKey)
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkLogStartScan(b *testing.B) {
    db, cleanup := setupTestDB(b)
    defer cleanup()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, err := LogStartScan("Local")
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkSaveStatToDb(b *testing.B) {
    db, cleanup := setupTestDB(b)
    defer cleanup()

    scanID, _ := LogStartScan("Local")

    // Create channel with test data
    testData := make(chan FileData, 100)
    go func() {
        for i := 0; i < b.N; i++ {
            testData <- FileData{
                FileName: fmt.Sprintf("file%d.txt", i),
                FilePath: fmt.Sprintf("/test/file%d.txt", i),
                Size:     1024,
                ModTime:  time.Now(),
            }
        }
        close(testData)
    }()

    b.ResetTimer()
    SaveStatToDb(scanID, testData)
}

func BenchmarkGetScansFromDb(b *testing.B) {
    db, cleanup := setupTestDB(b)
    defer cleanup()

    // Create 100 test scans
    for i := 0; i < 100; i++ {
        LogStartScan("Local")
    }

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        _, _, err := GetScansFromDb(1) // Page 1
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkSaveMessageMetadataToDb(b *testing.B) {
    db, cleanup := setupTestDB(b)
    defer cleanup()

    scanID, _ := LogStartScan("GMail")

    // Create channel with test messages
    messages := make(chan MessageMetadata, 100)
    go func() {
        for i := 0; i < b.N; i++ {
            messages <- MessageMetadata{
                MessageId:    fmt.Sprintf("msg%d", i),
                ThreadId:     fmt.Sprintf("thread%d", i),
                Date:         time.Now(),
                From:         "test@example.com",
                To:           "user@example.com",
                Subject:      "Test Subject",
                SizeEstimate: 1024,
            }
        }
        close(messages)
    }()

    b.ResetTimer()
    SaveMessageMetadataToDb(scanID, "test@example.com", messages)
}
```

**Target Benchmarks:**
- `GetOAuthToken`: < 5ms (with cache: < 0.1ms)
- `LogStartScan`: < 10ms
- `SaveStatToDb` (100 files): < 500ms
- `GetScansFromDb`: < 50ms
- `SaveMessageMetadataToDb` (100 messages): < 1s

### 1.2 Load Tests

**Purpose:** Verify system behavior under expected production load

**Tool:** `vegeta` (HTTP load testing)

**Scenarios:**

**Scenario 1: API Endpoint Load**
```bash
# Test health endpoint
echo "GET http://localhost:8090/api/health" | \
  vegeta attack -duration=60s -rate=50/s | \
  vegeta report

# Expected:
# - Success rate: 100%
# - Mean latency: < 100ms
# - 99th percentile: < 500ms
```

**Scenario 2: Scan Creation Load**
```bash
# Create scan_request.json
cat > scan_request.json << EOF
{
  "ScanType": "Local",
  "LocalScan": {
    "Path": "/tmp/test"
  }
}
EOF

# Test scan creation endpoint
echo "POST http://localhost:8090/api/scans" | \
  vegeta attack -duration=60s -rate=10/s \
    -header "Content-Type: application/json" \
    -body scan_request.json | \
  vegeta report

# Expected (with Issue #15 rate limiting):
# - Success rate: ~50% (5 req/min limit)
# - 429 responses: ~50%
# - Mean latency for 200s: < 200ms
```

**Scenario 3: Concurrent Scan Data Fetches**
```bash
# Test GET /api/scans endpoint with multiple concurrent requests
echo "GET http://localhost:8090/api/scans?page=1" | \
  vegeta attack -duration=30s -rate=100/s | \
  vegeta report

# Expected:
# - Success rate: > 95%
# - Mean latency: < 150ms
# - Database connection pool utilization: < 80%
```

**Scenario 4: SSE Connection Load**
```bash
# Test SSE endpoint with 50 concurrent connections
for i in {1..50}; do
  curl -N http://localhost:8090/sse/scanprogress &
done

# Monitor:
# - Memory usage (should not grow unbounded)
# - Goroutine count (should stabilize)
# - CPU usage (should remain < 50%)

# Cleanup
pkill -f "curl.*sse/scanprogress"
```

### 1.3 Stress Tests

**Purpose:** Find breaking points and resource limits

**Scenarios:**

**Scenario 1: Database Connection Exhaustion**
```bash
# Attempt to exceed max connection pool (Issue #17: 10 connections)
# Make 20 concurrent slow queries
for i in {1..20}; do
  curl "http://localhost:8090/api/scans" &
done

# Expected (with Issue #17 connection pooling):
# - First 10 succeed immediately
# - Next 10 queue and succeed when connections available
# - No "too many clients" errors
# - Wait count in pool stats increases
```

**Scenario 2: Rate Limit Bypass Attempt**
```bash
# Attempt to exceed rate limits (Issue #15)
for i in {1..100}; do
  curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8090/api/health
done

# Expected:
# - First 20 succeed (burst capacity)
# - Next 80 get 429 Too Many Requests
# - Rate limiter recovers after wait period
```

**Scenario 3: Large Scan Processing**
```bash
# Test with directory containing 10,000 files
mkdir -p /tmp/stress_test
for i in {1..10000}; do
  touch /tmp/stress_test/file_$i.txt
done

curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/stress_test"}}'

# Monitor:
# - Memory usage (should not exceed 500MB)
# - Scan completion time (should complete within 2 minutes)
# - Database insert throughput
# - No goroutine leaks
```

### 1.4 Endurance Tests

**Purpose:** Verify stability over extended periods

**Scenario: 24-Hour Stability Test**
```bash
# Run sustained load for 24 hours
echo "GET http://localhost:8090/api/health" | \
  vegeta attack -duration=24h -rate=10/s | \
  vegeta report > endurance_report.txt

# Monitor throughout:
# - Memory usage (should not grow continuously)
# - CPU usage (should remain stable)
# - Database connection leaks (pool stats)
# - Goroutine leaks (runtime.NumGoroutine())
# - Error rates (should remain < 0.1%)

# Expected results:
# - No crashes or panics
# - Memory stable (< 200MB)
# - Response times consistent
# - No resource leaks
```

---

## 2. Security Testing

### 2.1 Authentication & Authorization Testing

**After Issue #3 (Authentication) is implemented:**

```go
// auth/auth_test.go - Security tests
package auth

import (
    "net/http"
    "net/http/httptest"
    "testing"
)

func TestUnauthorizedAccess(t *testing.T) {
    // Test accessing protected endpoint without authentication
    req := httptest.NewRequest("GET", "/api/scans", nil)
    w := httptest.NewRecorder()

    handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    handler.ServeHTTP(w, req)

    // Should return 401 Unauthorized
    if w.Code != http.StatusUnauthorized {
        t.Errorf("Expected 401, got %d", w.Code)
    }
}

func TestExpiredToken(t *testing.T) {
    // Test with expired JWT token
    expiredToken := generateExpiredToken()

    req := httptest.NewRequest("GET", "/api/scans", nil)
    req.Header.Set("Authorization", "Bearer "+expiredToken)
    w := httptest.NewRecorder()

    handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    handler.ServeHTTP(w, req)

    // Should return 401 Unauthorized
    if w.Code != http.StatusUnauthorized {
        t.Errorf("Expected 401 for expired token, got %d", w.Code)
    }
}

func TestTokenForging(t *testing.T) {
    // Attempt to forge a JWT token with wrong signature
    forgedToken := generateForgedToken()

    req := httptest.NewRequest("GET", "/api/scans", nil)
    req.Header.Set("Authorization", "Bearer "+forgedToken)
    w := httptest.NewRecorder()

    handler := AuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    handler.ServeHTTP(w, req)

    // Should return 401 Unauthorized
    if w.Code != http.StatusUnauthorized {
        t.Errorf("Expected 401 for forged token, got %d", w.Code)
    }
}

func TestAccessOtherUserData(t *testing.T) {
    // User A tries to access User B's scan
    userAToken := generateValidToken("userA@example.com")
    userBScanID := 123 // Created by User B

    req := httptest.NewRequest("GET", fmt.Sprintf("/api/scans/%d", userBScanID), nil)
    req.Header.Set("Authorization", "Bearer "+userAToken)
    w := httptest.NewRecorder()

    // Should return 403 Forbidden (not authorized to access other user's data)
    if w.Code != http.StatusForbidden {
        t.Errorf("Expected 403, got %d", w.Code)
    }
}
```

### 2.2 Input Validation Testing

**SQL Injection Testing:**
```go
func TestSQLInjectionInScanPath(t *testing.T) {
    maliciousPath := "'; DROP TABLE scans; --"

    req := httptest.NewRequest("POST", "/api/scans", strings.NewReader(fmt.Sprintf(`{
        "ScanType": "Local",
        "LocalScan": {
            "Path": "%s"
        }
    }`, maliciousPath)))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    handler := http.HandlerFunc(DoScansHandler)
    handler.ServeHTTP(w, req)

    // Verify table still exists
    var count int
    err := db.Get(&count, "SELECT COUNT(*) FROM scans")
    if err != nil {
        t.Fatal("SQL injection successful - table was dropped!")
    }
}

func TestSQLInjectionInSearchFilter(t *testing.T) {
    maliciousFilter := "1' OR '1'='1"

    // Should not return all records - parameterized queries prevent this
    scans, err := GetScansFromDb(1)
    // Verify safe behavior
}
```

**XSS Testing (Frontend integration):**
```go
func TestXSSInScanName(t *testing.T) {
    xssPayload := "<script>alert('XSS')</script>"

    // Create scan with XSS payload in metadata
    scanID, _ := LogStartScan("Local")
    SaveScanMetadata(xssPayload, "/test", "", scanID)

    // Fetch scan data
    scan, _ := GetScanById(scanID)

    // Verify payload is escaped/sanitized when returned
    // Frontend should not execute the script
    if strings.Contains(scan.Metadata, "<script>") {
        t.Error("XSS payload not sanitized")
    }
}
```

**Path Traversal Testing:**
```go
func TestPathTraversalInLocalScan(t *testing.T) {
    // Attempt to scan outside allowed directory
    maliciousPath := "../../../../etc/passwd"

    req := httptest.NewRequest("POST", "/api/scans", strings.NewReader(fmt.Sprintf(`{
        "ScanType": "Local",
        "LocalScan": {
            "Path": "%s"
        }
    }`, maliciousPath)))
    req.Header.Set("Content-Type", "application/json")
    w := httptest.NewRecorder()

    handler := http.HandlerFunc(DoScansHandler)
    handler.ServeHTTP(w, req)

    // Should reject or sanitize the path
    // Verify no sensitive files were accessed
}
```

### 2.3 Rate Limiting Verification (Issue #15)

```go
func TestGlobalRateLimit(t *testing.T) {
    // Verify global rate limit (10 req/sec, burst 20)
    client := &http.Client{}
    successCount := 0
    rateLimitedCount := 0

    // Make 30 requests rapidly (exceeds burst of 20)
    for i := 0; i < 30; i++ {
        resp, err := client.Get("http://localhost:8090/api/health")
        if err != nil {
            t.Fatal(err)
        }

        if resp.StatusCode == http.StatusOK {
            successCount++
        } else if resp.StatusCode == http.StatusTooManyRequests {
            rateLimitedCount++
        }

        resp.Body.Close()
    }

    // Should see ~20 successful, ~10 rate limited
    if successCount < 15 || successCount > 25 {
        t.Errorf("Expected ~20 successful requests, got %d", successCount)
    }
    if rateLimitedCount < 5 || rateLimitedCount > 15 {
        t.Errorf("Expected ~10 rate limited requests, got %d", rateLimitedCount)
    }

    // Verify rate limit headers present
    resp, _ := client.Get("http://localhost:8090/api/health")
    if resp.Header.Get("X-RateLimit-Limit") == "" {
        t.Error("Missing X-RateLimit-Limit header")
    }
}

func TestScanRateLimit(t *testing.T) {
    // Verify scan-specific rate limit (5 req/min)
    client := &http.Client{}
    successCount := 0

    // Attempt to create 10 scans (exceeds limit of 5/min)
    for i := 0; i < 10; i++ {
        req, _ := http.NewRequest("POST", "http://localhost:8090/api/scans", strings.NewReader(`{
            "ScanType": "Local",
            "LocalScan": {"Path": "/tmp"}
        }`))
        req.Header.Set("Content-Type", "application/json")

        resp, err := client.Do(req)
        if err != nil {
            t.Fatal(err)
        }

        if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
            successCount++
        }

        resp.Body.Close()
        time.Sleep(1 * time.Second)
    }

    // Should only succeed ~5 times
    if successCount > 6 {
        t.Errorf("Rate limit not enforced - expected max 5 scans, got %d", successCount)
    }
}
```

### 2.4 OAuth Flow Security

```go
func TestOAuthStateValidation(t *testing.T) {
    // Verify state parameter prevents CSRF
    validState := generateValidState()
    invalidState := "invalid_state"

    // Request with invalid state should be rejected
    req := httptest.NewRequest("GET",
        fmt.Sprintf("/oauth/callback?state=%s&code=test_code", invalidState),
        nil)
    w := httptest.NewRecorder()

    handler := http.HandlerFunc(OAuthCallbackHandler)
    handler.ServeHTTP(w, req)

    // Should return error (not proceed with token exchange)
    if w.Code == http.StatusOK {
        t.Error("OAuth callback accepted invalid state parameter")
    }
}

func TestTokenStorage(t *testing.T) {
    // Verify tokens are encrypted/hashed before storage
    clientKey := "test@example.com"
    refreshToken := "sensitive_refresh_token"

    SaveOAuthToken("access", refreshToken, "Test User", clientKey, "scope", 3600, "Bearer")

    // Query database directly
    var storedToken string
    db.Get(&storedToken, "SELECT refresh_token FROM privatetokens WHERE client_key = $1", clientKey)

    // Token should not be stored in plain text
    // (This assumes encryption is implemented - may not be current behavior)
    if storedToken == refreshToken {
        t.Log("WARNING: Tokens stored in plain text - consider encryption")
    }
}
```

### 2.5 CORS Security

```go
func TestCORSConfiguration(t *testing.T) {
    // Verify CORS only allows whitelisted origins
    allowedOrigin := "https://bhandaar.example.com"
    maliciousOrigin := "https://evil.com"

    // Request from allowed origin
    req := httptest.NewRequest("GET", "/api/health", nil)
    req.Header.Set("Origin", allowedOrigin)
    w := httptest.NewRecorder()

    handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    }))

    handler.ServeHTTP(w, req)

    if w.Header().Get("Access-Control-Allow-Origin") != allowedOrigin {
        t.Error("CORS not allowing whitelisted origin")
    }

    // Request from malicious origin
    req = httptest.NewRequest("GET", "/api/health", nil)
    req.Header.Set("Origin", maliciousOrigin)
    w = httptest.NewRecorder()

    handler.ServeHTTP(w, req)

    if w.Header().Get("Access-Control-Allow-Origin") == maliciousOrigin {
        t.Error("CORS allowing non-whitelisted origin")
    }
}
```

---

## 3. Tools and Infrastructure

### 3.1 Performance Testing Tools

| Tool | Purpose | Installation |
|------|---------|--------------|
| **go test -bench** | Microbenchmarks | Built-in |
| **pprof** | CPU/memory profiling | Built-in (`import _ "net/http/pprof"`) |
| **vegeta** | HTTP load testing | `go install github.com/tsenart/vegeta@latest` |
| **hey** | Alternative load tester | `go install github.com/rakyll/hey@latest` |
| **wrk** | HTTP benchmarking | `brew install wrk` (macOS) |

### 3.2 Security Testing Tools

| Tool | Purpose | Installation |
|------|---------|--------------|
| **gosec** | Static analysis security scanner | `go install github.com/securego/gosec/v2/cmd/gosec@latest` |
| **govulncheck** | Go vulnerability checker | `go install golang.org/x/vuln/cmd/govulncheck@latest` |
| **OWASP ZAP** | Web app security scanner | Download from owasp.org |
| **sqlmap** | SQL injection testing | `pip install sqlmap` |

### 3.3 Monitoring and Profiling

**pprof Setup:**
```go
// main.go
import _ "net/http/pprof"

func main() {
    // ... existing setup ...

    // Start pprof server on separate port
    go func() {
        log.Println(http.ListenAndServe("localhost:6060", nil))
    }()

    // ... rest of main ...
}
```

**Usage:**
```bash
# CPU profiling
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Memory profiling
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutine profiling
go tool pprof http://localhost:6060/debug/pprof/goroutine

# View in browser
go tool pprof -http=:8081 http://localhost:6060/debug/pprof/profile
```

---

## 4. Test Scenarios

### 4.1 Realistic User Scenarios

**Scenario 1: Normal User Activity**
```
Timeline:
1. User logs in via OAuth (1 req)
2. User views scan list (1 req)
3. User creates new Gmail scan (1 req + SSE connection)
4. Scan completes over 2 minutes (500 progress events)
5. User views scan results (1 req)
6. User creates another scan (1 req)

Total: 5 HTTP requests + 1 SSE connection + 500 events
Expected duration: 3-4 minutes
Success rate: 100%
```

**Scenario 2: Multi-User Concurrent Activity**
```
Setup: 10 concurrent users
Each user:
- Creates 1 Gmail scan
- Creates 1 Photos scan
- Views scan results

Total: 20 scans + 20 result fetches
Expected duration: 5-10 minutes
Success rate: > 95%
Database connections: Peak 8-10
```

**Scenario 3: Burst Traffic**
```
Setup: 100 users access site simultaneously (e.g., after email notification)
Actions:
- 100 health checks
- 50 scan list views
- 10 new scans

Expected:
- Some rate limiting (429 responses)
- All critical operations succeed
- No service degradation
- Response times < 500ms (95th percentile)
```

### 4.2 Edge Cases

**Scenario 1: Empty Directory Scan**
```bash
mkdir /tmp/empty_scan
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/empty_scan"}}'

# Expected:
# - Completes quickly (< 1 second)
# - No errors
# - Scan marked as completed
# - Zero files returned
```

**Scenario 2: Extremely Large File**
```bash
# Create 10GB file
dd if=/dev/zero of=/tmp/large_file.bin bs=1G count=10

# Scan directory
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp"}}'

# Expected:
# - Scan completes (may take longer)
# - Memory usage stable (don't load entire file)
# - File size recorded correctly
# - No out-of-memory errors
```

**Scenario 3: Deeply Nested Directory**
```bash
# Create 100-level deep directory structure
mkdir -p /tmp/deep/{1..100}

# Expected:
# - No stack overflow
# - Completes successfully
# - All directories recorded
```

---

## 5. Metrics and Baselines

### 5.1 Performance Baselines

| Operation | Target | Acceptable | Unacceptable |
|-----------|--------|------------|--------------|
| Health check response | < 10ms | < 50ms | > 100ms |
| Scan creation (API) | < 100ms | < 200ms | > 500ms |
| Scan list fetch (10 items) | < 50ms | < 100ms | > 200ms |
| OAuth token fetch (cached) | < 1ms | < 5ms | > 10ms |
| OAuth token fetch (uncached) | < 10ms | < 50ms | > 100ms |
| Local scan (100 files) | < 5s | < 10s | > 30s |
| Database insert (1 row) | < 5ms | < 10ms | > 50ms |
| SSE event broadcast | < 10ms | < 50ms | > 100ms |

### 5.2 Resource Baselines

| Resource | Target | Acceptable | Unacceptable |
|----------|--------|------------|--------------|
| Memory usage (idle) | < 50MB | < 100MB | > 200MB |
| Memory usage (10 scans) | < 150MB | < 300MB | > 500MB |
| CPU usage (idle) | < 1% | < 5% | > 10% |
| CPU usage (scanning) | < 50% | < 80% | > 95% |
| Database connections | < 5 | < 8 | > 10 |
| Goroutines (idle) | < 20 | < 50 | > 100 |
| Goroutines (10 scans) | < 100 | < 200 | > 500 |

### 5.3 Security Baselines

| Check | Requirement | Status |
|-------|-------------|--------|
| gosec score | 0 high/critical issues | Pending |
| govulncheck | No known vulnerabilities | Pending |
| OWASP Top 10 | No critical issues | Pending |
| Rate limiting | Enforced on all endpoints | Implemented (Issue #15) |
| Input validation | All user inputs validated | Pending |
| SQL injection | All queries parameterized | Implemented |
| XSS protection | All outputs escaped | Pending |
| CORS | Whitelisted origins only | Implemented |
| Authentication | Required for all protected endpoints | Pending (Issue #3) |
| Authorization | Users can only access own data | Pending (Issue #3) |

---

## 6. Implementation Plan

### Phase 1: Setup (Week 1)

**Tasks:**
- [ ] Install performance testing tools (vegeta, pprof)
- [ ] Install security testing tools (gosec, govulncheck)
- [ ] Setup pprof endpoint in main.go
- [ ] Create performance test suite structure
- [ ] Create security test suite structure
- [ ] Document baseline metrics

**Deliverables:**
- Performance testing infrastructure ready
- Security scanning tools configured
- Baseline metrics documented

### Phase 2: Benchmark Tests (Week 1-2)

**Tasks:**
- [ ] Implement database operation benchmarks
- [ ] Implement API endpoint benchmarks
- [ ] Implement cache performance benchmarks (Issue #20)
- [ ] Run benchmarks and establish baselines
- [ ] Profile CPU and memory usage
- [ ] Document benchmark results

**Deliverables:**
- `*_benchmark_test.go` files in relevant packages
- Benchmark baseline report
- CPU/memory profiles

### Phase 3: Load Tests (Week 2)

**Tasks:**
- [ ] Create vegeta test scenarios
- [ ] Run load tests against staging environment
- [ ] Measure throughput and latency under load
- [ ] Identify bottlenecks
- [ ] Document load test results

**Deliverables:**
- Load test scripts
- Load test reports
- Bottleneck analysis

### Phase 4: Security Tests (Week 2-3)

**Tasks:**
- [ ] Run gosec static analysis
- [ ] Run govulncheck for dependency vulnerabilities
- [ ] Implement authentication/authorization tests
- [ ] Implement input validation tests
- [ ] Implement rate limiting verification tests
- [ ] Run OWASP ZAP scan (optional)
- [ ] Document security findings

**Deliverables:**
- Security test suite
- gosec report
- govulncheck report
- Security findings and remediation plan

### Phase 5: Stress & Endurance Tests (Week 3)

**Tasks:**
- [ ] Run stress tests to find breaking points
- [ ] Run 24-hour endurance test
- [ ] Monitor for memory leaks
- [ ] Monitor for goroutine leaks
- [ ] Document stability results

**Deliverables:**
- Stress test results
- Endurance test report
- Stability metrics

### Phase 6: Reporting & Remediation (Week 3)

**Tasks:**
- [ ] Compile comprehensive performance report
- [ ] Compile comprehensive security report
- [ ] Prioritize issues found
- [ ] Create remediation plan
- [ ] Update performance baselines

**Deliverables:**
- **Performance Testing Report**
- **Security Testing Report**
- **Remediation Plan**
- **Updated baselines**

---

## Success Criteria

**Performance:**
- ✅ All benchmarks meet target baselines
- ✅ Load tests show acceptable performance under expected traffic
- ✅ No memory leaks in 24-hour endurance test
- ✅ Resource usage within acceptable limits
- ✅ Identified and documented all bottlenecks

**Security:**
- ✅ Zero high/critical security issues from gosec
- ✅ Zero known vulnerabilities from govulncheck
- ✅ All input validation tests pass
- ✅ Rate limiting verified and working
- ✅ No SQL injection vulnerabilities
- ✅ Authentication/authorization working (after Issue #3)

---

## Appendix A: Test Data Generation

**Helper scripts for generating test data:**

```bash
# create_test_files.sh
#!/bin/bash
# Creates test files for local scan performance testing

BASE_DIR="/tmp/performance_test"
FILE_COUNT=$1

mkdir -p $BASE_DIR

for i in $(seq 1 $FILE_COUNT); do
  echo "Test file $i" > "$BASE_DIR/file_$i.txt"
done

echo "Created $FILE_COUNT test files in $BASE_DIR"
```

```bash
# create_nested_structure.sh
#!/bin/bash
# Creates nested directory structure for edge case testing

BASE_DIR="/tmp/nested_test"
DEPTH=$1

current_dir=$BASE_DIR
for i in $(seq 1 $DEPTH); do
  mkdir -p "$current_dir/level_$i"
  current_dir="$current_dir/level_$i"
done

echo "Created $DEPTH-level nested structure in $BASE_DIR"
```

---

## Appendix B: Monitoring Dashboard

**Recommended metrics to monitor during performance/security testing:**

```
Performance Metrics Dashboard:
- HTTP Request Rate (req/s)
- HTTP Response Time (p50, p95, p99)
- HTTP Error Rate (%)
- Database Query Time (ms)
- Database Connection Pool (open, idle, in-use)
- Memory Usage (MB)
- CPU Usage (%)
- Goroutine Count
- SSE Connection Count

Security Metrics Dashboard:
- Rate Limit Violations (count)
- Failed Authentication Attempts (count)
- Unauthorized Access Attempts (count)
- Input Validation Failures (count)
- Suspicious Activity Alerts
```

---

**END OF DOCUMENT**
