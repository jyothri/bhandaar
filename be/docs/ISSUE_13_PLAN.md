# Issue #13 Implementation Plan: Goroutine Leaks in Notification System

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P1 - High Priority (Resource Exhaustion)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #13: Goroutine Leaks in Notification System**. The current system starts goroutines for SSE notifications but fails to properly clean them up, leading to memory leaks and eventual resource exhaustion.

**Selected Approach:**
- **Idle Timeout**: 5-minute timeout for idle publishers
- **Slow Subscribers**: 5-second timeout, drop messages if subscriber slow
- **Photos Cleanup**: Add `defer close(notificationChannel)` (same pattern as gmail)
- **SSE Cleanup**: Auto-cleanup subscribers when client disconnects
- **Graceful Shutdown**: Integrate with Issue #8 - close all publishers/subscribers on SIGTERM
- **Monitoring**: Use existing GetPublisherCount/GetSubscriberCount methods

**Estimated Effort:** 6-8 hours

**Impact:**
- Prevents goroutine leaks from abandoned scans
- Prevents memory exhaustion from slow/stuck subscribers
- Enables clean shutdown of notification system
- Fixes photos scan publisher leak
- Improves SSE connection cleanup

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

**notification/hub.go - Current processNotifications:**
```go
func processNotifications(clientKey string) {
	// Get publisher channel safely
	globalHub.mu.RLock()
	publisher := globalHub.publishers[clientKey]
	globalHub.mu.RUnlock()

	if publisher == nil {
		return
	}

	for progress := range publisher {  // ❌ Blocks forever if channel never closes
		// Get subscribers safely for each notification
		globalHub.mu.RLock()
		subscriber := globalHub.subscribers[clientKey]
		subscriberAll := globalHub.subscribers[NOTIFICATION_ALL]
		globalHub.mu.RUnlock()

		pushToSubscriber(subscriber, progress)      // ❌ Blocks if channel full
		pushToSubscriber(subscriberAll, progress)   // ❌ Blocks if channel full
	}

	// Clean up after publisher channel closes
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()

	// Close and remove subscriber if it exists
	if ch, exists := globalHub.subscribers[clientKey]; exists {
		close(ch)
		delete(globalHub.subscribers, clientKey)
	}

	// Remove publisher
	delete(globalHub.publishers, clientKey)
}

func pushToSubscriber(subscriber chan<- Progress, progress Progress) {
	if subscriber == nil {
		return
	}
	subscriber <- progress  // ❌ BLOCKS FOREVER if subscriber can't receive
}
```

**collect/gmail.go - Proper Cleanup (✅):**
```go
func logProgress(scanId int, ClientKey string, done <-chan bool, ticker *time.Ticker, notificationChannel chan<- notification.Progress) {
	defer close(notificationChannel)  // ✅ Good - closes publisher on exit
	for {
		select {
		case <-done:
			// ... send final progress ...
			return
		case <-ticker.C:
			// ... send periodic progress ...
		}
	}
}
```

**collect/photos.go - Missing Cleanup (❌):**
```go
func startPhotosScan(scanId int, photosScan GPhotosScan, photosMediaItem chan<- db.PhotosMediaItem) error {
	// ...
	notificationChannel := notification.GetPublisher(photosScan.AlbumId)
	go logProgress(scanId, photosScan.AlbumId, done, ticker, notificationChannel)
	// ❌ NEVER CLOSES notificationChannel!
	// ❌ Photos scans don't have logProgress function with defer close
	// ...
}
```

**web/sse.go - SSE Handler:**
```go
func scanProgressHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	subscriber := notification.GetSubscriber(notification.NOTIFICATION_ALL)  // Creates channel
	rc := http.NewResponseController(w)
	clientGone := r.Context().Done()
	slog.Info("[scan events] Client Connected.")
	start := time.Now()
	for {
		select {
		case <-clientGone:
			slog.Info(fmt.Sprintf("[scan events] Client disconnected.Connection Duration: %s", time.Since(start)))
			return  // ❌ Returns but never cleans up subscriber channel!
		case progress, more := <-subscriber:
			// ... send to client ...
		}
	}
}
```

### 1.2 Leak Scenarios

**Scenario 1: Photos Scan Never Closes Publisher**

```
Timeline:
1. User starts photos scan
2. GetPublisher("album123") creates channel + starts processNotifications goroutine
3. Photos scan completes
4. ❌ Publisher channel NEVER closed
5. ❌ processNotifications goroutine STUCK in "for progress := range publisher"
6. ❌ Goroutine leaks forever

Result:
- 1 goroutine leaked per photos scan
- After 100 photos scans: 100 leaked goroutines
- Each goroutine holds references to maps, channels
- Memory grows unbounded
```

**Scenario 2: Slow SSE Subscriber Blocks Publisher**

