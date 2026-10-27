package realtime

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

type integrationMetrics struct {
	connected chan bool
	published chan string
	received  chan string
}

func (m *integrationMetrics) IncRealtimePublish(channel string, result string) {
	m.published <- result
}

func (m *integrationMetrics) IncRealtimeReceive(channel string, result string) {
	m.received <- result
}

func (m *integrationMetrics) IncRealtimeReconnect(result string) {}

func (m *integrationMetrics) SetRealtimeSubscriberConnected(connected bool) {
	m.connected <- connected
}

func TestRedisPublisherSubscriberIntegration(t *testing.T) {
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set TEST_REDIS_ADDR to run Redis integration test")
	}
	db := 0
	if raw := os.Getenv("TEST_REDIS_DB"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("invalid TEST_REDIS_DB: %v", err)
		}
		db = value
	}
	client := goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: os.Getenv("TEST_REDIS_PASSWORD"),
		DB:       db,
	})
	defer client.Close()
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}

	metrics := &integrationMetrics{
		connected: make(chan bool, 8),
		published: make(chan string, 8),
		received:  make(chan string, 8),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	subscriber := NewRedisSubscriber(client, logger, metrics)
	receivedEvent := make(chan Envelope, 1)
	subscriber.Register(EventNotificationCreated, func(
		ctx context.Context,
		event Envelope,
	) error {
		receivedEvent <- event
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		errCh <- subscriber.Run(ctx)
	}()
	waitForConnected(t, metrics.connected)

	publisher := NewRedisPublisher(client, metrics)
	event, err := NewEnvelope(
		EventNotificationCreated,
		99,
		map[string]any{"id": 7, "kind": "like"},
	)
	if err != nil {
		t.Fatalf("create event: %v", err)
	}
	if err := publisher.Publish(ctx, event); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case got := <-receivedEvent:
		if got.ID != event.ID || got.RecipientID != 99 {
			t.Fatalf("received event = %+v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for Redis pub/sub event")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("subscriber stopped with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("subscriber did not stop after cancellation")
	}
}

func waitForConnected(t *testing.T, connected <-chan bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case value := <-connected:
			if value {
				return
			}
		case <-deadline:
			t.Fatal("subscriber did not become connected")
		}
	}
}
