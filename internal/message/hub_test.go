package message

import (
	"testing"
	"time"
)

func TestHubRoutesMessageToReceiver(t *testing.T) {
	hub := NewHub()
	receiverEvents, cancelReceiver := hub.Subscribe(2)
	defer cancelReceiver()
	otherEvents, cancelOther := hub.Subscribe(3)
	defer cancelOther()

	hub.Broadcast(Item{ID: 1, SenderID: 1, ReceiverID: 2})
	hub.Broadcast(Item{ID: 2, SenderID: 1, ReceiverID: 3})

	if got := receiveMessageEvent(t, receiverEvents); got.ID != 1 {
		t.Fatalf("receiver got message %d, want 1", got.ID)
	}
	if got := receiveMessageEvent(t, otherEvents); got.ID != 2 {
		t.Fatalf("other user got message %d, want 2", got.ID)
	}
}

func TestHubDropsMessageForSlowSubscriber(t *testing.T) {
	hub := NewHub()
	events, cancel := hub.Subscribe(1)
	defer cancel()

	for i := 0; i < 33; i++ {
		hub.Broadcast(Item{ID: uint64(i + 1), ReceiverID: 1})
	}

	done := make(chan struct{})
	go func() {
		hub.Broadcast(Item{ID: 100, ReceiverID: 1})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Broadcast blocked on slow subscriber")
	}
	if len(events) != 32 {
		t.Fatalf("buffered messages = %d, want 32", len(events))
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

func receiveMessageEvent(t *testing.T, events <-chan Item) Item {
	t.Helper()
	select {
	case item := <-events:
		return item
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message event")
		return Item{}
	}
}