```
Timeline:
1. Client connects to /sse/scanprogress
2. GetSubscriber(NOTIFICATION_ALL) creates channel
3. Gmail scan starts sending progress updates
4. Client network slows down / freezes
5. ❌ pushToSubscriber() blocks trying to send
6. ❌ processNotifications goroutine STUCK in send
7. Other scans can't send to NOTIFICATION_ALL
8. All scan progress notifications blocked

Result:
- One slow client blocks ALL scan notifications
- System appears frozen
- Other clients don't receive updates
```

**Scenario 3: SSE Client Disconnects, Subscriber Channel Leaks**

```
Timeline:
1. Client connects to /sse/scanprogress
2. GetSubscriber(NOTIFICATION_ALL) creates channel
3. Client disconnects (browser closed, network issue)
4. SSE handler detects clientGone and returns
5. ❌ Subscriber channel NEVER cleaned up
6. ❌ Channel stays in subscribers map
7. ❌ processNotifications keeps trying to send to it

Result:
- 1 subscriber channel leaked per disconnected client
- After 1000 client connections: 1000 leaked channels
- Each send attempt wastes CPU time
- Memory grows with leaked channels
```

**Scenario 4: Abandoned Scan Never Completes**

```
Timeline:
1. Gmail scan starts, GetPublisher("user@gmail.com") creates channel
2. Google API rate limits scan
3. Scan gets stuck indefinitely
4. ❌ No timeout mechanism
5. ❌ processNotifications goroutine runs forever
6. ❌ Never closes even if no activity

Result:
- Goroutine runs forever even with no activity
- Accumulates with stuck scans
- No automatic cleanup
```

### 1.3 Resource Impact

**Memory Leak Calculation:**

```
Per Leaked Goroutine:
- Goroutine stack: ~2-8 KB
- Channel buffers: Unbuffered = minimal
- Map entries: ~32 bytes per entry
- Total per leak: ~10 KB

After 1000 Leaks:
- 1000 goroutines * 10 KB = 10 MB
- Plus indirect references
- Estimated: 20-30 MB

After 10,000 Leaks:
- 10,000 goroutines * 10 KB = 100 MB
- Plus indirect references
- Estimated: 200-300 MB
- Risk of OOM
```

**Goroutine Count Growth:**

```bash
# Check current goroutine count
curl http://localhost:8090/debug/pprof/goroutine?debug=1 | head -20

# Expected without leaks: ~20-50 goroutines
# With leaks after 1 day: 100-500 goroutines
# With leaks after 1 week: 500-5000 goroutines
```

### 1.4 Current vs Target Behavior

| Scenario | Current Behavior | Target Behavior |
|----------|------------------|-----------------|
| **Photos scan completes** | Publisher never closed, goroutine leaks | Publisher closed, goroutine exits |
| **SSE client disconnects** | Subscriber channel leaks | Subscriber cleaned up immediately |
| **Slow SSE client** | Blocks all notifications | Drop messages after 5s timeout |
| **Idle scan (stuck)** | Goroutine runs forever | Timeout after 5 minutes, cleanup |
| **Server shutdown** | Goroutines keep running | All closed gracefully |
| **Monitoring leaks** | Manual inspection only | Use existing count methods |

---

## 2. Target Architecture

