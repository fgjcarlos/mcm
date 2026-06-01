package server

import (
	"sync"
	"testing"
	"time"
)

// TestBrokerEventHubConcurrentPublishAndSnapshot exercises the message-publish
// path and Snapshot concurrently with a persistence store attached. It guards
// the invariant that Snapshot must not run the store query while holding h.mu:
// under -race this surfaces data races, and a lock-ordering regression that
// deadlocked would hang the test.
func TestBrokerEventHubConcurrentPublishAndSnapshot(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	hub := app.brokerEvents // persistence is wired by New

	const publishers = 4
	const perPublisher = 200

	var wg sync.WaitGroup
	wg.Add(publishers + 2)

	for p := 0; p < publishers; p++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				hub.Publish(BrokerEvent{Type: "topic_message", Topic: "factory/line/x", ObservedAt: time.Now().UTC()})
			}
		}()
	}
	for s := 0; s < 2; s++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perPublisher; i++ {
				_ = hub.Snapshot()
			}
		}()
	}
	wg.Wait()

	snap := hub.Snapshot()
	if snap.TopicMessages != publishers*perPublisher {
		t.Fatalf("TopicMessages = %d, want %d", snap.TopicMessages, publishers*perPublisher)
	}
}
