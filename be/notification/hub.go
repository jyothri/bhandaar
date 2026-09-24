package notification

import (
	"log/slog"
	"sync"
)

const NOTIFICATION_ALL string = "all"

// subscriberBuffer is how many progress updates a slow subscriber can fall
// behind before its oldest queued update is discarded. Publishing never blocks.
const subscriberBuffer = 16

// Hub fans progress notifications out to every subscriber. Each Subscribe
// call gets its own channel, so multiple SSE clients all see every update.
type Hub struct {
	publishers  map[string]chan Progress
	subscribers map[string]map[chan Progress]struct{}
	mu          sync.RWMutex
}

var globalHub *Hub

func init() {
	globalHub = &Hub{
		publishers:  make(map[string]chan Progress),
		subscribers: make(map[string]map[chan Progress]struct{}),
	}
}

// GetPublisher returns a new channel for publishing progress for clientKey.
// The caller must close it when done; that closes clientKey's subscribers.
func GetPublisher(clientKey string) chan<- Progress {
	publisher := make(chan Progress)

	globalHub.mu.Lock()
	globalHub.publishers[clientKey] = publisher
	globalHub.mu.Unlock()

	go processNotifications(clientKey, publisher)
	return publisher
}

// Subscribe registers a new subscriber for clientKey (or NOTIFICATION_ALL)
// and returns its channel plus a function that unsubscribes. The channel is
// closed on unsubscribe, or when clientKey's publisher closes.
func Subscribe(clientKey string) (<-chan Progress, func()) {
	ch := make(chan Progress, subscriberBuffer)

	globalHub.mu.Lock()
	if globalHub.subscribers[clientKey] == nil {
		globalHub.subscribers[clientKey] = make(map[chan Progress]struct{})
	}
	globalHub.subscribers[clientKey][ch] = struct{}{}
	globalHub.mu.Unlock()

	unsubscribe := func() {
		globalHub.mu.Lock()
		defer globalHub.mu.Unlock()
		// Whoever removes the channel from the set closes it, under the lock,
		// so it is closed exactly once and never sent to after closing.
		if subs, ok := globalHub.subscribers[clientKey]; ok {
			if _, ok := subs[ch]; ok {
				delete(subs, ch)
				close(ch)
				if len(subs) == 0 {
					delete(globalHub.subscribers, clientKey)
				}
			}
		}
	}
	return ch, unsubscribe
}

func processNotifications(clientKey string, publisher <-chan Progress) {
	for progress := range publisher {
		broadcast(clientKey, progress)
		broadcast(NOTIFICATION_ALL, progress)
	}

	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()

	// Close subscribers of this clientKey; NOTIFICATION_ALL subscribers stay.
	for ch := range globalHub.subscribers[clientKey] {
		close(ch)
	}
	delete(globalHub.subscribers, clientKey)

	// A newer publisher may have replaced this one for the same clientKey.
	if globalHub.publishers[clientKey] == publisher {
		delete(globalHub.publishers, clientKey)
	}
}

// broadcast sends progress to every subscriber of key without blocking.
// Holding the read lock keeps subscribers from being closed mid-send.
func broadcast(key string, progress Progress) {
	globalHub.mu.RLock()
	defer globalHub.mu.RUnlock()

	for ch := range globalHub.subscribers[key] {
		select {
		case ch <- progress:
		default:
			// Buffer full. Updates are cumulative snapshots, so discard the
			// oldest queued one rather than this newer one; otherwise a lagging
			// client could miss a scan's final event and show stale counts.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- progress:
			default:
				slog.Warn("Dropping progress update for slow subscriber",
					"subscription", key,
					"scan_id", progress.ScanId)
			}
		}
	}
}

type Progress struct {
	ClientKey      string  `json:"client_key"`
	ProcessedCount int     `json:"processed_count"`
	ActiveCount    int     `json:"active_count"`
	CompletionPct  float32 `json:"completion_pct"`
	ElapsedInSec   int     `json:"elapsed_in_sec"`
	EtaInSec       int     `json:"eta_in_sec"`
	ScanId         int     `json:"scan_id"`
}

// Helper methods for monitoring and management

func (h *Hub) GetPublisherCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.publishers)
}

func (h *Hub) GetSubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	count := 0
	for _, subs := range h.subscribers {
		count += len(subs)
	}
	return count
}
