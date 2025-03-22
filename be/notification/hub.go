package notification

const NOTIFICATION_ALL string = "all"

var publishers map[string]chan Progress
var subscribers map[string]chan Progress

func init() {
	publishers = make(map[string]chan Progress)
	subscribers = make(map[string]chan Progress)
}

func GetPublisher(clientKey string) chan<- Progress {
	if publishers[clientKey] == nil {
		publishers[clientKey] = make(chan Progress)
		go processNotifications(clientKey)
	}
	return publishers[clientKey]
}

func GetSubscriber(clientKey string) <-chan Progress {
	if subscribers[clientKey] == nil {
		subscribers[clientKey] = make(chan Progress)
	}
	return subscribers[clientKey]
}

func processNotifications(clientKey string) {
	for progress := range publishers[clientKey] {
		pushToSubscriber(subscribers[clientKey], progress)
		pushToSubscriber(subscribers[NOTIFICATION_ALL], progress)
	}
	if subscribers[clientKey] != nil {
		close(subscribers[clientKey])
		delete(subscribers, clientKey)
	}
	delete(publishers, clientKey)
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
