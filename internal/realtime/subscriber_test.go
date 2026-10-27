package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeSubscriberMetrics struct {
	results map[string]int
}

func (m *fakeSubscriberMetrics) IncRealtimeReceive(channel string, result string) {
	if m.results == nil {
		m.results = make(map[string]int)
	}
	m.results[result]++
}

func (m *fakeSubscriberMetrics) IncRealtimeReconnect(result string) {}

func (m *fakeSubscriberMetrics) SetRealtimeSubscriberConnected(connected bool) {}

func TestSubscriberHandleMessage(t *testing.T) {
	event, err := NewEnvelope(
		EventNotificationCreated,
		2,
		map[string]any{"id": 7, "kind": "like"},
	)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}

	metrics := &fakeSubscriberMetrics{}
	subscriber := NewRedisSubscriber(
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics,
	)
	var received Envelope
	subscriber.Register(
		EventNotificationCreated,
		func(ctx context.Context, event Envelope) error {
			received = event
			return nil
		},
	)

	subscriber.handleMessage(context.Background(), NotificationChannel, string(raw))
	if received.ID != event.ID || received.RecipientID != 2 {
		t.Fatalf("received envelope = %+v", received)
	}
	if metrics.results["success"] != 1 {
		t.Fatalf("success metrics = %d, want 1", metrics.results["success"])
	}
}

func TestSubscriberRejectsBadEvents(t *testing.T) {
	tests := []struct {
		name       string
		channel    string
		raw        string
		wantResult string
	}{
		{
			name:       "非法 JSON",
			channel:    NotificationChannel,
			raw:        `{`,
			wantResult: "decode_error",
		},
		{
			name:       "频道不匹配",
			channel:    MessageChannel,
			raw:        mustEnvelopeJSON(t, EventNotificationCreated),
			wantResult: "channel_mismatch",
		},
		{
			name:       "没有 Handler",
			channel:    MessageChannel,
			raw:        mustEnvelopeJSON(t, EventMessageCreated),
			wantResult: "unhandled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := &fakeSubscriberMetrics{}
			subscriber := NewRedisSubscriber(
				nil,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				metrics,
			)
			subscriber.handleMessage(context.Background(), tt.channel, tt.raw)
			if metrics.results[tt.wantResult] != 1 {
				t.Fatalf(
					"metrics[%s] = %d, want 1; all=%v",
					tt.wantResult,
					metrics.results[tt.wantResult],
					metrics.results,
				)
			}
		})
	}
}

func TestSubscriberHandlerError(t *testing.T) {
	event, err := NewEnvelope(EventMessageCreated, 2, map[string]int{"id": 1})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	raw, _ := json.Marshal(event)
	metrics := &fakeSubscriberMetrics{}
	subscriber := NewRedisSubscriber(
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics,
	)
	subscriber.Register(EventMessageCreated, func(ctx context.Context, event Envelope) error {
		return errors.New("handler failed")
	})

	subscriber.handleMessage(context.Background(), MessageChannel, string(raw))
	if metrics.results["handler_error"] != 1 {
		t.Fatalf("handler_error metrics = %d, want 1", metrics.results["handler_error"])
	}
}

func mustEnvelopeJSON(t *testing.T, eventType EventType) string {
	t.Helper()
	event, err := NewEnvelope(eventType, 2, map[string]int{"id": 1})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return string(raw)
}

func testTime() time.Time {
	return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
}
