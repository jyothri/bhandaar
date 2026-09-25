# Issue #8 Implementation Plan: Graceful Shutdown

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P1 - High Priority (Operational Stability)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #8: No Graceful Shutdown**. The current system terminates immediately on SIGTERM/SIGINT, dropping active requests and leaving resources in inconsistent states.

**Selected Approach:**
- **Shutdown Timeout**: 30 seconds for in-flight requests
- **Scan Handling**: Complete within timeout, mark as failed if forced
- **SSE Cleanup**: Send shutdown event, then close connections
- **Database**: Keep current defer cleanup pattern
- **Signals**: Handle SIGTERM and SIGINT
- **Health Check**: Return HTTP 503 during shutdown

**Estimated Effort:** 4-6 hours

**Impact:**
- Prevents dropped requests during deployment
- Ensures clean database connection closure
- Properly closes SSE connections
- Marks interrupted scans appropriately
- Enables zero-downtime deployments

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Implementation Details](#3-implementation-details)
4. [Testing Strategy](#4-testing-strategy)
5. [Deployment Plan](#5-deployment-plan)
6. [Monitoring and Verification](#6-monitoring-and-verification)

---

## 1. Current State Analysis

### 1.1 Current Implementation

**main.go (lines 28-42):**
```go
func main() {
    // Initialize database connection
    if err := db.SetupDatabase(); err != nil {
        slog.Error("Failed to initialize database", "error", err)
        os.Exit(1)
    }
    defer func() {
        if err := db.Close(); err != nil {
            slog.Error("Failed to close database", "error", err)
        }
    }()

    slog.Info("Starting web server")
    web.Server()  // ❌ Blocks forever, no shutdown handling
}
```

**web/web_server.go (lines 14-37):**
```go
func Server() {
    slog.Info("Starting web server.")
    r := mux.NewRouter()

    // ... middleware and route setup ...

    srv := &http.Server{
        Handler: handler,
        Addr:    ":8090",
        WriteTimeout: 10 * time.Second,
        ReadTimeout:  10 * time.Second,
    }
    log.Fatal(srv.ListenAndServe())  // ❌ Fatal on any error, no graceful shutdown
}
```

### 1.2 Problems with Current Implementation

| Problem | Impact | Severity |
|---------|--------|----------|
| **No signal handling** | SIGTERM kills process immediately | HIGH |
| **Active requests dropped** | 5xx errors for in-flight requests | HIGH |
| **SSE connections terminated** | Clients don't know server is shutting down | MEDIUM |
| **In-progress scans abandoned** | Scans left in "Pending" state forever | MEDIUM |
| **No graceful shutdown period** | Can't finish current work | HIGH |
| **Database connections leaked** | defer may not run on forced kill | MEDIUM |
| **No health check indication** | Load balancer can't drain traffic | MEDIUM |

### 1.3 Impact Scenarios

**Scenario 1: Kubernetes Deployment**
```
1. New pod starts
2. Kubernetes sends SIGTERM to old pod
3. ❌ Old pod dies immediately
4. ❌ Active API requests return connection errors
5. ❌ SSE clients reconnect (unnecessary)
6. ❌ In-progress scans marked as failed in database
```

**Scenario 2: Server Restart**
```
1. Admin runs: systemctl restart bhandaar
2. systemd sends SIGTERM
3. ❌ Server killed immediately
4. ❌ Active Gmail scan interrupted
5. ❌ Scan stays in "Pending" forever
6. User sees stuck scan, has to delete manually
```

**Scenario 3: Cloud Auto-scaling**
```
1. Load decreases, scale-in triggered
2. Instance receives termination notice
3. ❌ Instance killed after 30s regardless of state
4. ❌ Database connections not properly closed
5. ❌ Connection pool exhaustion on DB side
```

### 1.4 Resources Requiring Cleanup

| Resource | Current State | Cleanup Needed |
|----------|---------------|----------------|
| **HTTP Server** | Blocking on ListenAndServe | Shutdown with context timeout |
| **Active HTTP Requests** | Terminated mid-flight | Allow completion within timeout |
| **SSE Connections** | Dropped without notice | Send shutdown event, close gracefully |
| **Notification Hub** | Goroutines may keep running | Close publisher channels |
| **In-Progress Scans** | Left in "Pending" state | Mark as "Interrupted" or "Failed" |
| **Database Connection Pool** | Closed via defer (if it runs) | Ensure defer executes after shutdown |

---

## 2. Target Architecture

### 2.1 Shutdown Flow Diagram

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Normal Operation                                         │
│    - HTTP server running                                    │
│    - Processing requests                                    │
│    - Scans executing in background                          │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Signal Received (SIGTERM or SIGINT)                     │
│    - Signal handler catches interrupt                       │
│    - Logs: "Shutdown signal received"                      │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Health Check Updated                                     │
│    - /api/health returns HTTP 503                          │
│    - Load balancer stops sending new traffic               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Server Stops Accepting New Connections                  │
│    - srv.Shutdown(ctx) called with 30s timeout             │
│    - New requests rejected with "connection refused"        │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. In-Flight Requests Complete (up to 30s)                 │
│    - Active API calls allowed to finish                     │
│    - New requests not accepted                              │
│    - Timeout enforced                                       │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 6. SSE Connections Closed                                   │
│    - Send "shutdown" event to all SSE clients              │
│    - Close SSE connections gracefully                       │
│    - Clients can reconnect to new instance                  │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 7. Background Scans Handled                                 │
│    - Scans completing within 30s: marked "Completed"       │
│    - Scans still running at timeout: marked "Interrupted"   │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 8. Database Connection Closed                               │
│    - defer db.Close() executes                              │
│    - All connections returned to pool                       │
│    - Clean shutdown logged                                  │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 9. Process Exits                                            │
│    - Exit code 0 (clean shutdown)                          │
│    - All resources cleaned up                               │
│    - Logs: "Server exited cleanly"                         │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Component Responsibilities

**main.go:**
- Set up signal handling for SIGTERM and SIGINT
- Coordinate shutdown sequence
- Wait for http.Server.Shutdown() to complete
- Ensure database cleanup via existing defer

**web/web_server.go:**
- Return *http.Server instead of blocking
- Start server in goroutine
- Provide mechanism to update health check status

**web/api.go:**
- Add shutdown state tracking
- Update health endpoint to return 503 during shutdown

**notification/hub.go:**
- Add Shutdown() method to notify all subscribers
- Close publisher channels on shutdown

---

## 3. Implementation Details

### 3.1 Main Application: `main.go`

**Complete rewrite of main() function:**

```go
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/notification"
	"github.com/jyothri/hdd/web"
)

func init() {
	options := &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("2006-01-02 15:04:05.999"))
			}
			return a
		},
		Level: slog.LevelDebug,
	}

	handler := slog.NewTextHandler(os.Stdout, options)
	logger := slog.New(handler)
	slog.SetDefault(logger)
	slog.SetLogLoggerLevel(slog.LevelDebug)
}

func main() {
	// Initialize database connection
	if err := db.SetupDatabase(); err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close database", "error", err)
		} else {
			slog.Info("Database connection closed successfully")
		}
	}()

	// Start web server (non-blocking)
	slog.Info("Starting web server")
	srv := web.StartServer()

	// Set up signal handling for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Block until signal received
	sig := <-quit
	slog.Info("Shutdown signal received", "signal", sig.String())

	// Mark server as shutting down (health checks will return 503)
	web.MarkShuttingDown()

	// Notify all SSE clients about shutdown
	notification.NotifyShutdown()

	// Create shutdown context with 30 second timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slog.Info("Gracefully shutting down server", "timeout", "30s")

	// Attempt graceful shutdown
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown after timeout", "error", err)
		// Force close if graceful shutdown times out
		if err := srv.Close(); err != nil {
			slog.Error("Failed to force close server", "error", err)
		}
	} else {
		slog.Info("Server shutdown completed gracefully")
	}

	// Give a moment for deferred cleanups
	time.Sleep(100 * time.Millisecond)

	slog.Info("Application exited cleanly")
}
```

### 3.2 Web Server: `web/web_server.go`

**Update to return *http.Server and start in goroutine:**

```go
package web

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/jyothri/hdd/constants"
	"github.com/rs/cors"
)

// Shutdown state tracking
var isShuttingDown atomic.Bool

// StartServer starts the HTTP server and returns the server instance
func StartServer() *http.Server {
	slog.Info("Initializing web server")
	r := mux.NewRouter()

	// Apply global default size limit to all routes (512 KB)
	r.Use(RequestSizeLimitMiddleware(DefaultMaxBodySize))

	api(r)
	oauth(r)
	sse(r)

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
		IdleTimeout:  60 * time.Second,
	}

	// Start server in goroutine
	go func() {
		slog.Info("Server listening", "addr", ":8090")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("Server error", "error", err)
		}
	}()

	return srv
}

// MarkShuttingDown sets the shutdown flag
func MarkShuttingDown() {
	isShuttingDown.Store(true)
	slog.Info("Server marked as shutting down")
}

// IsShuttingDown returns true if server is shutting down
func IsShuttingDown() bool {
	return isShuttingDown.Load()
}
```

### 3.3 Health Endpoint Update: `web/api.go`

**Update health endpoint to return 503 during shutdown:**

```go
func api(r *mux.Router) {
	// Handle API routes
	api := r.PathPrefix("/api/").Subrouter()

	// Health check endpoint
	api.HandleFunc("/health", healthCheckHandler)

	// Scan POST endpoint with larger body limit (1 MB)
	scanPostRouter := api.PathPrefix("/scans").Subrouter()
	scanPostRouter.Use(RequestSizeLimitMiddleware(ScanRequestMaxBodySize))
	scanPostRouter.HandleFunc("", DoScansHandler).Methods("POST")

	// ... rest of routes
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	if IsShuttingDown() {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"ok":     false,
			"status": "shutting_down",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}
```

### 3.4 Notification Hub Shutdown: `notification/hub.go`

**Add shutdown notification mechanism:**

```go
// Add to notification/hub.go

// NotifyShutdown sends shutdown notification to all SSE subscribers
func NotifyShutdown() {
	globalHub.mu.RLock()
	defer globalHub.mu.RUnlock()

	slog.Info("Notifying SSE clients about shutdown",
		"subscriber_count", len(globalHub.subscribers))

	shutdownProgress := Progress{
		ClientKey:      "system",
		ProcessedCount: -1,
		ActiveCount:    -1,
		CompletionPct:  -1,
		ElapsedInSec:   -1,
		EtaInSec:       -1,
		ScanId:         -1,
	}

	// Send shutdown notification to all subscribers
	for clientKey, subscriber := range globalHub.subscribers {
		if subscriber != nil {
			select {
			case subscriber <- shutdownProgress:
				slog.Info("Sent shutdown notification", "client_key", clientKey)
			default:
				slog.Warn("Could not send shutdown notification (channel full)",
					"client_key", clientKey)
			}
		}
	}
}

// Shutdown closes all publisher channels gracefully
func Shutdown() {
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()

	slog.Info("Shutting down notification hub",
		"publisher_count", len(globalHub.publishers),
		"subscriber_count", len(globalHub.subscribers))

	// Close all publisher channels
	for clientKey, publisher := range globalHub.publishers {
		if publisher != nil {
			close(publisher)
			slog.Info("Closed publisher channel", "client_key", clientKey)
		}
		delete(globalHub.publishers, clientKey)
	}

	// Subscriber channels will be closed by processNotifications goroutines
}
```

### 3.5 SSE Handler Update: `web/sse.go`

**Update SSE handlers to detect shutdown notification:**

```go
func scanProgressHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	subscriber := notification.GetSubscriber(notification.NOTIFICATION_ALL)
	rc := http.NewResponseController(w)
	clientGone := r.Context().Done()
	slog.Info("[scan events] Client Connected.")
	start := time.Now()

	for {
		select {
		case <-clientGone:
			slog.Info(fmt.Sprintf("[scan events] Client disconnected. Connection Duration: %s",
				time.Since(start)))
			return

		case progress, more := <-subscriber:
			timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)

			// Check for shutdown notification (ScanId == -1)
			if progress.ScanId == -1 {
				slog.Info("[scan events] Shutdown notification received")
				if _, err := fmt.Fprintf(w, "event:shutdown\nretry: 10000\nid:%s\ndata:server shutting down\n\n",
					timestamp); err != nil {
					slog.Warn(fmt.Sprintf("[scan events] Unable to write shutdown event. err: %s",
						err.Error()))
				}
				rc.Flush()
				return
			}

			if !more {
				if _, err := fmt.Fprintf(w, "event:close\nretry: 10000\nid:%s\ndata:close at %s \n\n",
					timestamp, time.Now().Format(time.RFC850)); err != nil {
					slog.Warn(fmt.Sprintf("[scan events] Unable to write. err: %s", err.Error()))
					return
				}
			}

			slog.Info(fmt.Sprintf("[scan events] Got progress notification: %v", progress))
			serializedBody, err := json.Marshal(progress)
			if err != nil {
				slog.Warn(fmt.Sprintf("[scan events] Unable to Serialize. err: %s", err.Error()))
				continue
			}

			if _, err := fmt.Fprintf(w, "event:progress\nretry: 10000\nid:%s\ndata:%v \n\n",
				timestamp, string(serializedBody)); err != nil {
				slog.Warn(fmt.Sprintf("[scan events] Unable to write. err: %s", err.Error()))
			}
			rc.SetWriteDeadline(time.Time{})
			rc.Flush()
		}
	}
}
```

---

## 4. Testing Strategy

### 4.1 Unit Tests

**`web/web_server_test.go`** (New file)

```go
package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMarkShuttingDown(t *testing.T) {
	// Reset state
	isShuttingDown.Store(false)

	// Initially not shutting down
	assert.False(t, IsShuttingDown())

	// Mark as shutting down
	MarkShuttingDown()

	// Should now be true
	assert.True(t, IsShuttingDown())
}

func TestStartServer(t *testing.T) {
	srv := StartServer()
	assert.NotNil(t, srv)
	assert.Equal(t, ":8090", srv.Addr)

	// Clean up
	srv.Close()
}
```

**`notification/hub_test.go`** (Add to existing or create)

```go
package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotifyShutdown(t *testing.T) {
	// Setup
	globalHub = &Hub{
		publishers:  make(map[string]chan Progress),
		subscribers: make(map[string]chan Progress),
	}

	// Create subscriber
	subscriber := GetSubscriber("test-client")

	// Send shutdown notification
	go NotifyShutdown()

	// Should receive shutdown notification
	select {
	case progress := <-subscriber:
		assert.Equal(t, -1, progress.ScanId)
		assert.Equal(t, "system", progress.ClientKey)
	case <-time.After(1 * time.Second):
		t.Fatal("Did not receive shutdown notification")
	}
}

func TestShutdown(t *testing.T) {
	// Setup
	globalHub = &Hub{
		publishers:  make(map[string]chan Progress),
		subscribers: make(map[string]chan Progress),
	}

	// Create publisher
	publisher := GetPublisher("test-client")

	// Verify publisher exists
	globalHub.mu.RLock()
	assert.Len(t, globalHub.publishers, 1)
	globalHub.mu.RUnlock()

	// Shutdown
	Shutdown()

	// Publishers should be closed
	globalHub.mu.RLock()
	assert.Len(t, globalHub.publishers, 0)
	globalHub.mu.RUnlock()

	// Writing to publisher should panic (channel closed)
	assert.Panics(t, func() {
		publisher <- Progress{}
	})
}
```

### 4.2 Integration Tests

**Manual Integration Test Procedure:**

```bash
# Test 1: Normal shutdown with no active requests
# 1. Start server
go run .

# 2. Send SIGTERM
kill -TERM <pid>

# Expected output:
# - "Shutdown signal received signal=terminated"
# - "Gracefully shutting down server timeout=30s"
# - "Server shutdown completed gracefully"
# - "Database connection closed successfully"
# - "Application exited cleanly"
# Exit code: 0

# Test 2: Shutdown with active requests
# 1. Start server
go run .

# 2. In another terminal, start a long request
curl http://localhost:8090/api/scans &

# 3. Immediately send SIGTERM
kill -TERM <pid>

# Expected:
# - Request completes (if within 30s)
# - Server waits for completion
# - Then shuts down gracefully

# Test 3: Shutdown with SSE connection
# 1. Start server
go run .

# 2. Connect SSE client
curl -N http://localhost:8090/sse/scanprogress &

# 3. Send SIGTERM
kill -TERM <pid>

# Expected:
# - SSE client receives "event:shutdown"
# - Connection closed gracefully
# - Server shuts down

# Test 4: Forced shutdown (timeout exceeded)
# Simulate by having a request that takes >30s
# Server should log: "Server forced to shutdown after timeout"
```

### 4.3 Automated Integration Test

**`test/integration/shutdown_test.go`** (New file)

```go
//go:build integration
// +build integration

package integration

import (
	"context"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/jyothri/hdd/db"
	"github.com/jyothri/hdd/web"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGracefulShutdown(t *testing.T) {
	// Setup database
	err := db.SetupDatabase()
	require.NoError(t, err)
	defer db.Close()

	// Start server
	srv := web.StartServer()
	time.Sleep(100 * time.Millisecond) // Let server start

	// Verify server is running
	resp, err := http.Get("http://localhost:8090/api/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Mark as shutting down
	web.MarkShuttingDown()

	// Health check should now return 503
	resp, err = http.Get("http://localhost:8090/api/health")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	resp.Body.Close()

	// Shutdown server
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestShutdownWithActiveRequest(t *testing.T) {
	// Setup
	err := db.SetupDatabase()
	require.NoError(t, err)
	defer db.Close()

	srv := web.StartServer()
	time.Sleep(100 * time.Millisecond)

	// Start a request that will be in-flight during shutdown
	requestComplete := make(chan bool)
	go func() {
		resp, err := http.Get("http://localhost:8090/api/health")
		if err == nil {
			resp.Body.Close()
			requestComplete <- true
		} else {
			requestComplete <- false
		}
	}()

	// Give request time to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown with generous timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdownComplete := make(chan bool)
	go func() {
		err := srv.Shutdown(ctx)
		shutdownComplete <- (err == nil)
	}()

	// Request should complete
	select {
	case completed := <-requestComplete:
		assert.True(t, completed, "Request should complete successfully")
	case <-time.After(3 * time.Second):
		t.Fatal("Request did not complete in time")
	}

	// Shutdown should complete
	select {
	case success := <-shutdownComplete:
		assert.True(t, success, "Shutdown should complete successfully")
	case <-time.After(6 * time.Second):
		t.Fatal("Shutdown did not complete in time")
	}
}
```

### 4.4 Load Test During Shutdown

```bash
# Using vegeta for load testing
echo "GET http://localhost:8090/api/health" | \
  vegeta attack -duration=60s -rate=50 | \
  vegeta report

# While test is running (at ~30s mark):
# Send SIGTERM to server

# Expected:
# - Requests before shutdown: 200 OK
# - Requests during shutdown grace period: 200 OK (completing)
# - Requests after server stops: Connection refused
# - No requests should return 500 or timeout unexpectedly
```

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Code review completed
- [ ] Unit tests passing
- [ ] Integration tests passing
- [ ] Manual shutdown testing completed
- [ ] Load balancer health check configured to use `/api/health`
- [ ] Deployment scripts updated for graceful shutdown
- [ ] Rollback plan prepared
- [ ] Team trained on new shutdown behavior

### 5.2 Deployment Steps

**Step 1: Deploy to Staging**

```bash
# Build
cd be
go build -o hdd

# Deploy to staging
scp hdd staging-server:/opt/bhandaar/
ssh staging-server 'systemctl restart bhandaar'

# Verify graceful shutdown works
ssh staging-server 'systemctl stop bhandaar'
ssh staging-server 'journalctl -u bhandaar -n 50'

# Look for:
# - "Shutdown signal received"
# - "Server shutdown completed gracefully"
# - "Application exited cleanly"
```

**Step 2: Update systemd Service (if using systemd)**

Update `/etc/systemd/system/bhandaar.service`:

```ini
[Unit]
Description=Bhandaar Storage Analyzer
After=network.target postgresql.service

[Service]
Type=simple
User=bhandaar
WorkingDirectory=/opt/bhandaar
ExecStart=/opt/bhandaar/hdd
Restart=on-failure
RestartSec=5s

# Graceful shutdown settings
KillMode=mixed
KillSignal=SIGTERM
TimeoutStopSec=35s
# Give app 30s + 5s buffer

[Install]
WantedBy=multi-user.target
```

Reload systemd:
```bash
systemctl daemon-reload
```

**Step 3: Update Kubernetes Deployment (if using k8s)**

Update deployment YAML:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bhandaar-backend
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    spec:
      containers:
      - name: backend
        image: jyothri/hdd-go-build:latest
        ports:
        - containerPort: 8090

        # Liveness probe - server is alive
        livenessProbe:
          httpGet:
            path: /api/health
            port: 8090
          initialDelaySeconds: 10
          periodSeconds: 10
          timeoutSeconds: 5
          failureThreshold: 3

        # Readiness probe - server ready for traffic
        readinessProbe:
          httpGet:
            path: /api/health
            port: 8090
          initialDelaySeconds: 5
          periodSeconds: 5
          timeoutSeconds: 3
          failureThreshold: 2

        # Graceful shutdown
        lifecycle:
          preStop:
            exec:
              # Optional: can add custom cleanup script
              command: ["/bin/sh", "-c", "sleep 5"]

        # Termination grace period (must be > app shutdown timeout)
        terminationGracePeriodSeconds: 35
```

**Step 4: Deploy to Production**

```bash
# Tag release
git tag -a v1.x.x -m "Add graceful shutdown"
git push origin v1.x.x

# Build production binary
cd be
go build -o hdd

# For Docker/Kubernetes
docker build -t jyothri/hdd-go-build:v1.x.x .
docker push jyothri/hdd-go-build:v1.x.x

# For direct deployment
scp hdd production-server:/opt/bhandaar/hdd.new
ssh production-server 'mv /opt/bhandaar/hdd /opt/bhandaar/hdd.backup'
ssh production-server 'mv /opt/bhandaar/hdd.new /opt/bhandaar/hdd'
ssh production-server 'systemctl restart bhandaar'

# Monitor logs
ssh production-server 'journalctl -u bhandaar -f'
```

**Step 5: Verify Production Deployment**

```bash
# Test health endpoint
curl https://api.your-domain.com/api/health
# Expected: {"ok":true}

# Test graceful shutdown (on one instance if load balanced)
# Find PID
ssh production-server 'pgrep hdd'

# Send SIGTERM
ssh production-server 'kill -TERM <pid>'

# Watch logs
ssh production-server 'journalctl -u bhandaar -n 100'

# Verify:
# - "Shutdown signal received"
# - "Server shutdown completed gracefully"
# - No error messages
# - Exit code 0
```

### 5.3 Rollback Procedure

If issues are detected:

```bash
# Stop current service
systemctl stop bhandaar

# Restore previous binary
mv /opt/bhandaar/hdd.backup /opt/bhandaar/hdd

# Restart
systemctl start bhandaar

# Verify
systemctl status bhandaar
curl http://localhost:8090/api/health
```

For Kubernetes:

```bash
# Rollback to previous deployment
kubectl rollback deployment/bhandaar-backend

# Verify
kubectl get pods
kubectl logs -f deployment/bhandaar-backend
```

---

## 6. Monitoring and Verification

### 6.1 Metrics to Track

**Shutdown Metrics:**
- Time to complete graceful shutdown (should be < 30s)
- Number of requests dropped during shutdown (should be 0)
- Number of scans interrupted during shutdown
- Database connection cleanup success rate

**Operational Metrics:**
- Application restart count
- Graceful shutdown success rate
- Forced shutdown count (timeout exceeded)
- SSE client disconnection rate during shutdown

### 6.2 Log Analysis

**Successful Shutdown Log Pattern:**
```
2025-12-21 10:30:00.000 INFO Shutdown signal received signal=terminated
2025-12-21 10:30:00.001 INFO Server marked as shutting down
2025-12-21 10:30:00.002 INFO Notifying SSE clients about shutdown subscriber_count=3
2025-12-21 10:30:00.003 INFO Gracefully shutting down server timeout=30s
2025-12-21 10:30:05.123 INFO Server shutdown completed gracefully
2025-12-21 10:30:05.223 INFO Database connection closed successfully
2025-12-21 10:30:05.323 INFO Application exited cleanly
```

**Forced Shutdown Log Pattern (timeout):**
```
2025-12-21 10:30:00.000 INFO Shutdown signal received signal=terminated
2025-12-21 10:30:00.001 INFO Gracefully shutting down server timeout=30s
2025-12-21 10:30:30.001 ERROR Server forced to shutdown after timeout error=context deadline exceeded
2025-12-21 10:30:30.050 INFO Database connection closed successfully
2025-12-21 10:30:30.100 INFO Application exited cleanly
```

### 6.3 Alerting Rules

**Prometheus Alert Examples:**

```yaml
groups:
  - name: bhandaar_shutdown
    rules:
      - alert: FrequentRestarts
        expr: rate(process_start_time_seconds[5m]) > 0.1
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Bhandaar backend restarting frequently"
          description: "Instance {{ $labels.instance }} restarting > 0.1/s"

      - alert: ForcedShutdowns
        expr: increase(bhandaar_forced_shutdown_total[1h]) > 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Bhandaar forced shutdowns detected"
          description: "Instance {{ $labels.instance }} had {{ $value }} forced shutdowns"
```

### 6.4 Dashboards

**Grafana Dashboard Panels:**

1. **Shutdown Duration**
   - Graph showing time taken for graceful shutdown
   - Threshold line at 30s

2. **Shutdown Success Rate**
   - Pie chart: Graceful vs Forced shutdowns
   - Target: 100% graceful

3. **Active Connections During Shutdown**
   - Time series of active connections
   - Should drop to 0 within timeout

4. **SSE Clients Notified**
   - Count of SSE clients that received shutdown notification
   - Should match total connected clients

---

## 7. Security Considerations

### 7.1 Signal Handling Security

**Signal Bombing Prevention:**

The implementation already handles this correctly - only one signal triggers shutdown:

```go
quit := make(chan os.Signal, 1)  // Buffer of 1
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
sig := <-quit  // Blocks until ONE signal received
// Subsequent signals ignored during shutdown
```

**Unauthorized Shutdown Prevention:**

Ensure only authorized processes can send signals:

```bash
# Set proper file permissions
chmod 755 /opt/bhandaar/hdd

# Run as non-root user
User=bhandaar  # in systemd service file

# Only root and bhandaar user can kill process
```

### 7.2 Resource Cleanup Security

**Database Connection Leaks:**

The defer pattern ensures cleanup even on panic:

```go
defer func() {
    if err := db.Close(); err != nil {
        slog.Error("Failed to close database", "error", err)
    }
}()
```

**SSE Connection Cleanup:**

Shutdown notification prevents clients from reconnecting unnecessarily:

```javascript
// Frontend should handle shutdown event:
eventSource.addEventListener('shutdown', () => {
    console.log('Server shutting down, will reconnect after delay');
    eventSource.close();
    // Wait before reconnecting to allow server restart
    setTimeout(() => reconnect(), 10000);
});
```

---

## 8. Best Practices Implemented

### 8.1 The Twelve-Factor App Compliance

✅ **IX. Disposability**: Maximize robustness with fast startup and graceful shutdown
✅ **VIII. Concurrency**: Handles concurrent requests during shutdown
✅ **VII. Port binding**: Clean release of port 8090
✅ **VI. Processes**: Stateless, cleans up all resources

### 8.2 Cloud Native Principles

✅ **Health checks**: Returns 503 during shutdown for load balancer draining
✅ **Observability**: Comprehensive logging of shutdown process
✅ **Resilience**: Handles both graceful and forced shutdown scenarios
✅ **Automation**: Works seamlessly with orchestration systems (k8s, systemd)

### 8.3 Go Best Practices

✅ **Context usage**: Proper context with timeout for shutdown
✅ **Goroutine management**: Server started in goroutine, cleaned up properly
✅ **Channel usage**: Buffered channel for signal handling
✅ **Error handling**: All errors logged, none silently ignored

---

## Appendix A: Complete File Changes Summary

### Files to Modify

1. **`main.go`** - Major update
   - Add signal handling imports
   - Rewrite main() function for graceful shutdown
   - Keep existing init() and defer db.Close()

2. **`web/web_server.go`** - Significant update
   - Rename Server() to StartServer()
   - Return *http.Server instead of blocking
   - Start server in goroutine
   - Add shutdown state tracking (atomic.Bool)
   - Add MarkShuttingDown() and IsShuttingDown() functions

3. **`web/api.go`** - Minor update
   - Update health endpoint to check shutdown state
   - Return 503 during shutdown

4. **`notification/hub.go`** - Add methods
   - Add NotifyShutdown() function
   - Add Shutdown() function (optional, for cleanup)

5. **`web/sse.go`** - Minor update
   - Detect shutdown notification (ScanId == -1)
   - Send "event:shutdown" to SSE clients

### Files to Create

1. **`web/web_server_test.go`** - New
   - Unit tests for shutdown state tracking

2. **`notification/hub_test.go`** - Add tests (or create if doesn't exist)
   - Test NotifyShutdown()
   - Test Shutdown()

3. **`test/integration/shutdown_test.go`** - New
   - Integration tests for graceful shutdown scenarios

---

## Appendix B: Testing Checklist

### Manual Testing
- [ ] Server starts successfully
- [ ] Health endpoint returns 200 OK during normal operation
- [ ] SIGTERM triggers graceful shutdown
- [ ] SIGINT (Ctrl+C) triggers graceful shutdown
- [ ] Health endpoint returns 503 during shutdown
- [ ] Active requests complete during shutdown (within timeout)
- [ ] Requests after shutdown receive connection refused
- [ ] SSE clients receive shutdown notification
- [ ] Database connection closes properly
- [ ] Server logs "Application exited cleanly"
- [ ] Exit code is 0 on graceful shutdown

### Integration Testing
- [ ] Shutdown with no active requests completes in <1s
- [ ] Shutdown with active requests waits for completion
- [ ] Shutdown with SSE connections notifies clients
- [ ] Shutdown timeout enforced (30s)
- [ ] Forced shutdown works when timeout exceeded
- [ ] Load test during shutdown shows clean transition

### Production Verification
- [ ] Zero-downtime deployment works
- [ ] Load balancer drains traffic properly
- [ ] No 5xx errors during deployment
- [ ] Logs show graceful shutdown
- [ ] Metrics show 100% graceful shutdown rate
- [ ] No database connection leaks

---

## Appendix C: Troubleshooting Guide

### Problem: Server doesn't shut down gracefully

**Symptoms:**
- Logs show "Server forced to shutdown after timeout"
- Exit takes exactly 30 seconds

**Diagnosis:**
```bash
# Check what's keeping server alive
# Send SIGTERM, then check goroutines before timeout
kill -TERM <pid>
# Quickly attach debugger or add debug logging
```

**Common Causes:**
1. Long-running scan still active
2. Database query hanging
3. External API call not timing out
4. SSE connection not closing

**Solution:**
- Add timeouts to all external calls
- Ensure all database queries have context with timeout
- Close SSE connections more aggressively

### Problem: Requests dropped during shutdown

**Symptoms:**
- 502 Bad Gateway errors
- Connection refused errors

**Diagnosis:**
- Check load balancer health check interval
- Verify health endpoint returns 503 immediately on shutdown

**Solution:**
- Reduce health check interval to 1-2 seconds
- Add small delay before starting shutdown to allow LB to detect

### Problem: Database connections leaked

**Symptoms:**
- PostgreSQL shows too many connections
- "too many clients already" errors

**Diagnosis:**
```sql
-- Check active connections
SELECT count(*) FROM pg_stat_activity WHERE datname = 'hdd_db';
```

**Solution:**
- Verify db.Close() is called
- Check for unclosed rows/statements in code
- Add connection pool size limits

---

**END OF DOCUMENT**
