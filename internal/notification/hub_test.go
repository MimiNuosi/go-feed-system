package notification

import (
	"testing"
	"time"
)

func TestHubRoutesEventsByUser(t *testing.T) {
	hub := NewHub()
	userEvents, cancelUser := hub.Subscribe(2)
	defer cancelUser()
	otherEvents, cancelOther := hub.Subscribe(3)
	defer cancelOther()

	hub.Broadcast(Item{ID: 1, UserID: 2})
	hub.Broadcast(Item{ID: 2, UserID: 3})

	if got := receiveHubItem(t, userEvents); got.ID != 1 {
		t.Fatalf("user 2 received id %d, want 1", got.ID)
	}
	if got := receiveHubItem(t, otherEvents); got.ID != 2 {
		t.Fatalf("user 3 received id %d, want 2", got.ID)
	}
}

func TestHubDropsEventForSlowSubscriber(t *testing.T) {
	hub := NewHub()
	events, cancel := hub.Subscribe(1)
	defer cancel()

	for i := 0; i < 17; i++ {
		hub.Broadcast(Item{ID: uint64(i + 1), UserID: 1})
	}

	// 前 16 条进入缓冲，第 17 条被丢弃；Broadcast 本身不能阻塞。
	done := make(chan struct{})
	go func() {
		hub.Broadcast(Item{ID: 100, UserID: 1})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked on slow subscriber")
	}
	if len(events) != 16 {
		t.Fatalf("buffered events = %d, want 16", len(events))
	}
}

func TestHubCancelIsIdempotent(t *testing.T) {
	hub := NewHub()
	events, cancel := hub.Subscribe(1)
	cancel()
	cancel()

	if _, ok := <-events; ok {
		t.Fatal("expected subscription channel to be closed")
	}
}

func receiveHubItem(t *testing.T, events <-chan Item) Item {
	t.Helper()
	select {
	case item := <-events:
		return item
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hub event")
		return Item{}
	}
}
