package notification

import "sync"

const NOTIFICATION_ALL string = "all"

// Hub manages progress notifications with thread-safe map access
type Hub struct {
	publishers  map[string]chan Progress
	subscribers map[string]chan Progress
	mu          sync.RWMutex
}

var globalHub *Hub

func init() {
	globalHub = &Hub{
		publishers:  make(map[string]chan Progress),
		subscribers: make(map[string]chan Progress),
	}
}

func GetPublisher(clientKey string) chan<- Progress {
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()

	if globalHub.publishers[clientKey] == nil {
		globalHub.publishers[clientKey] = make(chan Progress)
		go processNotifications(clientKey)
	}
	return globalHub.publishers[clientKey]
}

func GetSubscriber(clientKey string) <-chan Progress {
	globalHub.mu.Lock()
	defer globalHub.mu.Unlock()

	if globalHub.subscribers[clientKey] == nil {
		globalHub.subscribers[clientKey] = make(chan Progress)
	}
	return globalHub.subscribers[clientKey]
}

func processNotifications(clientKey string) {
	// Get publisher channel safely
	globalHub.mu.RLock()
	publisher := globalHub.publishers[clientKey]
	globalHub.mu.RUnlock()

	if publisher == nil {
		return
	}

	for progress := range publisher {
		// Get subscribers safely for each notification
		globalHub.mu.RLock()
		subscriber := globalHub.subscribers[clientKey]
		subscriberAll := globalHub.subscribers[NOTIFICATION_ALL]
		globalHub.mu.RUnlock()

		pushToSubscriber(subscriber, progress)
		pushToSubscriber(subscriberAll, progress)
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
	subscriber <- progress
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

func (h *Hub) ClosePublisher(clientKey string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ch, exists := h.publishers[clientKey]; exists {
		close(ch)
		delete(h.publishers, clientKey)
	}
}

func (h *Hub) GetPublisherCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.publishers)
}

func (h *Hub) GetSubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