### 2.1 Notification Flow with Timeouts

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Scan Starts (Gmail or Photos)                           │
│    - GetPublisher(clientKey) creates channel               │
│    - Starts processNotifications goroutine                  │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. processNotifications Goroutine (Enhanced)               │
│    - Listens to publisher channel with timeout             │
│    - Sends to subscribers with timeout                     │
│    - Exits on channel close OR 5-min idle timeout         │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Push to Subscribers (Non-Blocking)                     │
│    - Attempt send with 5-second timeout                    │
│    - If timeout, log warning and drop message              │
│    - Don't block on slow subscribers                       │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. SSE Clients (Enhanced Cleanup)                         │
│    - Client connects → GetSubscriber creates channel       │
│    - Client disconnects → RemoveSubscriber cleans up       │
│    - Subscriber removed from map immediately               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Scan Completes (Gmail and Photos)                      │
│    - defer close(notificationChannel) in both              │
│    - processNotifications detects close and exits          │
│    - Cleanup subscribers and publishers                    │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 6. Graceful Shutdown (Integration with Issue #8)          │
│    - SIGTERM received                                       │
│    - ShutdownHub() closes all publishers                   │
│    - All processNotifications goroutines exit              │
│    - Clean shutdown                                         │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Component Responsibilities

**notification/hub.go:**
- ✅ Start processNotifications goroutine per publisher
- ✅ Handle channel close detection
- ✅ Implement 5-minute idle timeout
- ✅ Implement 5-second send timeout for slow subscribers
- ✅ Provide RemoveSubscriber() for SSE cleanup
- ✅ Provide ShutdownHub() for graceful shutdown

**collect/gmail.go:**
- ✅ Already closes publisher in logProgress (no changes needed)

**collect/photos.go:**
- ❌ Add logProgress function with defer close (NEW)
- ❌ Or reuse gmail's logProgress pattern

**web/sse.go:**
- ❌ Add RemoveSubscriber() call when client disconnects (NEW)

**main.go:**
- ❌ Call ShutdownHub() during graceful shutdown (Integration with Issue #8)

### 2.3 Timeout Strategy

**Idle Timeout (5 minutes):**
```
If no messages sent to publisher for 5 minutes:
1. Log warning
2. Close publisher channel
3. processNotifications goroutine exits
4. Cleanup resources

Use case: Scan stuck, API rate limited, network issues
```

**Slow Subscriber Timeout (5 seconds):**
```
If subscriber can't receive message within 5 seconds:
1. Log warning with subscriber info
2. Drop message (don't block)
3. Continue processing other messages

Use case: Slow client, network congestion, browser frozen
```

---

## 3. Implementation Details

### 3.1 Enhanced processNotifications: `notification/hub.go`

**Replace existing processNotifications function:**

```go
func processNotifications(clientKey string) {
	// Get publisher channel safely
	globalHub.mu.RLock()
	publisher := globalHub.publishers[clientKey]
	globalHub.mu.RUnlock()

	if publisher == nil {
		slog.Warn("processNotifications started with nil publisher", "client_key", clientKey)
		return
	}

	slog.Info("Starting notification processor", "client_key", clientKey)
	defer slog.Info("Notification processor exited", "client_key", clientKey)

	// Idle timeout ticker (5 minutes)
	idleTimeout := time.NewTimer(5 * time.Minute)
	defer idleTimeout.Stop()

	for {
		// Reset idle timeout on each iteration
		if !idleTimeout.Stop() {
			select {
			case <-idleTimeout.C:
			default:
			}
		}
		idleTimeout.Reset(5 * time.Minute)

		select {
		case progress, ok := <-publisher:
			if !ok {
				// Channel closed, clean exit
				slog.Info("Publisher channel closed, cleaning up",
					"client_key", clientKey)
				cleanupAfterPublisherClose(clientKey)
				return
			}

			// Send to subscribers with timeout
			globalHub.mu.RLock()
			subscriber := globalHub.subscribers[clientKey]
			subscriberAll := globalHub.subscribers[NOTIFICATION_ALL]
			globalHub.mu.RUnlock()

			// Push with timeout (non-blocking)
			pushToSubscriberWithTimeout(subscriber, progress, clientKey)
			pushToSubscriberWithTimeout(subscriberAll, progress, NOTIFICATION_ALL)

		case <-idleTimeout.C:
			// No messages for 5 minutes, cleanup
			slog.Warn("Publisher idle for 5 minutes, closing",
				"client_key", clientKey)

			// Close publisher to trigger cleanup
			globalHub.mu.Lock()
			if ch, exists := globalHub.publishers[clientKey]; exists {
				close(ch)
				delete(globalHub.publishers, clientKey)
			}
			globalHub.mu.Unlock()

			cleanupAfterPublisherClose(clientKey)
			return
		}
	}
}

// cleanupAfterPublisherClose cleans up subscribers when publisher closes
func cleanupAfterPublisherClose(clientKey string) {
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()

	// Close and remove subscriber if it exists
	if ch, exists := globalHub.subscribers[clientKey]; exists {
		close(ch)
		delete(globalHub.subscribers, clientKey)
		slog.Info("Closed subscriber channel", "client_key", clientKey)
	}

	// Remove publisher if still exists
	delete(globalHub.publishers, clientKey)
}

// pushToSubscriberWithTimeout sends to subscriber with 5-second timeout
func pushToSubscriberWithTimeout(subscriber chan<- Progress, progress Progress, subscriberKey string) {
	if subscriber == nil {
		return
	}

	select {
	case subscriber <- progress:
		// Sent successfully
	case <-time.After(5 * time.Second):
		// Subscriber too slow, drop message
		slog.Warn("Subscriber slow, dropping message",
			"subscriber_key", subscriberKey,
			"scan_id", progress.ScanId,
			"client_key", progress.ClientKey)
	}
}
```

### 3.2 Add RemoveSubscriber: `notification/hub.go`

**Add new function for SSE cleanup:**

```go
// RemoveSubscriber removes a subscriber and closes its channel
// This should be called when an SSE client disconnects
func RemoveSubscriber(clientKey string) {
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()

	if ch, exists := globalHub.subscribers[clientKey]; exists {
		close(ch)
		delete(globalHub.subscribers, clientKey)
		slog.Info("Removed subscriber", "client_key", clientKey)
	}
}
```

### 3.3 Add ShutdownHub for Graceful Shutdown: `notification/hub.go`

**Add shutdown method (Integration with Issue #8):**

```go
// ShutdownHub closes all publishers and subscribers for graceful shutdown
// This should be called during application shutdown (SIGTERM/SIGINT)
func ShutdownHub() {
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()

	slog.Info("Shutting down notification hub",
		"publisher_count", len(globalHub.publishers),
		"subscriber_count", len(globalHub.subscribers))

	// Close all publisher channels
	for clientKey, publisher := range globalHub.publishers {
		if publisher != nil {
			close(publisher)
			slog.Info("Closed publisher during shutdown", "client_key", clientKey)
		}
		delete(globalHub.publishers, clientKey)
	}

	// Close all subscriber channels
	for clientKey, subscriber := range globalHub.subscribers {
		if subscriber != nil {
			close(subscriber)
			slog.Info("Closed subscriber during shutdown", "client_key", clientKey)
		}
		delete(globalHub.subscribers, clientKey)
	}

	slog.Info("Notification hub shutdown complete")
}
```

### 3.4 Fix Photos Scan: `collect/photos.go`

**Add logProgress function (same pattern as gmail):**

```go
// Add this function to photos.go (same as gmail.go)
func logProgress(scanId int, clientKey string, done <-chan bool, ticker *time.Ticker, notificationChannel chan<- notification.Progress) {
	defer close(notificationChannel)  // ✅ Ensure publisher closed
	defer slog.Info("Photos scan progress logging stopped",
		"scan_id", scanId,
		"client_key", clientKey)

	for {
		select {
		case <-done:
			// Send final progress notification
			progress := notification.Progress{
				ProcessedCount: int(counter_processed.Load()),
				ActiveCount:    int(counter_pending.Load()),
				ScanId:         scanId,
				ClientKey:      clientKey,
				ElapsedInSec:   int(time.Since(start).Seconds()),
				CompletionPct:  100.0,
			}
			notificationChannel <- progress
			slog.Info("Photos scan completed, final progress sent",
				"scan_id", scanId,
				"processed", progress.ProcessedCount)
			return

		case <-ticker.C:
			// Send periodic progress notification
			processed := int(counter_processed.Load())
			pending := int(counter_pending.Load())
			total := processed + pending
			var completionPct float32
			if total > 0 {
				completionPct = float32(processed) / float32(total) * 100
			}

			elapsed := int(time.Since(start).Seconds())
			var eta int
			if processed > 0 && pending > 0 {
				eta = elapsed * pending / processed
			}

			progress := notification.Progress{
				ProcessedCount: processed,
				ActiveCount:    pending,
				CompletionPct:  completionPct,
				ElapsedInSec:   elapsed,
				EtaInSec:       eta,
				ScanId:         scanId,
				ClientKey:      clientKey,
			}
			notificationChannel <- progress
		}
	}
}
```

**Note:** Photos scan already calls `go logProgress(...)` on line 108, but the function doesn't exist! It's likely using gmail's logProgress which might not be ideal. Creating a photos-specific one is cleaner.

**Alternative approach:** If photos.go is already using gmail's logProgress via import, just ensure the `done` channel is properly signaled and the defer close executes.

### 3.5 Update SSE Handler: `web/sse.go`

**Add cleanup when client disconnects:**

```go
func scanProgressHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)
	subscriber := notification.GetSubscriber(notification.NOTIFICATION_ALL)

	// ✅ NEW: Ensure cleanup when handler exits
	defer notification.RemoveSubscriber(notification.NOTIFICATION_ALL)

	rc := http.NewResponseController(w)
	clientGone := r.Context().Done()
	slog.Info("[scan events] Client Connected.")
	start := time.Now()

	for {
		select {
		case <-clientGone:
			slog.Info(fmt.Sprintf("[scan events] Client disconnected. Connection Duration: %s",
				time.Since(start)))
			// RemoveSubscriber called by defer
			return

		case progress, more := <-subscriber:
			timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)

			if !more {
				// Channel closed (shutdown or cleanup)
				if _, err := fmt.Fprintf(w, "event:close\nretry: 10000\nid:%s\ndata:close at %s \n\n",
					timestamp, time.Now().Format(time.RFC850)); err != nil {
					slog.Warn(fmt.Sprintf("[scan events] Unable to write. err: %s", err.Error()))
				}
				return
			}

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

**Important:** The current implementation uses `NOTIFICATION_ALL` for all clients. If this is intended (broadcast to all), then we can't use `defer RemoveSubscriber(NOTIFICATION_ALL)` because it would remove the shared subscriber.

**Better approach for NOTIFICATION_ALL:**

```go
func scanProgressHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)

	// Generate unique key for this SSE connection
	connectionID := fmt.Sprintf("sse_%d", time.Now().UnixNano())

	// Subscribe with unique ID
	subscriber := notification.GetSubscriber(notification.NOTIFICATION_ALL)

	// ✅ Note: NOTIFICATION_ALL is shared, don't remove it
	// The subscriber channel itself isn't closed here because it's shared
	// The processNotifications goroutine handles cleanup when publisher closes

	rc := http.NewResponseController(w)
	clientGone := r.Context().Done()
	slog.Info("[scan events] Client Connected.", "connection_id", connectionID)
	start := time.Now()

	for {
		select {
		case <-clientGone:
			slog.Info("[scan events] Client disconnected",
				"connection_id", connectionID,
				"duration", time.Since(start))
			return

		case progress, more := <-subscriber:
			// ... existing code ...
		}
	}
}
```

**Actually**, reviewing the code more carefully, each `GetSubscriber(NOTIFICATION_ALL)` call returns the SAME channel (it's cached). So multiple SSE clients share the same channel. This is fine for broadcasting, but means we can't close it per-client.

The current architecture is:
- Each scan has its own publisher (keyed by clientKey/albumId)
- Multiple subscribers can listen to same publisher
- `NOTIFICATION_ALL` is a special subscriber that receives ALL notifications

So the cleanup strategy is:
- When scan completes: close publisher → processNotifications cleans up
- When SSE client disconnects: just stop reading, don't close shared channel
- No per-client cleanup needed for NOTIFICATION_ALL

**Revised SSE handler (no changes needed for NOTIFICATION_ALL):**

The current implementation is actually correct for NOTIFICATION_ALL. The cleanup happens when publishers close.

However, if we want per-client subscribers (not NOTIFICATION_ALL), we'd create unique keys:

```go
func scanProgressHandler(w http.ResponseWriter, r *http.Request) {
	setHeaders(w)

	// Option 1: Use shared NOTIFICATION_ALL (current, no cleanup needed)
	subscriber := notification.GetSubscriber(notification.NOTIFICATION_ALL)

	// Option 2: Use per-client subscriber (would need cleanup)
	// connectionID := fmt.Sprintf("sse_%d", time.Now().UnixNano())
	// subscriber := notification.GetSubscriber(connectionID)
	// defer notification.RemoveSubscriber(connectionID)

	// ... rest stays same ...
}
```

**Decision:** Keep using NOTIFICATION_ALL (shared), no changes needed to SSE handler. The timeout mechanisms in processNotifications will prevent blocking.

### 3.6 Integration with Issue #8 Graceful Shutdown

**Update main.go shutdown sequence:**

This will be documented in detail in Issue #8 implementation, but here's the integration point:

```go
// In main.go (from Issue #8 plan)
func main() {
	// ... database setup ...

	srv := web.StartServer()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	slog.Info("Shutdown signal received", "signal", sig.String())

	// Mark server as shutting down (Issue #8)
	web.MarkShuttingDown()

	// ✅ NEW: Shutdown notification hub (Issue #13)
	notification.ShutdownHub()

	// Shutdown HTTP server with timeout (Issue #8)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	// Database cleanup happens via defer
	slog.Info("Application exited cleanly")
}
```

---

## 4. Testing Strategy

### 4.1 Unit Tests

**`notification/hub_test.go`** (Add to existing or create)

```go
package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessNotificationsIdleTimeout(t *testing.T) {
	// Reset global hub
	globalHub = &Hub{
		publishers:  make(map[string]chan Progress),
		subscribers: make(map[string]chan Progress),
	}

	// Create publisher
	publisher := GetPublisher("test-idle")
	require.NotNil(t, publisher)

	// Wait longer than idle timeout (5 minutes + buffer)
	// Note: This test would take too long, so we'd need to make timeout configurable
	// or test the timeout mechanism separately

	// For now, just verify cleanup happens when channel closed
	close(publisher.(chan Progress))

	// Give processNotifications time to cleanup
	time.Sleep(100 * time.Millisecond)

	// Verify publisher removed
	globalHub.mu.RLock()
	_, exists := globalHub.publishers["test-idle"]
	globalHub.mu.RUnlock()

	assert.False(t, exists, "Publisher should be removed after close")
}

func TestPushToSubscriberWithTimeout(t *testing.T) {
	// Create subscriber channel with no buffer
	subscriber := make(chan Progress)

	// Start goroutine that doesn't read (slow subscriber)
	go func() {
		time.Sleep(10 * time.Second) // Longer than 5s timeout
		<-subscriber
	}()

	// Push should timeout, not block
	start := time.Now()
	pushToSubscriberWithTimeout(subscriber, Progress{ScanId: 123}, "test")
	elapsed := time.Since(start)

	// Should return after ~5 seconds, not block forever
	assert.Less(t, elapsed, 6*time.Second, "Should timeout within 6 seconds")
	assert.Greater(t, elapsed, 4*time.Second, "Should wait at least 4 seconds")
}

func TestRemoveSubscriber(t *testing.T) {
	// Reset global hub
	globalHub = &Hub{
		publishers:  make(map[string]chan Progress),
		subscribers: make(map[string]chan Progress),
	}

	// Create subscriber
	subscriber := GetSubscriber("test-remove")
	require.NotNil(t, subscriber)

	// Verify exists
	globalHub.mu.RLock()
	_, exists := globalHub.subscribers["test-remove"]
	globalHub.mu.RUnlock()
	assert.True(t, exists)

	// Remove subscriber
	RemoveSubscriber("test-remove")

	// Verify removed
	globalHub.mu.RLock()
	_, exists = globalHub.subscribers["test-remove"]
	globalHub.mu.RUnlock()
	assert.False(t, exists)

	// Channel should be closed
	_, ok := <-subscriber
	assert.False(t, ok, "Channel should be closed")
}

func TestShutdownHub(t *testing.T) {
	// Reset global hub
	globalHub = &Hub{
		publishers:  make(map[string]chan Progress),
		subscribers: make(map[string]chan Progress),
	}

	// Create multiple publishers and subscribers
	pub1 := GetPublisher("pub1")
	pub2 := GetPublisher("pub2")
	sub1 := GetSubscriber("sub1")
	sub2 := GetSubscriber("sub2")

	// Verify created
	assert.Equal(t, 2, globalHub.GetPublisherCount())
	assert.Equal(t, 2, globalHub.GetSubscriberCount())

	// Shutdown
	ShutdownHub()

	// Verify all closed and removed
	assert.Equal(t, 0, globalHub.GetPublisherCount())
	assert.Equal(t, 0, globalHub.GetSubscriberCount())

	// Channels should be closed
	_, ok1 := <-pub1
	_, ok2 := <-pub2
	_, ok3 := <-sub1
	_, ok4 := <-sub2

	assert.False(t, ok1, "pub1 should be closed")
	assert.False(t, ok2, "pub2 should be closed")
	assert.False(t, ok3, "sub1 should be closed")
	assert.False(t, ok4, "sub2 should be closed")
}
```

### 4.2 Integration Tests

**Test Photos Scan Cleanup:**

```bash
# Start server
go run .

# Trigger photos scan
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "GPhotos",
    "GPhotosScan": {
      "AlbumId": "test-album",
      "RefreshToken": "...",
      "FetchSize": false,
      "FetchMd5Hash": false
    }
  }'

