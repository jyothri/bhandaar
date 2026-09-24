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
	close(pub)
}

func TestPublishingNeverBlocks(t *testing.T) {
	// A subscriber that never reads must not stall the publisher (and so the scan).
	_, unsub := Subscribe(NOTIFICATION_ALL)
	defer unsub()

	pub := GetPublisher("never-blocks")
	for i := 0; i < subscriberBuffer*3; i++ {
		publish(t, pub, Progress{ScanId: i})
	}
	close(pub)
}

func TestPublishingWithNoSubscribers(t *testing.T) {
	pub := GetPublisher("no-subscribers")
	publish(t, pub, Progress{ScanId: 1})
	close(pub)
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
	close(pub)

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
	close(pub)
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
	close(pub)
}
