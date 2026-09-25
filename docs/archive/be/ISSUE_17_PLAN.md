# Issue #17 Implementation Plan: No Database Connection Lifecycle Management

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P2 - Medium Priority (Resource Management & Reliability)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #17: No Database Connection Lifecycle Management**. The current system opens a database connection but never explicitly manages its lifecycle, has no connection pool configuration, and lacks proper shutdown handling.

**Selected Approach:**
- **Connection Pool**: Conservative defaults (MaxOpen: 10, MaxIdle: 5, MaxLifetime: 5min, MaxIdleTime: 10min)
- **Close Function**: Enhanced Close() with graceful timeout and active connection waiting
- **Database Structure**: Keep global `db *sqlx.DB`, add configuration and Close() methods
- **Health Check**: Comprehensive with connection stats (open, idle, in-use counts)
- **Validation**: On startup only (fail-fast if DB unavailable)
- **Shutdown Integration**: Close database in parallel with HTTP shutdown (Issue #8)
- **Leak Detection**: Log connection stats only on shutdown
- **Breaking Changes**: Minimal - maintain backward compatibility where possible
- **Query Timeout**: 30-second default timeout for all database operations
- **Prepared Statements**: Kept separate - addressed in Issue #18

**Estimated Effort:** 4-6 hours

**Impact:**
- Prevents connection leaks
- Enables graceful shutdown
- Improves connection pool efficiency
- Provides visibility into database health
- Prevents hanging queries
- Better resource utilization

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Implementation Details](#3-implementation-details)
4. [Testing Strategy](#4-testing-strategy)
5. [Deployment Plan](#5-deployment-plan)
6. [Integration with Issue #8](#6-integration-with-issue-8)

---

## 1. Current State Analysis

### 1.1 Current Implementation

**db/database.go (after Issue #1 fix):**
```go
package db

import (
	"fmt"
	"log/slog"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const (
	host     = "hdd_db"
	port     = 5432
	user     = "hddb"
	password = "hddb"
	dbname   = "hdd_db"
)

var db *sqlx.DB  // ❌ Global variable, never closed

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

	// ❌ No connection pool configuration!
	// db.SetMaxOpenConns() - not called
	// db.SetMaxIdleConns() - not called
	// db.SetConnMaxLifetime() - not called

	slog.Info("Successfully connected to database")

	if err := migrateDB(); err != nil {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	return nil
}

// ❌ No Close() function!
```

**main.go (after Issue #1 fix):**
```go
func main() {
	// ... logger setup ...

	// Initialize database connection
	if err := db.SetupDatabase(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}

	// ❌ No defer db.Close() - connection never closed!

	slog.Info("Starting web server")
	web.Server()  // Blocks forever
}
```

### 1.2 Current Problems

**Problem 1: No Connection Pool Configuration**
```go
// With default settings:
// - MaxOpenConns = unlimited (can exhaust database server)
// - MaxIdleConns = 2 (too low, causes frequent reconnects)
// - ConnMaxLifetime = 0 (connections never recycled)
// - ConnMaxIdleTime = 0 (idle connections never closed)
```

**Impact:**
- Under high load, may open hundreds of connections
- Database server may reject new connections
- Stale connections accumulate
- Inefficient connection reuse

**Problem 2: No Explicit Close**
```go
var db *sqlx.DB
// Connection opened on startup
// Never closed, even on shutdown
```

**Impact:**
- Database sees connections as "active" even after process exits
- Ungraceful disconnection may cause database warnings
- Can't perform cleanup operations
- No visibility into shutdown process

**Problem 3: No Connection Health Monitoring**
```go
// Can't answer:
// - How many connections are open?
// - How many are idle?
// - Are we approaching connection limits?
// - Is the database still reachable?
```

**Impact:**
- Can't detect connection leaks
- No visibility for debugging
- Hard to tune pool settings
- No health endpoint insight

**Problem 4: No Query Timeouts**
```go
// All queries can run indefinitely
db.Exec(query, args...)  // No context, no timeout
```

**Impact:**
- Slow queries can block indefinitely
- No way to cancel long-running operations
- Cascading failures under load
- Resource exhaustion

### 1.3 Resource Impact

**Connection Leak Scenario:**
```
Assumptions:
- Each connection uses ~1 MB memory on database server
- PostgreSQL default max_connections = 100

Timeline:
1. Application starts: 2 connections (initial + 1 query)
2. After 100 requests: 10 connections (normal usage)
3. After 1000 requests: 50 connections (some leaked)
4. After 10000 requests: 95 connections (approaching limit)
5. Next request: "too many clients" error - service degraded

Without connection pooling:
- Can exhaust database connection limit
- Other services can't connect
- Cascading failure
```

**Memory Impact on App Server:**
```
Per sqlx.DB connection overhead:
- Connection state: ~32 KB
- Read/write buffers: ~64 KB
- Total per connection: ~100 KB

With 50 leaked connections:
- 50 × 100 KB = 5 MB
- Plus PostgreSQL driver overhead
- Total: ~10 MB leaked

Not critical for memory, but critical for database server.
```

### 1.4 Current vs Target Behavior

| Aspect | Current | Target |
|--------|---------|--------|
| **Connection pool** | Default (unlimited) | Configured (10 max, 5 idle) |
| **Connection lifecycle** | Never closed | Gracefully closed on shutdown |
| **Connection recycling** | Never (stale forever) | Every 5 minutes max |
| **Idle connection cleanup** | Never | After 10 minutes idle |
| **Health check** | Basic ping only | Stats + ping |
| **Query timeout** | None (infinite) | 30 seconds default |
| **Shutdown** | Abrupt | Graceful with 15s timeout |
| **Monitoring** | None | Log stats on shutdown |

---

## 2. Target Architecture

### 2.1 Database Lifecycle Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Application Startup                                      │
│    - Load configuration from environment/constants          │
│    - Create connection string                               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. SetupDatabase()                                          │
│    - sqlx.Open() to create connection pool                 │
│    - db.Ping() to validate connectivity                    │
│    - Configure connection pool:                             │
│      • SetMaxOpenConns(10)                                 │
│      • SetMaxIdleConns(5)                                  │
│      • SetConnMaxLifetime(5 min)                           │
│      • SetConnMaxIdleTime(10 min)                          │
│    - Run migrations                                         │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Normal Operations                                        │
│    - Queries use 30-second context timeout                 │
│    - Connection pool manages lifecycle automatically       │
│    - Idle connections cleaned up after 10 min              │
│    - Connections recycled after 5 min                       │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Health Check (Optional)                                  │
│    - GET /health endpoint calls GetDatabaseHealth()        │
│    - Returns: ok, open_connections, idle, in_use           │
│    - Performs db.Ping() to verify connectivity             │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Graceful Shutdown (SIGTERM/SIGINT)                      │
│    - Main goroutine calls CloseDatabase()                  │
│    - In parallel with HTTP server shutdown (Issue #8)      │
│    - Wait up to 15 seconds for active queries              │
│    - Log final connection stats                            │
│    - Call db.Close()                                        │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Connection Pool Architecture

**Conservative Pool Settings:**
```
MaxOpenConns: 10
├─ Maximum total connections (active + idle)
├─ Prevents database server overload
└─ Suitable for single-instance deployment

MaxIdleConns: 5
├─ Keeps connections warm for quick reuse
├─ Reduces latency from connection establishment
└─ Balances resource usage vs performance

ConnMaxLifetime: 5 minutes
├─ Closes connections after 5 minutes regardless of use
├─ Prevents stale connections
├─ Helps with database load balancing
└─ Good for cloud databases with auto-scaling

ConnMaxIdleTime: 10 minutes
├─ Closes idle connections after 10 minutes
├─ Frees resources during low traffic
└─ Automatically scales down

Timeline Example:
Time    | Open | Idle | In-Use | Event
--------|------|------|--------|------------------
0:00    | 1    | 1    | 0      | Startup, 1 conn created
0:01    | 3    | 0    | 3      | 3 concurrent queries
0:02    | 3    | 3    | 0      | Queries done, connections idle
5:00    | 2    | 2    | 0      | 1 conn recycled (MaxLifetime)
10:00   | 2    | 0    | 2      | 2 queries using warm connections
10:12   | 0    | 0    | 0      | All idle conns closed (MaxIdleTime)
10:13   | 1    | 0    | 1      | New query creates new conn
```

### 2.3 Query Timeout Strategy

**Default 30-Second Timeout:**
```go
// All database operations use context with timeout
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := db.ExecContext(ctx, query, args...)
if err != nil {
    if errors.Is(err, context.DeadlineExceeded) {
        // Query took longer than 30 seconds
        return fmt.Errorf("query timeout: %w", err)
    }
    return err
}
```

**Timeout Selection Rationale:**
```
30 seconds is appropriate because:
✅ Long enough for complex scans to write batches
✅ Short enough to prevent indefinite blocking
✅ Prevents cascade failures under load
✅ Aligns with typical HTTP request timeouts

Query Type               Expected Duration    30s Adequate?
-----------------------------------------------------------------
INSERT single row        < 10 ms              ✅ Yes
INSERT batch (100 rows)  < 100 ms             ✅ Yes
SELECT scan by ID        < 50 ms              ✅ Yes
SELECT scan data (1000)  < 500 ms             ✅ Yes
DELETE cascade (7 tables)< 2 seconds          ✅ Yes
Table migration          < 5 seconds          ✅ Yes

Only pathological cases would exceed 30s:
❌ Full table scan on millions of rows (use pagination)
❌ Deadlock or lock contention (database issue)
❌ Network partition (infrastructure issue)
```

### 2.4 Health Check Response Format

**GET /health response:**
```json
{
  "status": "healthy",
  "database": {
    "connected": true,
    "ping_ms": 5,
    "stats": {
      "max_open_connections": 10,
      "open_connections": 3,
      "in_use": 1,
      "idle": 2,
      "wait_count": 0,
      "wait_duration_ms": 0,
      "max_idle_closed": 5,
      "max_idle_time_closed": 12,
      "max_lifetime_closed": 3
    }
  },
  "timestamp": "2025-12-21T10:30:00Z"
}
```

**GET /health response (unhealthy):**
```json
{
  "status": "unhealthy",
  "database": {
    "connected": false,
    "error": "failed to ping database: connection refused",
    "stats": null
  },
  "timestamp": "2025-12-21T10:30:00Z"
}
```

---

## 3. Implementation Details

### 3.1 Enhanced Database Setup: `db/database.go`

**Update SetupDatabase() with connection pool configuration:**

```go
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const (
	host     = "hdd_db"
	port     = 5432
	user     = "hddb"
	password = "hddb"
	dbname   = "hdd_db"
)

// Connection pool configuration constants
const (
	maxOpenConns    = 10               // Maximum total connections
	maxIdleConns    = 5                // Maximum idle connections
	connMaxLifetime = 5 * time.Minute  // Recycle connections after 5 min
	connMaxIdleTime = 10 * time.Minute // Close idle connections after 10 min
)

var db *sqlx.DB

// SetupDatabase initializes the database connection with proper pool configuration
func SetupDatabase() error {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	var err error
	db, err = sqlx.Open("postgres", psqlInfo)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool for optimal resource usage
	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)
	db.SetConnMaxIdleTime(connMaxIdleTime)

	slog.Info("Database connection pool configured",
		"max_open", maxOpenConns,
		"max_idle", maxIdleConns,
		"max_lifetime", connMaxLifetime,
		"max_idle_time", connMaxIdleTime)

	// Validate database connectivity with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Successfully connected to database")

	// Run migrations
	if err := migrateDB(); err != nil {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	return nil
}

// CloseDatabase gracefully closes the database connection
// Waits for active queries to complete (up to timeout)
func CloseDatabase() error {
	if db == nil {
		slog.Warn("CloseDatabase called but db is nil")
		return nil
	}

	slog.Info("Closing database connection...")

	// Log final connection stats before closing
	stats := db.Stats()
	slog.Info("Database connection stats at shutdown",
		"open_connections", stats.OpenConnections,
		"in_use", stats.InUse,
		"idle", stats.Idle,
		"wait_count", stats.WaitCount,
		"wait_duration_ms", stats.WaitDuration.Milliseconds(),
		"max_idle_closed", stats.MaxIdleClosed,
		"max_idle_time_closed", stats.MaxIdleTimeClosed,
		"max_lifetime_closed", stats.MaxLifetimeClosed,
	)

	// Create context with timeout for graceful close
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Channel to signal completion
	done := make(chan error, 1)

	// Close database in goroutine
	go func() {
		done <- db.Close()
	}()

	// Wait for close to complete or timeout
	select {
	case err := <-done:
		if err != nil {
			slog.Error("Error closing database", "error", err)
			return fmt.Errorf("failed to close database: %w", err)
		}
		slog.Info("Database connection closed successfully")
		return nil

	case <-ctx.Done():
		slog.Warn("Database close timed out after 15 seconds, forcing close")
		// db.Close() is still running, but we give up waiting
		return fmt.Errorf("database close timeout: %w", ctx.Err())
	}
}

// GetDatabaseHealth returns database health information
// Used by health check endpoints
func GetDatabaseHealth() (map[string]interface{}, error) {
	if db == nil {
		return map[string]interface{}{
			"connected": false,
			"error":     "database not initialized",
		}, fmt.Errorf("database not initialized")
	}

	// Ping database to verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pingStart := time.Now()
	err := db.PingContext(ctx)
	pingDuration := time.Since(pingStart)

	if err != nil {
		return map[string]interface{}{
			"connected": false,
			"error":     err.Error(),
			"stats":     nil,
		}, fmt.Errorf("database ping failed: %w", err)
	}

	// Get connection pool stats
	stats := db.Stats()

	return map[string]interface{}{
		"connected": true,
		"ping_ms":   pingDuration.Milliseconds(),
		"stats": map[string]interface{}{
			"max_open_connections":  maxOpenConns,
			"open_connections":      stats.OpenConnections,
			"in_use":                stats.InUse,
			"idle":                  stats.Idle,
			"wait_count":            stats.WaitCount,
			"wait_duration_ms":      stats.WaitDuration.Milliseconds(),
			"max_idle_closed":       stats.MaxIdleClosed,
			"max_idle_time_closed":  stats.MaxIdleTimeClosed,
			"max_lifetime_closed":   stats.MaxLifetimeClosed,
		},
	}, nil
}

// Helper function to get default context with timeout for queries
// This is used internally by database functions
func getDefaultContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
```

### 3.2 Update Database Functions with Context

**Add context to critical database operations:**

```go
// Example: Update CreateScan to use context
func CreateScan(scanType, path, refreshToken string) (int, error) {
	ctx, cancel := getDefaultContext()
	defer cancel()

	insert_row := `insert into scans (scan_type, path, refresh_token, created_on, scan_start_time, status)
	               values ($1, $2, $3, current_timestamp, current_timestamp, 'Pending') RETURNING id`

	lastInsertId := 0
	err := db.QueryRowContext(ctx, insert_row, scanType, path, refreshToken).Scan(&lastInsertId)
	if err != nil {
		return 0, fmt.Errorf("failed to create scan: %w", err)
	}

	return lastInsertId, nil
}

// Example: Update InsertScanData to use context
func InsertScanData(scanId int, size int64, name, path string, isFolder bool, md5sum string, modTime string) error {
	ctx, cancel := getDefaultContext()
	defer cancel()

	query := `INSERT INTO scandata (scan_id, size, name, path, is_folder, md5sum, mod_time)
	          VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := db.ExecContext(ctx, query, scanId, size, name, path, isFolder, md5sum, modTime)
	if err != nil {
		return fmt.Errorf("failed to insert scan data: %w", err)
	}

	return nil
}

// Example: Update GetScanData to use context
func GetScanData(scanId int) ([]ScanDataItem, error) {
	ctx, cancel := getDefaultContext()
	defer cancel()

	query := `SELECT size, name, path, is_folder, md5sum, mod_time
	          FROM scandata
	          WHERE scan_id = $1
	          ORDER BY path`

	var items []ScanDataItem
	err := db.SelectContext(ctx, &items, query, scanId)
	if err != nil {
		return nil, fmt.Errorf("failed to get scan data: %w", err)
	}

	return items, nil
}

// Update ALL database functions similarly:
// - LogStartScan()
// - MarkScanCompleted()
// - MarkScanFailed()
// - SaveOAuthToken()
// - GetRefreshToken()
// - ListScans()
// - DeleteScan() (already uses transaction, add context)
// - SaveMessageMetadataToDb()
// - SavePhotoMetadataToDb()
// - GetGmailData()
// - GetPhotos()
// ... etc (all ~27 database functions)
```

### 3.3 Update main.go with Graceful Close

**Add database close to shutdown sequence:**

```go
package main

import (
	"log/slog"
	"os"

	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/web"
)

func main() {
	// Initialize structured logger
	initLogger()

	// Initialize database connection with pool configuration
	if err := db.SetupDatabase(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}

	// Ensure database is closed when application exits
	defer func() {
		if err := db.CloseDatabase(); err != nil {
			slog.Error("Error during database close", "error", err)
		}
	}()

	slog.Info("Starting web server")
	web.Server()
}

func initLogger() {
	logLevel := new(slog.LevelVar)
	logLevel.Set(slog.LevelInfo)

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	logger := slog.New(handler)
	slog.SetDefault(logger)
}
```

### 3.4 Integration with Issue #8 Graceful Shutdown

**When Issue #8 is implemented, update main.go:**

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"github.com/jyothri/hdd/web"
)

func main() {
	// Initialize structured logger
	initLogger()

	// Initialize database connection with pool configuration
	if err := db.SetupDatabase(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}

	// Start web server (returns *http.Server from Issue #8)
	srv := web.Server()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	slog.Info("Shutdown signal received", "signal", sig.String())

	// WaitGroup to coordinate parallel shutdown
	var wg sync.WaitGroup

	// 1. Mark server as shutting down (Issue #8)
	web.MarkShuttingDown()

	// 2. Shutdown notification hub (Issue #13)
	notification.ShutdownHub()

	// 3. Shutdown HTTP server and database IN PARALLEL (Issue #8 + #17)
	wg.Add(2)

	// Shutdown HTTP server
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("HTTP server forced to shutdown", "error", err)
			srv.Close()
		} else {
			slog.Info("HTTP server shutdown completed gracefully")
		}
	}()

	// Close database
	go func() {
		defer wg.Done()
		if err := db.CloseDatabase(); err != nil {
			slog.Error("Database close failed", "error", err)
		}
	}()

	// Wait for both to complete
	wg.Wait()

	slog.Info("Application exited cleanly")
}
```

### 3.5 Health Check Endpoint (Optional)

**Add health endpoint to web/api.go:**

```go
package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/db"
)

func api(r *mux.Router) {
	api := r.PathPrefix("/api/").Subrouter()

	// Health check endpoint
	api.HandleFunc("/health", HealthCheckHandler).Methods("GET")

	// ... existing routes ...
}

// HealthCheckHandler returns application health status
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	dbHealth, dbErr := db.GetDatabaseHealth()

	health := map[string]interface{}{
		"status":    "healthy",
		"database":  dbHealth,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	statusCode := http.StatusOK

	// If database is unhealthy, mark overall status as unhealthy
	if dbErr != nil {
		health["status"] = "unhealthy"
		statusCode = http.StatusServiceUnavailable
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(health); err != nil {
		slog.Error("Failed to encode health check response", "error", err)
	}
}
```

---

## 4. Testing Strategy

### 4.1 Manual Testing

**Test 1: Database Startup**
```bash
# Start PostgreSQL
docker start postgres

# Start application
cd be
go run .

# Expected logs:
# INFO Database connection pool configured max_open=10 max_idle=5 ...
# INFO Successfully connected to database
# INFO Starting web server
```

**Test 2: Health Check Endpoint**
```bash
# Check health when database is up
curl http://localhost:8090/api/health | jq

# Expected response (200 OK):
{
  "status": "healthy",
  "database": {
    "connected": true,
    "ping_ms": 5,
    "stats": {
      "max_open_connections": 10,
      "open_connections": 1,
      "in_use": 0,
      "idle": 1,
      ...
    }
  },
  "timestamp": "2025-12-21T10:30:00Z"
}

# Stop PostgreSQL
docker stop postgres

# Check health when database is down
curl http://localhost:8090/api/health | jq

# Expected response (503 Service Unavailable):
{
  "status": "unhealthy",
  "database": {
    "connected": false,
    "error": "failed to ping database: ...",
    "stats": null
  },
  "timestamp": "2025-12-21T10:30:01Z"
}
```

**Test 3: Connection Pool Behavior**
```bash
# Start server
go run .

# Make concurrent requests to trigger connection pool
for i in {1..20}; do
  curl http://localhost:8090/api/scans &
done
wait

# Check health to see connection pool stats
curl http://localhost:8090/api/health | jq '.database.stats'

# Expected: open_connections <= 10 (max_open_connections)
# Expected: in_use + idle = open_connections
```

**Test 4: Query Timeout**
```bash
# This test requires a slow query
# Temporarily add to database.go for testing:

func TestSlowQuery() error {
    ctx, cancel := getDefaultContext()  // 30 second timeout
    defer cancel()

    // Simulate slow query
    _, err := db.ExecContext(ctx, "SELECT pg_sleep(35)")
    return err
}

# Run the test
# Expected: Returns error after ~30 seconds
# Expected log: "query timeout: context deadline exceeded"
```

**Test 5: Graceful Shutdown**
```bash
# Start server
go run . &
PID=$!

# Make a long-running request
curl http://localhost:8090/api/scans &
CURL_PID=$!

# Send SIGTERM
kill -TERM $PID

# Expected logs:
# INFO Shutdown signal received signal=terminated
# INFO Closing database connection...
# INFO Database connection stats at shutdown open_connections=... in_use=... idle=...
# INFO Database connection closed successfully
# INFO Application exited cleanly

# Check if curl completed (should finish gracefully)
wait $CURL_PID
echo "Curl exit code: $?"
# Expected: 0 (success) or connection error if timeout
```

**Test 6: Connection Recycling**
```bash
# This is a long-running test (5 minutes)
# Start server, make a query
go run . &
sleep 2
curl http://localhost:8090/api/scans

# Check stats
curl http://localhost:8090/api/health | jq '.database.stats.max_lifetime_closed'
# Expected: 0 (no connections recycled yet)

# Wait 6 minutes
sleep 360

# Make another query (forces connection check)
curl http://localhost:8090/api/scans

# Check stats again
curl http://localhost:8090/api/health | jq '.database.stats.max_lifetime_closed'
# Expected: >= 1 (connections recycled due to MaxLifetime)
```

### 4.2 Unit Tests

**Create db/database_test.go:**

```go
package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupDatabase(t *testing.T) {
	// This requires PostgreSQL to be running
	// Skip if not available
	err := SetupDatabase()
	if err != nil {
		t.Skipf("Database not available: %v", err)
	}
	defer CloseDatabase()

	// Verify db is not nil
	assert.NotNil(t, db)

	// Verify can ping
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = db.PingContext(ctx)
	assert.NoError(t, err)

	// Verify stats
	stats := db.Stats()
	assert.LessOrEqual(t, stats.MaxOpenConnections, maxOpenConns)
}

func TestCloseDatabase(t *testing.T) {
	err := SetupDatabase()
	if err != nil {
		t.Skipf("Database not available: %v", err)
	}

	// Close should not error
	err = CloseDatabase()
	assert.NoError(t, err)

	// Calling close again should not panic
	err = CloseDatabase()
	assert.NoError(t, err)
}

func TestGetDatabaseHealth_Healthy(t *testing.T) {
	err := SetupDatabase()
	if err != nil {
		t.Skipf("Database not available: %v", err)
	}
	defer CloseDatabase()

	health, err := GetDatabaseHealth()
	require.NoError(t, err)

	assert.True(t, health["connected"].(bool))
	assert.NotNil(t, health["stats"])
	assert.Greater(t, health["ping_ms"].(int64), int64(0))
}

func TestGetDatabaseHealth_NotInitialized(t *testing.T) {
	// Save current db
	oldDb := db
	defer func() { db = oldDb }()

	// Set db to nil
	db = nil

	health, err := GetDatabaseHealth()
	assert.Error(t, err)
	assert.False(t, health["connected"].(bool))
	assert.Contains(t, health["error"], "not initialized")
}

func TestQueryTimeout(t *testing.T) {
	err := SetupDatabase()
	if err != nil {
		t.Skipf("Database not available: %v", err)
	}
	defer CloseDatabase()

	// Create a context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Try a slow query (pg_sleep)
	_, err = db.ExecContext(ctx, "SELECT pg_sleep(1)")

	// Should timeout
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context deadline exceeded")
}

func TestConnectionPoolLimits(t *testing.T) {
	err := SetupDatabase()
	if err != nil {
		t.Skipf("Database not available: %v", err)
	}
	defer CloseDatabase()

	stats := db.Stats()

	// Should not exceed max open connections
	assert.LessOrEqual(t, stats.OpenConnections, maxOpenConns)
	assert.LessOrEqual(t, stats.Idle, maxIdleConns)
}
```

### 4.3 Integration Testing

**Test with real scans:**

```bash
# Test 1: Create scan and verify database operations
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/test"}}'

# Should return scan_id
# Check health to see connection usage
curl http://localhost:8090/api/health | jq '.database.stats'

# Test 2: Multiple concurrent scans
for i in {1..5}; do
  curl -X POST http://localhost:8090/api/scans \
    -H "Content-Type: application/json" \
    -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/test'$i'"}}' &
done
wait

# Check connection stats
curl http://localhost:8090/api/health | jq '.database.stats'
# Expected: open_connections <= 10, no wait_count
```

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Code review completed
- [ ] Unit tests passing
- [ ] Manual testing completed
- [ ] Integration tests passing
- [ ] Database migrations tested
- [ ] Backward compatibility verified
- [ ] Documentation updated
- [ ] Rollback plan prepared

### 5.2 Deployment Steps

**Step 1: Update Database Package**

```bash
cd be

# Update db/database.go with:
# - Connection pool configuration
# - CloseDatabase() function
# - GetDatabaseHealth() function
# - getDefaultContext() helper
# - Context timeout for all queries

# Verify builds
go build .
```

**Step 2: Update Database Functions**

```bash
# Update all database functions to use getDefaultContext()
# This is backward compatible (adds timeout, doesn't change signatures)

# Files to update:
# - db/database.go (all query functions)

# Verify builds
go build .
```

**Step 3: Update main.go**

```bash
# Add defer db.CloseDatabase()

# Verify builds
go build .
```

**Step 4: Add Health Endpoint (Optional)**

```bash
# Update web/api.go
# Add HealthCheckHandler

# Verify builds
go build .
```

**Step 5: Test Locally**

```bash
# Start PostgreSQL
docker start postgres

# Run application
./hdd &
PID=$!

# Test health endpoint
curl http://localhost:8090/api/health

# Test shutdown
kill -TERM $PID

# Expected logs show graceful close
```

**Step 6: Deploy to Staging**

```bash
# Build
go build -o hdd

# Deploy
scp hdd staging:/opt/bhandaar/

# Stop staging server
ssh staging 'systemctl stop bhandaar'

# Start server
ssh staging 'systemctl start bhandaar'

# Verify logs
ssh staging 'journalctl -u bhandaar -n 50'

# Expected:
# - "Database connection pool configured"
# - "Successfully connected to database"
# - No errors
```

**Step 7: Monitor Staging**

```bash
# Check health endpoint
curl https://staging/api/health

# Monitor logs for connection issues
ssh staging 'journalctl -u bhandaar -f | grep -i "database\|connection"'

# Run for 24 hours, check for:
# - Connection leaks
# - Query timeouts
# - Pool exhaustion
# - Graceful shutdown behavior
```

**Step 8: Deploy to Production**

```bash
# Tag release
git tag -a v1.x.x -m "Add database connection lifecycle management (Issue #17)"
git push origin v1.x.x

# Build production binary
go build -o hdd

# Deploy (Kubernetes example)
docker build -t jyothri/hdd-go-build:v1.x.x .
docker push jyothri/hdd-go-build:v1.x.x
kubectl set image deployment/bhandaar-backend backend=jyothri/hdd-go-build:v1.x.x
kubectl rollout status deployment/bhandaar-backend
```

**Step 9: Post-Deployment Verification**

```bash
# Check health endpoint
curl https://api.production.com/api/health

# Expected response with database stats

# Monitor logs
kubectl logs -f deployment/bhandaar-backend | grep -i "database\|connection"

# Watch for:
# - Successful connection pool configuration
# - No connection errors
# - Graceful shutdown on pod termination
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

# Verify rollback
curl https://api.production.com/api/health
```

### 5.4 Monitoring Post-Deployment

**Metrics to Watch:**

1. **Connection Pool Stats** (from /health endpoint)
   ```bash
   # Every 5 minutes
   curl -s https://api/api/health | jq '.database.stats'
   ```

2. **Database Logs** (on PostgreSQL server)
   ```bash
   # Check for connection errors
   tail -f /var/log/postgresql/postgresql-*.log | grep -i "connection\|error"
   ```

3. **Application Logs**
   ```bash
   # Watch for query timeouts
   kubectl logs -f deployment/bhandaar-backend | grep "context deadline exceeded"

   # Watch for connection errors
   kubectl logs -f deployment/bhandaar-backend | grep "database\|connection"
   ```

4. **Connection Count Over Time**
   ```bash
   # Create monitoring script
   while true; do
     echo "$(date): $(curl -s localhost:8090/api/health | jq -r '.database.stats.open_connections')"
     sleep 60
   done

   # Expected: Stable number, not growing
   ```

---

## 6. Integration with Issue #8

### 6.1 Shutdown Coordination

**Issue #8 Graceful Shutdown provides:**
- 30-second timeout for HTTP server shutdown
- Signal handling (SIGTERM/SIGINT)
- SSE notification to clients
- Health check returns 503 during shutdown

**Issue #17 adds:**
- Database close with 15-second timeout
- Connection pool stats logging
- Parallel shutdown with HTTP server

**Combined shutdown sequence:**

```go
func main() {
	// ... setup ...

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutdown initiated")

	// 1. Mark as shutting down (Issue #8)
	web.MarkShuttingDown()  // Health check returns 503

	// 2. Stop accepting new connections (Issue #8)
	// HTTP server stops accepting, existing requests continue

	// 3. Shutdown notification hub (Issue #13)
	notification.ShutdownHub()

	// 4. Parallel shutdown (Issue #8 + #17)
	var wg sync.WaitGroup
	wg.Add(2)

	// HTTP server: 30s timeout
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	// Database: 15s timeout
	go func() {
		defer wg.Done()
		db.CloseDatabase()  // Has internal 15s timeout
	}()

	wg.Wait()

	slog.Info("Shutdown complete")
}
```

**Timeout hierarchy:**
```
Total shutdown window: 30 seconds (from Issue #8)
├─ HTTP server shutdown: 30 seconds
│  └─ Finish in-flight requests
│
└─ Database close: 15 seconds
   └─ Wait for active queries
   └─ Close connections

Both run in parallel, so total time = max(30s, 15s) = 30s
```

### 6.2 Testing Graceful Shutdown Integration

**Test Scenario: Shutdown during active scan**

```bash
# Start server
go run . &
PID=$!

# Start long-running scan
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/large/directory"}}' &

# Wait for scan to start
sleep 2

# Send SIGTERM
kill -TERM $PID

# Expected behavior:
# 1. "Shutdown signal received" log
# 2. Health endpoint returns 503
# 3. Database begins close (logs connection stats)
# 4. HTTP server waits for scan to finish (or 30s timeout)
# 5. Both complete, "Application exited cleanly" log

# Verify logs show:
# - Database stats at shutdown
# - Database closed successfully
# - HTTP server shutdown gracefully
# - No errors about abrupt disconnection
```

---

## Appendix A: Complete File Changes Summary

### Files to Modify

1. **`db/database.go`**
   - Add connection pool configuration constants
   - Update `SetupDatabase()` with pool config
   - Add `CloseDatabase()` function
   - Add `GetDatabaseHealth()` function
   - Add `getDefaultContext()` helper
   - Update all query functions to use `getDefaultContext()`

2. **`main.go`**
   - Add `defer db.CloseDatabase()` after `SetupDatabase()`
   - (When Issue #8 implemented) Add parallel shutdown with WaitGroup

3. **`web/api.go`** (Optional)
   - Add `HealthCheckHandler()` function
   - Register `/api/health` route

### Files to Create

**`db/database_test.go`** (Optional but recommended)
- Test database setup
- Test connection pool configuration
- Test graceful close
- Test health check
- Test query timeouts

---

## Appendix B: Database Function Update Checklist

All functions in `db/database.go` need context timeout. Here's the complete list:

**Scan Operations:**
- [ ] `CreateScan()` - add context
- [ ] `LogStartScan()` - add context
- [ ] `MarkScanCompleted()` - add context
- [ ] `MarkScanFailed()` - add context
- [ ] `ListScans()` - add context
- [ ] `GetScan()` - add context
- [ ] `DeleteScan()` - add context to transaction

**Scan Data Operations:**
- [ ] `InsertScanData()` - add context
- [ ] `GetScanData()` - add context
- [ ] `InsertLocalScanData()` - add context

**OAuth/Account Operations:**
- [ ] `SaveOAuthToken()` - add context
- [ ] `GetRefreshToken()` - add context
- [ ] `GetAccount()` - add context
- [ ] `GetAccounts()` - add context
- [ ] `DeleteAccount()` - add context

**Gmail Operations:**
- [ ] `SaveMessageMetadataToDb()` - add context (goroutine, special handling)
- [ ] `GetGmailData()` - add context
- [ ] `GetScanRequestsForAccount()` - add context

**Photos Operations:**
- [ ] `SavePhotoMetadataToDb()` - add context (goroutine, special handling)
- [ ] `GetPhotos()` - add context
- [ ] `SavePhotoAlbum()` - add context
- [ ] `GetPhotoAlbums()` - add context

**Drive Operations:**
- [ ] `SaveDriveMetadataToDb()` - add context (if exists)
- [ ] `GetDriveData()` - add context (if exists)

**Migration:**
- [ ] `migrateDB()` - add context

**Total:** ~27 functions to update

---

## Appendix C: Connection Pool Tuning Guide

### When to Adjust MaxOpenConns

**Increase to 25-50 if:**
- Running multiple instances (each needs fewer connections)
- High concurrency requirements (many simultaneous scans)
- Database server has high capacity
- Monitoring shows frequent `WaitCount` > 0

**Keep at 10 if:**
- Single instance deployment
- Moderate traffic
- Database on shared hosting
- Want to be conservative

### When to Adjust MaxIdleConns

**Increase to match MaxOpenConns if:**
- Traffic is consistent throughout the day
- Connection establishment is expensive (TLS overhead)
- Want lowest possible latency

**Decrease to 2-3 if:**
- Traffic is very bursty
- Want to minimize resource usage
- Database charges per connection

### When to Adjust ConnMaxLifetime

**Decrease to 1-2 minutes if:**
- Using connection pooling load balancer
- Database instances scale up/down frequently
- Need aggressive connection recycling

**Increase to 15-30 minutes if:**
- Connection establishment is expensive
- Database is stable
- No load balancing concerns

### Monitoring Tuning Effectiveness

```bash
# Check if pool is sized correctly
curl http://localhost:8090/api/health | jq '.database.stats' | grep -E 'wait_count|open_connections'

# Good indicators:
# - wait_count: 0 or very low (no blocking)
# - open_connections: 3-7 (80% capacity available)

# Bad indicators:
# - wait_count: > 100 (frequently blocking, increase MaxOpenConns)
# - open_connections: always at max (increase MaxOpenConns)
# - open_connections: always 1-2 (decrease MaxIdleConns to save resources)
```

---

## Appendix D: Troubleshooting Guide

### Problem: "too many clients" error from PostgreSQL

**Diagnosis:**
```bash
# Check current PostgreSQL max_connections
docker exec postgres psql -U postgres -c "SHOW max_connections;"

# Check current connection count
docker exec postgres psql -U postgres -c "SELECT count(*) FROM pg_stat_activity;"
```

**Solution:**
- Decrease `maxOpenConns` in application
- Or increase PostgreSQL `max_connections` setting
- Typical PostgreSQL default: 100 connections

### Problem: High WaitCount in stats

**Diagnosis:**
```bash
curl http://localhost:8090/api/health | jq '.database.stats.wait_count'
curl http://localhost:8090/api/health | jq '.database.stats.wait_duration_ms'
```

**Solution:**
- Increase `maxOpenConns` if wait_count is high
- Check if queries are slow (optimize queries)
- Consider connection pooling at database level (pgBouncer)

### Problem: Connections not being recycled

**Diagnosis:**
```bash
# Check lifetime stats
curl http://localhost:8090/api/health | jq '.database.stats.max_lifetime_closed'

# Should increase over time (connections recycled every 5 min)
```

**Solution:**
- Verify `ConnMaxLifetime` is set correctly
- Check database server time vs app server time (clock skew)
- May need to restart application

### Problem: Database close hangs on shutdown

**Diagnosis:**
```bash
# Check for long-running queries
docker exec postgres psql -U postgres -c "
  SELECT pid, now() - pg_stat_activity.query_start AS duration, query
  FROM pg_stat_activity
  WHERE state = 'active' AND now() - pg_stat_activity.query_start > interval '30 seconds';"
```

**Solution:**
- Increase close timeout in `CloseDatabase()` (currently 15s)
- Investigate slow queries
- Consider adding query timeout context to all operations

### Problem: Query timeout errors

**Diagnosis:**
```bash
# Search logs for timeout errors
grep "context deadline exceeded" /var/log/bhandaar/app.log
```

**Solution:**
- Increase default timeout in `getDefaultContext()` (currently 30s)
- Optimize slow queries
- Add indices to database tables
- Use pagination for large result sets

---

**END OF DOCUMENT**