# Monitor goroutine count
watch -n 5 'curl -s http://localhost:8090/debug/pprof/goroutine?debug=1 | grep "goroutine profile" | head -1'

# Expected: Count stable after scan completes
# Before fix: Count grows by 1 per photos scan
# After fix: Count returns to baseline
```

**Test Slow Subscriber Timeout:**

```bash
# Connect SSE client that doesn't read
curl -N http://localhost:8090/sse/scanprogress > /dev/null &
SSE_PID=$!

# Suspend the process (simulates slow client)
kill -STOP $SSE_PID

# Start scan (will generate notifications)
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/tmp/test"}}'

# Check logs for timeout warnings
tail -f logs/app.log | grep "Subscriber slow"

# Expected: See "Subscriber slow, dropping message" warnings
# Expected: Scan completes despite slow subscriber

# Cleanup
kill -KILL $SSE_PID
```

**Test Idle Timeout:**

```bash
# This is hard to test manually (5 minute wait)
# Better to unit test or reduce timeout in test environment

# If testing manually:
# 1. Start publisher without closing it
# 2. Wait 5 minutes
# 3. Check logs for "Publisher idle for 5 minutes, closing"
```

### 4.3 Goroutine Leak Detection

**Monitor goroutines over time:**

```bash
#!/bin/bash
# monitor_goroutines.sh

