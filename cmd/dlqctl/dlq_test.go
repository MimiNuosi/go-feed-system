package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/config"
	"go-feed-system/pkg/rabbitmq"
)

func TestHeadersForReplay(t *testing.T) {
	tests := []struct {
		name    string
		headers amqp.Table
		want    amqp.Table
	}{
		{
			name:    "nil headers",
			headers: nil,
			want:    amqp.Table{},
		},
		{
			name: "reset retry and death headers",
			headers: amqp.Table{
				retryCountHeader:      int32(2),
				"x-death":             []interface{}{"death"},
				"x-first-death-queue": "main",
				"x-last-death-queue":  "main",
				"trace-id":            "trace-1",
			},
			want: amqp.Table{
				"trace-id": "trace-1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := headersForReplay(tt.headers)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d headers, got %d: %v", len(tt.want), len(got), got)
			}
			for key, wantValue := range tt.want {
				if got[key] != wantValue {
					t.Errorf("expected header %q=%v, got %v", key, wantValue, got[key])
				}
			}
		})
	}
}

func TestRetryCountFromHeaders(t *testing.T) {
	tests := []struct {
		name    string
		headers amqp.Table
		want    int
	}{
		{name: "nil", headers: nil, want: 0},
		{name: "int32", headers: amqp.Table{retryCountHeader: int32(3)}, want: 3},
		{name: "int64", headers: amqp.Table{retryCountHeader: int64(4)}, want: 4},
		{name: "string fallback", headers: amqp.Table{retryCountHeader: "5"}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryCountFromHeaders(tt.headers); got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestNormalizeLimit(t *testing.T) {
	tests := []struct {
		name     string
		value    int
		fallback int
		want     int
	}{
		{name: "positive", value: 5, fallback: 10, want: 5},
		{name: "zero", value: 0, fallback: 10, want: 10},
		{name: "negative", value: -1, fallback: 10, want: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeLimit(tt.value, tt.fallback); got != tt.want {
				t.Fatalf("expected %d, got %d", tt.want, got)
			}
		})
	}
}

func TestReplayDeadLetterIntegration(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	cfg := config.RabbitMQConfig{
		URL:           url,
		Exchange:      "test.dlqctl.events." + suffix,
		ExchangeType:  "topic",
		RoutingKey:    "test.video.published." + suffix,
		Queue:         "test.dlqctl.queue." + suffix,
		DLX:           "test.dlqctl.dlx." + suffix,
		DLQ:           "test.dlqctl.dlq." + suffix,
		RetryExchange: "test.dlqctl.retry." + suffix,
		RetryQueue:    "test.dlqctl.retry.queue." + suffix,
		MaxRetries:    3,
		RetryDelay:    time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := rabbitmq.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open rabbitmq connection: %v", err)
	}
	defer func() {
		if err := rabbitmq.Close(conn); err != nil {
			t.Errorf("close rabbitmq connection: %v", err)
		}
	}()

	adminChannel, err := conn.Channel()
	if err != nil {
		t.Fatalf("create admin channel: %v", err)
	}
	defer func() {
		if err := adminChannel.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			t.Errorf("close admin channel: %v", err)
		}
	}()
	defer cleanupDLQTopology(t, adminChannel, cfg)

	if err := rabbitmq.DeclareTopology(ctx, adminChannel, cfg); err != nil {
		t.Fatalf("declare topology: %v", err)
	}

	session, err := openDLQSession(ctx, cfg, newDLQTestLogger())
	if err != nil {
		t.Fatalf("open dlq session: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Errorf("close dlq session: %v", err)
		}
	}()

	messageID := "event-replay-1"
	body := []byte(`{"video_id":101,"author_id":202}`)
	publishedAt := time.Now().UTC().Truncate(time.Millisecond)
	if err := adminChannel.PublishWithContext(
		ctx,
		cfg.Exchange,
		cfg.RoutingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    messageID,
			Type:         "video.published",
			Timestamp:    publishedAt,
			Headers: amqp.Table{
				retryCountHeader: int32(2),
				"trace-id":       "trace-replay-1",
			},
			Body: body,
		},
	); err != nil {
		t.Fatalf("publish main message: %v", err)
	}

	mainDelivery := getDLQTestMessage(t, adminChannel, cfg.Queue)
	if err := mainDelivery.Nack(false, false); err != nil {
		t.Fatalf("dead-letter main message: %v", err)
	}

	deadLetter := waitForDeadLetterSummary(t, ctx, cfg, session.channel, messageID)
	if deadLetter.Type != "video.published" {
		t.Fatalf("expected type video.published, got %q", deadLetter.Type)
	}
	if deadLetter.RetryCount != 2 {
		t.Fatalf("expected retry count 2, got %d", deadLetter.RetryCount)
	}

	summary, found, err := replayDeadLetter(
		ctx,
		cfg,
		session.channel,
		session.producer,
		messageID,
		false,
		100,
	)
	if err != nil {
		t.Fatalf("dry-run replay: %v", err)
	}
	if !found {
		t.Fatal("expected dry-run replay to find message")
	}
	if summary.MessageID != messageID {
		t.Fatalf("expected message id %q, got %q", messageID, summary.MessageID)
	}
	assertNoDLQTestMessage(t, adminChannel, cfg.Queue)

	summary, found, err = replayDeadLetter(
		ctx,
		cfg,
		session.channel,
		session.producer,
		messageID,
		true,
		100,
	)
	if err != nil {
		t.Fatalf("execute replay: %v", err)
	}
	if !found {
		t.Fatal("expected execute replay to find message")
	}
	if summary.MessageID != messageID {
		t.Fatalf("expected message id %q, got %q", messageID, summary.MessageID)
	}

	replayed := getDLQTestMessage(t, adminChannel, cfg.Queue)
	if replayed.MessageId != messageID {
		t.Fatalf("expected replayed message id %q, got %q", messageID, replayed.MessageId)
	}
	if !bytes.Equal(replayed.Body, body) {
		t.Fatalf("expected replayed body %s, got %s", body, replayed.Body)
	}
	if got := retryCountFromHeaders(replayed.Headers); got != 0 {
		t.Fatalf("expected retry count reset, got %d", got)
	}
	if got := replayed.Headers["trace-id"]; got != "trace-replay-1" {
		t.Fatalf("expected trace-id preserved, got %v", got)
	}
	if err := replayed.Ack(false); err != nil {
		t.Fatalf("ack replayed message: %v", err)
	}

	deadLetters, err := listDeadLetters(ctx, cfg, session.channel, 100)
	if err != nil {
		t.Fatalf("list dead letters after replay: %v", err)
	}
	if len(deadLetters) != 0 {
		t.Fatalf("expected empty dlq, got %+v", deadLetters)
	}
}

