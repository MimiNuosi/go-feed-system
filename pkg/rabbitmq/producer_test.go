package rabbitmq

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/config"
)

func TestProducerPublishValidation(t *testing.T) {
	tests := []struct {
		name    string
		message Message
	}{
		{
			name:    "缺少消息 ID",
			message: Message{Type: "video.published", Body: []byte(`{}`)},
		},
		{
			name:    "缺少消息类型",
			message: Message{MessageID: "event-1", Body: []byte(`{}`)},
		},
		{
			name:    "缺少消息体",
			message: Message{MessageID: "event-1", Type: "video.published"},
		},
	}

	producer := NewProducer(config.RabbitMQConfig{}, newTestLogger())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := producer.Publish(context.Background(), tt.message); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestProducerCloseIsIdempotent(t *testing.T) {
	producer := NewProducer(config.RabbitMQConfig{}, newTestLogger())

	if err := producer.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := producer.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

func TestProducerPublishIntegration(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}

	cfg := config.RabbitMQConfig{
		URL:          url,
		Exchange:     "test.feed.events." + uuid.NewString(),
		ExchangeType: "topic",
		RoutingKey:   "video.published",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminConn, err := Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open admin connection: %v", err)
	}
	t.Cleanup(func() {
		if err := adminConn.Close(); err != nil {
			t.Errorf("close admin connection: %v", err)
		}
	})

	adminChannel, err := adminConn.Channel()
	if err != nil {
		t.Fatalf("create admin channel: %v", err)
	}
	t.Cleanup(func() {
		if err := adminChannel.Close(); err != nil {
			t.Errorf("close admin channel: %v", err)
		}
	})

	queueName := "test.feed.queue." + uuid.NewString()
	if err := adminChannel.ExchangeDeclare(
		cfg.Exchange,
		cfg.ExchangeType,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		t.Fatalf("declare test exchange: %v", err)
	}
	if _, err := adminChannel.QueueDeclare(
		queueName,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		t.Fatalf("declare test queue: %v", err)
	}
	if err := adminChannel.QueueBind(
		queueName,
		cfg.RoutingKey,
		cfg.Exchange,
		false,
		nil,
	); err != nil {
		t.Fatalf("bind test queue: %v", err)
	}

	t.Cleanup(func() {
		if _, err := adminChannel.QueueDelete(queueName, false, false, false); err != nil {
			t.Errorf("delete test queue: %v", err)
		}
		if err := adminChannel.ExchangeDelete(cfg.Exchange, false, false); err != nil {
			t.Errorf("delete test exchange: %v", err)
		}
	})

	producer := NewProducer(cfg, newTestLogger())
	t.Cleanup(func() {
		if err := producer.Close(); err != nil {
			t.Errorf("close producer: %v", err)
		}
	})

	timestamp := time.Now().UTC().Truncate(time.Second)
	messages := []Message{
		{
			MessageID: "event-1",
			Type:      "video.published",
			Body:      []byte(`{"video_id":1}`),
			Timestamp: timestamp,
		},
		{
			MessageID: "event-2",
			Type:      "video.published",
			Body:      []byte(`{"video_id":2}`),
			Timestamp: timestamp,
		},
	}

	for _, message := range messages {
		if err := producer.Publish(ctx, message); err != nil {
			t.Fatalf("publish message %s: %v", message.MessageID, err)
		}
	}

	for _, want := range messages {
		got := getMessage(t, adminChannel, queueName)
		if !bytes.Equal(got.Body, want.Body) {
			t.Errorf("expected body %s, got %s", want.Body, got.Body)
		}
		if got.MessageId != want.MessageID {
			t.Errorf("expected message ID %q, got %q", want.MessageID, got.MessageId)
		}
		if got.Type != want.Type {
			t.Errorf("expected type %q, got %q", want.Type, got.Type)
		}
		if got.ContentType != "application/json" {
			t.Errorf("expected content type application/json, got %q", got.ContentType)
		}
		if got.DeliveryMode != amqp.Persistent {
			t.Errorf("expected persistent delivery mode, got %d", got.DeliveryMode)
		}
		if !got.Timestamp.Equal(want.Timestamp) {
			t.Errorf("expected timestamp %v, got %v", want.Timestamp, got.Timestamp)
		}
	}
}

func getMessage(t *testing.T, channel *amqp.Channel, queueName string) amqp.Delivery {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		message, ok, err := channel.Get(queueName, true)
		if err != nil {
			t.Fatalf("get message: %v", err)
		}
		if ok {
			return message
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for message")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