echo "Monitoring goroutine count over time..."
echo "Timestamp,Goroutines,Publishers,Subscribers" > goroutine_stats.csv

while true; do
  TIMESTAMP=$(date +%s)

  # Get goroutine count from pprof
  GOROUTINES=$(curl -s http://localhost:8090/debug/pprof/goroutine?debug=1 | \
    grep "goroutine profile" | \
    awk '{print $4}' | \
    sed 's/://g')

  # Get publisher/subscriber counts (would need API endpoint)
  # For now, just log goroutines

  echo "$TIMESTAMP,$GOROUTINES,0,0" >> goroutine_stats.csv
  echo "$(date): $GOROUTINES goroutines"

  sleep 60  # Check every minute
done
```

**Analyze results:**

```bash
# Plot goroutine growth
gnuplot <<EOF
set datafile separator ","
set xlabel "Time"
set ylabel "Goroutine Count"
set title "Goroutine Count Over Time"
plot "goroutine_stats.csv" using 1:2 with lines title "Goroutines"
pause -1
EOF
```

**Expected Results:**
- Before fix: Linear growth (1 goroutine per photos scan)
- After fix: Stable baseline (small fluctuations only)

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Update notification/hub.go with enhanced processNotifications
- [ ] Add RemoveSubscriber function
- [ ] Add ShutdownHub function
- [ ] Add logProgress to collect/photos.go (or verify it uses gmail's)
- [ ] Update main.go to call ShutdownHub (after Issue #8 implemented)
- [ ] Unit tests passing
- [ ] Manual integration tests completed
- [ ] Goroutine monitoring in place

### 5.2 Deployment Steps

**Step 1: Update notification/hub.go**

```bash
# Make changes to processNotifications, add new functions
# Verify builds
go build .
```

**Step 2: Fix Photos Scan**

```bash
# Add logProgress to collect/photos.go
# Or verify existing usage is correct
go build .
```

**Step 3: Test in Development**

```bash
# Start server
./hdd &