func waitForDeadLetterSummary(
	t *testing.T,
	ctx context.Context,
	cfg config.RabbitMQConfig,
	channel *amqp.Channel,
	messageID string,
) deadLetterSummary {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		summaries, err := listDeadLetters(ctx, cfg, channel, 100)
		if err != nil {
			t.Fatalf("list dead letters: %v", err)
		}
		for _, summary := range summaries {
			if summary.MessageID == messageID {
				return summary
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for dead letter %q", messageID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func getDLQTestMessage(t *testing.T, channel *amqp.Channel, queue string) amqp.Delivery {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for {
		delivery, ok, err := channel.Get(queue, false)
		if err != nil {
			t.Fatalf("get message from %s: %v", queue, err)
		}
		if ok {
			return delivery
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for message in %s", queue)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertNoDLQTestMessage(t *testing.T, channel *amqp.Channel, queue string) {
	t.Helper()

	delivery, ok, err := channel.Get(queue, false)
	if err != nil {
		t.Fatalf("get message from %s: %v", queue, err)
	}
	if ok {
		_ = delivery.Nack(false, true)
		t.Fatalf("expected %s to be empty", queue)
	}
}

func cleanupDLQTopology(
	t *testing.T,
	channel *amqp.Channel,
	cfg config.RabbitMQConfig,
) {
	t.Helper()

	for _, queue := range []string{cfg.Queue, cfg.DLQ, cfg.RetryQueue} {
		if _, err := channel.QueueDelete(queue, false, false, false); err != nil &&
			!errors.Is(err, amqp.ErrClosed) {
			t.Errorf("delete queue %s: %v", queue, err)
		}
	}
	for _, exchange := range []string{cfg.Exchange, cfg.DLX, cfg.RetryExchange} {
		if err := channel.ExchangeDelete(exchange, false, false); err != nil &&
			!errors.Is(err, amqp.ErrClosed) {
			t.Errorf("delete exchange %s: %v", exchange, err)
		}
	}
}

func newDLQTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
