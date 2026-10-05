package outbox

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"go-feed-system/pkg/rabbitmq"
)

var errMessagePublish = errors.New("message publish failed")

type fakeMessagePublisher struct {
	publishFunc func(ctx context.Context, message rabbitmq.Message) error

	callCount   int
	lastContext context.Context
	lastMessage rabbitmq.Message
}

func (f *fakeMessagePublisher) Publish(ctx context.Context, message rabbitmq.Message) error {
	f.callCount++
	f.lastContext = ctx
	f.lastMessage = message
	if f.publishFunc != nil {
		return f.publishFunc(ctx, message)
	}
	return nil
}

func TestRabbitMQPublisher_PublishMapsEvent(t *testing.T) {
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "outbox-test")

	createdAt := time.Now().UTC().Truncate(time.Millisecond)
	event := Event{
		ID:          100,
		EventID:     "event-100",
		EventType:   EventTypeVideoPublished,
		AggregateID: 200,
		Payload:     []byte(`{"video_id":200,"author_id":10}`),
		Status:      StatusPending,
		RetryCount:  3,
		LastError:   "previous failure",
		CreatedAt:   createdAt,
	}

	publisher := &fakeMessagePublisher{}
	adapter := NewRabbitMQPublisher(publisher)

	if err := adapter.Publish(ctx, event); err != nil {
		t.Fatalf("publish event: %v", err)
	}
	if publisher.callCount != 1 {
		t.Fatalf("expected one publish call, got %d", publisher.callCount)
	}
	if publisher.lastContext != ctx {
		t.Error("expected adapter to pass the original context")
	}

	got := publisher.lastMessage
	if got.MessageID != event.EventID {
		t.Errorf("expected message ID %q, got %q", event.EventID, got.MessageID)
	}
	if got.Type != event.EventType {
		t.Errorf("expected type %q, got %q", event.EventType, got.Type)
	}
	if !bytes.Equal(got.Body, event.Payload) {
		t.Errorf("expected body %s, got %s", event.Payload, got.Body)
	}
	if !got.Timestamp.Equal(event.CreatedAt) {
		t.Errorf("expected timestamp %v, got %v", event.CreatedAt, got.Timestamp)
	}
}

func TestRabbitMQPublisher_PropagatesPublishError(t *testing.T) {
	publisher := &fakeMessagePublisher{
		publishFunc: func(ctx context.Context, message rabbitmq.Message) error {
			return errMessagePublish
		},
	}
	adapter := NewRabbitMQPublisher(publisher)

	err := adapter.Publish(context.Background(), Event{
		EventID:   "event-1",
		EventType: EventTypeVideoPublished,
		Payload:   []byte(`{"video_id":1}`),
		CreatedAt: time.Now(),
	})
	if !errors.Is(err, errMessagePublish) {
		t.Fatalf("expected wrapped publish error, got %v", err)
	}
	if publisher.callCount != 1 {
		t.Fatalf("expected one publish call, got %d", publisher.callCount)
	}
}

func TestRabbitMQPublisher_NilPublisher(t *testing.T) {
	adapter := NewRabbitMQPublisher(nil)

	err := adapter.Publish(context.Background(), Event{
		EventID:   "event-1",
		EventType: EventTypeVideoPublished,
		Payload:   []byte(`{"video_id":1}`),
		CreatedAt: time.Now(),
	})
	if err == nil {
		t.Fatal("expected nil publisher to return an error")
	}
}