# Run multiple scans
for i in {1..10}; do
  curl -X POST http://localhost:8090/api/scans \
    -H "Content-Type: application/json" \
    -d '{"ScanType":"GPhotos","GPhotosScan":{...}}'
  sleep 5
done

# Check goroutine count
curl http://localhost:8090/debug/pprof/goroutine?debug=1 | head -20

# Should be stable, not growing
```

**Step 4: Deploy to Staging**

```bash
# Build
go build -o hdd

# Deploy
scp hdd staging:/opt/bhandaar/
ssh staging 'systemctl restart bhandaar'

# Monitor
ssh staging 'journalctl -u bhandaar -f | grep -E "notification|goroutine|idle|slow"'
```

**Step 5: Monitor Staging**

```bash
# Run goroutine monitoring script for 24 hours
./monitor_goroutines.sh

# Check for:
# - Stable goroutine count
# - No "Subscriber slow" warnings (unless actually slow)
# - Publishers cleaning up after scans
```

**Step 6: Deploy to Production**

```bash
# Tag release
git tag -a v1.x.x -m "Fix goroutine leaks in notification system (Issue #13)"
git push origin v1.x.x

# Build and deploy
go build -o hdd
docker build -t jyothri/hdd-go-build:v1.x.x .
docker push jyothri/hdd-go-build:v1.x.x

