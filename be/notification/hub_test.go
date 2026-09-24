package notification

import (
	"testing"
	"time"
)

const timeout = time.Second

func receive(t *testing.T, ch <-chan Progress) Progress {
	t.Helper()
	select {
	case p, ok := <-ch:
		if !ok {
			t.Fatal("channel closed, expected a progress update")
		}
		return p
	case <-time.After(timeout):
		t.Fatal("timed out waiting for a progress update")
	}
	return Progress{}
}

func expectClosed(t *testing.T, ch <-chan Progress) {
	t.Helper()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed, got an update")
		}
	case <-time.After(timeout):
		t.Fatal("timed out waiting for channel to close")
	}
}

// publish sends p and fails the test if the hub blocks the publisher.
func publish(t *testing.T, pub chan<- Progress, p Progress) {
	t.Helper()
	select {
	case pub <- p:
	case <-time.After(timeout):
		t.Fatal("publisher blocked")
	}
}

// waitPublisherGone waits until processNotifications has finished with key's
// publisher; removing it from the map is the last thing it does.
func waitPublisherGone(t *testing.T, key string) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		globalHub.mu.RLock()
		_, exists := globalHub.publishers[key]
		globalHub.mu.RUnlock()
		if !exists {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("publisher %q still registered", key)
		case <-time.After(time.Millisecond):
		}
	}
}

// closeAndWait closes a test's publisher and waits until the hub is done with
// it, so its broadcasts can't leak into later tests' NOTIFICATION_ALL
// subscribers.
func closeAndWait(t *testing.T, pub chan<- Progress, key string) {
	t.Helper()
	close(pub)
	waitPublisherGone(t, key)
}

func TestEverySubscriberReceivesEveryUpdate(t *testing.T) {
	a, unsubA := Subscribe(NOTIFICATION_ALL)
	defer unsubA()
	b, unsubB := Subscribe(NOTIFICATION_ALL)
	defer unsubB()

	pub := GetPublisher("every-subscriber")
	for i := 1; i <= 3; i++ {
		publish(t, pub, Progress{ScanId: i})
		for _, ch := range []<-chan Progress{a, b} {
			if got := receive(t, ch).ScanId; got != i {
				t.Fatalf("got scan %d, want %d", got, i)
			}
		}
	}
	closeAndWait(t, pub, "every-subscriber")
}

func TestPublishingNeverBlocks(t *testing.T) {
	// A subscriber that never reads must not stall the publisher (and so the scan).
	_, unsub := Subscribe(NOTIFICATION_ALL)
	defer unsub()

	pub := GetPublisher("never-blocks")
	for i := 0; i < subscriberBuffer*3; i++ {
		publish(t, pub, Progress{ScanId: i})
	}
	closeAndWait(t, pub, "never-blocks")
}

func TestFullBufferKeepsNewestUpdate(t *testing.T) {
	// Progress is a cumulative snapshot: a lagging client must still end up
	// with the latest one (e.g. a scan's final event), not stale counts.
	// Subscribe to this publisher's own key (not NOTIFICATION_ALL, which other
	// tests' publishers may still be broadcasting to). The hub closes this
	// channel once the publisher closes, so draining it needs no timing.
	ch, unsub := Subscribe("keeps-newest")
	defer unsub()

	pub := GetPublisher("keeps-newest")
	const total = subscriberBuffer * 3
	for i := 1; i <= total; i++ {
		publish(t, pub, Progress{ScanId: i})
	}
	closeAndWait(t, pub, "keeps-newest")

	var got []int
	deadline := time.After(timeout)
	for done := false; !done; {
		select {
		case p, ok := <-ch:
			if !ok {
				done = true
				break
			}
			got = append(got, p.ScanId)
		case <-deadline:
			t.Fatal("timed out waiting for the subscriber channel to close")
		}
	}
	if len(got) != subscriberBuffer {
		t.Fatalf("got %d buffered updates, want %d", len(got), subscriberBuffer)
	}
	if last := got[len(got)-1]; last != total {
		t.Fatalf("newest update in buffer is %d, want %d (got %v)", last, total, got)
	}
}

func TestPublishingWithNoSubscribers(t *testing.T) {
	pub := GetPublisher("no-subscribers")
	publish(t, pub, Progress{ScanId: 1})
	closeAndWait(t, pub, "no-subscribers")
}

func TestPublisherCloseClosesOnlyItsOwnSubscribers(t *testing.T) {
	own, unsubOwn := Subscribe("closing-key")
	defer unsubOwn() // must be safe after the hub already closed it
	all, unsubAll := Subscribe(NOTIFICATION_ALL)
	defer unsubAll()

	pub := GetPublisher("closing-key")
	publish(t, pub, Progress{ScanId: 7})
	receive(t, own)
	receive(t, all)
	closeAndWait(t, pub, "closing-key")

	expectClosed(t, own)
	select {
	case _, ok := <-all:
		if !ok {
			t.Fatal("NOTIFICATION_ALL subscriber was closed by a scan's publisher")
		}
	case <-time.After(50 * time.Millisecond):
	}
}

func TestUnsubscribeStopsDeliveryAndCloses(t *testing.T) {
	ch, unsub := Subscribe(NOTIFICATION_ALL)
	unsub()
	unsub() // idempotent
	expectClosed(t, ch)

	pub := GetPublisher("after-unsubscribe")
	publish(t, pub, Progress{ScanId: 1})
	closeAndWait(t, pub, "after-unsubscribe")
}

func TestConcurrentSubscribeAndPublish(t *testing.T) {
	pub := GetPublisher("concurrent")
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			ch, unsub := Subscribe(NOTIFICATION_ALL)
			select {
			case <-ch:
			default:
			}
			unsub()
		}
	}()
	for i := 0; i < 200; i++ {
		publish(t, pub, Progress{ScanId: i})
	}
	<-done
	closeAndWait(t, pub, "concurrent")
}