# Kubernetes deployment
kubectl set image deployment/bhandaar-backend backend=jyothri/hdd-go-build:v1.x.x
kubectl rollout status deployment/bhandaar-backend
```

**Step 7: Post-Deployment Monitoring**

```bash
# Watch goroutine count
watch -n 60 'kubectl exec -it <pod> -- curl localhost:8090/debug/pprof/goroutine?debug=1 | grep "goroutine profile"'

# Check logs for issues
kubectl logs -f deployment/bhandaar-backend | grep -E "notification|idle|slow"

# Monitor for 1 week to confirm no leaks
```

### 5.3 Rollback Plan

**If goroutine leaks detected:**

```bash
# Kubernetes
kubectl rollout undo deployment/bhandaar-backend

# systemd
ssh production 'systemctl stop bhandaar'
ssh production 'cp /opt/bhandaar/hdd.backup /opt/bhandaar/hdd'
ssh production 'systemctl start bhandaar'
```

---

## 6. Integration with Issue #8

### 6.1 Shutdown Sequence

**From Issue #8 Plan, add notification shutdown:**

```go
// main.go
func main() {
	// ... setup ...

	srv := web.StartServer()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	slog.Info("Shutdown signal received", "signal", sig.String())

	// 1. Mark server as shutting down (Issue #8)
	web.MarkShuttingDown()

	// 2. Shutdown notification hub (Issue #13) - NEW
	notification.ShutdownHub()

	// 3. Notify SSE clients about shutdown (Issue #8)
	notification.NotifyShutdown()  // From Issue #8 plan

	// 4. Shutdown HTTP server (Issue #8)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
		srv.Close()
	}

	slog.Info("Application exited cleanly")
}
```

### 6.2 Coordination with Issue #8

**Issue #8 provides:**
- NotifyShutdown() - sends shutdown event to SSE clients
- 30-second graceful shutdown window
- Health check returns 503 during shutdown

**Issue #13 adds:**
- ShutdownHub() - closes all publishers/subscribers
- Ensures processNotifications goroutines exit
- Prevents new notifications during shutdown

**Combined effect:**
1. Signal received
2. Health check returns 503 (Issue #8)
3. ShutdownHub() closes all channels (Issue #13)
4. NotifyShutdown() tells SSE clients (Issue #8)
5. HTTP server stops accepting requests (Issue #8)
6. Existing requests complete within 30s (Issue #8)
7. All goroutines exit cleanly (Issue #13)
8. Database closes (existing defer)

### 6.3 Testing Shutdown Integration

```bash
# Start server
./hdd &
PID=$!

# Connect SSE client
curl -N http://localhost:8090/sse/scanprogress &
SSE_PID=$!

# Start long scan
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{"ScanType":"Local","LocalScan":{"Path":"/large/directory"}}'

# Wait a bit
sleep 5

# Send SIGTERM
kill -TERM $PID

# Expected logs:
# - "Shutdown signal received"
# - "Shutting down notification hub"
# - "Closed publisher during shutdown"
# - "Notification hub shutdown complete"
# - "Server shutdown completed gracefully"
# - "Application exited cleanly"

# SSE client should receive shutdown event
# Check SSE_PID output for "event:shutdown"
```

---

## 7. Monitoring and Observability

### 7.1 Log Patterns to Watch

**Normal Operation:**
```
INFO Starting notification processor client_key=user@gmail.com
INFO Notification processor exited client_key=user@gmail.com
```

**Idle Timeout (Warning):**
```
WARN Publisher idle for 5 minutes, closing client_key=abandoned-scan
INFO Closed subscriber channel client_key=abandoned-scan
```

**Slow Subscriber (Warning):**
```
WARN Subscriber slow, dropping message subscriber_key=NOTIFICATION_ALL scan_id=123
```

**Graceful Shutdown:**
```
INFO Shutting down notification hub publisher_count=3 subscriber_count=2
INFO Closed publisher during shutdown client_key=user1@gmail.com
INFO Closed publisher during shutdown client_key=user2@gmail.com
INFO Notification hub shutdown complete
```

### 7.2 Using Existing Monitoring Methods

**Check active publishers/subscribers:**

```go
// In any handler or health check
publisherCount := globalHub.GetPublisherCount()
subscriberCount := globalHub.GetSubscriberCount()

slog.Info("Notification hub status",
	"publishers", publisherCount,
	"subscribers", subscriberCount)
```

**Add to health check (optional):**

```go
func HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"ok": true,
		"notification_hub": map[string]int{
			"publishers":  globalHub.GetPublisherCount(),
			"subscribers": globalHub.GetSubscriberCount(),
		},
	}
	writeJSONResponseOK(w, health)
}
```

### 7.3 Goroutine Monitoring

**Manual check:**
```bash
# Check goroutine count
curl http://localhost:8090/debug/pprof/goroutine?debug=1 | grep "goroutine profile"

# Get detailed goroutine dump
curl http://localhost:8090/debug/pprof/goroutine?debug=2 > goroutines.txt
```

**Expected baseline:**
- Small deployment: 20-50 goroutines
- With active scans: +1 per publisher
- After scans complete: back to baseline

**Red flags:**
- Goroutine count continuously growing
- Count doesn't return to baseline after scans
- Hundreds of `processNotifications` goroutines

---

## Appendix A: Complete File Changes Summary

### Files to Modify

1. **`notification/hub.go`**
   - Replace `processNotifications()` function with timeout logic
   - Add `cleanupAfterPublisherClose()` helper
   - Add `pushToSubscriberWithTimeout()` helper
   - Add `RemoveSubscriber()` function
   - Add `ShutdownHub()` function

2. **`collect/photos.go`**
   - Add `logProgress()` function (if not already present)
   - Ensure `defer close(notificationChannel)` in logProgress
   - Or verify existing logProgress usage is correct

3. **`main.go`** (After Issue #8 implemented)
   - Add `notification.ShutdownHub()` call in shutdown sequence

4. **`web/sse.go`** (Optional - if per-client subscribers needed)
   - Add cleanup for per-client subscribers
   - Current NOTIFICATION_ALL usage is fine as-is

### Files to Create

**`notification/hub_test.go`** - Add tests (or add to existing)
- TestProcessNotificationsIdleTimeout
- TestPushToSubscriberWithTimeout
- TestRemoveSubscriber
- TestShutdownHub

---

## Appendix B: Troubleshooting Guide

### Problem: Goroutines still leaking after fix

**Diagnosis:**
```bash
# Get goroutine dump
curl http://localhost:8090/debug/pprof/goroutine?debug=2 > dump.txt

# Look for processNotifications
grep -A 5 "processNotifications" dump.txt

# Check if stuck
# Should see exit path being taken, not blocked on channel receive
```

**Common Causes:**
1. Publisher channel never closed
2. Idle timeout not triggering
3. New leak source (different from notification)

**Solution:**
- Verify all scan types close publisher
- Check logs for timeout messages
- Profile to find leak source

### Problem: Messages being dropped frequently

**Diagnosis:**
```bash
# Check logs for slow subscriber warnings
grep "Subscriber slow" /var/log/bhandaar/app.log | wc -l

# If many warnings, identify slow client
grep "Subscriber slow" /var/log/bhandaar/app.log | tail -20
```

**Common Causes:**
1. Client network slow/congested
2. Client CPU overloaded
3. Timeout too short (5s)

**Solution:**
- If timeout too short, could increase to 10s
- Add client-side buffering
- Investigate client performance

### Problem: Idle timeout too aggressive

**Diagnosis:**
```bash
# Check for premature closures
grep "Publisher idle" /var/log/bhandaar/app.log

# Check if scans actually still active
# Compare with scan completion times
```

**Common Causes:**
1. Scan legitimately slow (API rate limits)
2. 5-minute timeout too short for large scans

**Solution:**
- Increase timeout to 10 minutes if needed
- Or make timeout configurable per scan type

---

**END OF DOCUMENT**
