package feed

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/config"
	"go-feed-system/pkg/rabbitmq"
)

var errConsumerFanout = errors.New("fanout failed")

type acknowledgmentCall struct {
	kind     string
	multiple bool
	requeue  bool
}

type fakeAcknowledger struct {
	err   error
	calls []acknowledgmentCall
}

func (f *fakeAcknowledger) Ack(tag uint64, multiple bool) error {
	f.calls = append(f.calls, acknowledgmentCall{kind: "ack", multiple: multiple})
	return f.err
}

func (f *fakeAcknowledger) Nack(tag uint64, multiple, requeue bool) error {
	f.calls = append(f.calls, acknowledgmentCall{
		kind:     "nack",
		multiple: multiple,
		requeue:  requeue,
	})
	return f.err
}

func (f *fakeAcknowledger) Reject(tag uint64, requeue bool) error {
	f.calls = append(f.calls, acknowledgmentCall{kind: "reject", requeue: requeue})
	return f.err
}

type fakeFanoutHandler struct {
	err       error
	callCount int
	lastEvent VideoPublishedEvent
}

func (f *fakeFanoutHandler) Fanout(ctx context.Context, event VideoPublishedEvent) error {
	f.callCount++
	f.lastEvent = event
	return f.err
}

func TestRabbitConsumer_HandleDelivery(t *testing.T) {
	publishedAt := time.Now().UTC().Truncate(time.Millisecond)
	validBody := marshalVideoPublishedMessage(t, videoPublishedMessage{
		VideoID:     101,
		AuthorID:    202,
		PublishedAt: publishedAt,
	})
	invalidBody := marshalVideoPublishedMessage(t, videoPublishedMessage{
		VideoID:     0,
		AuthorID:    0,
		PublishedAt: time.Time{},
	})

	background := func() context.Context {
		return context.Background()
	}
	canceled := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}

	tests := []struct {
		name string

		messageType string
		messageID   string
		body        []byte
		ctx         func() context.Context
		fanoutErr   error

		wantFanoutCalls int
		wantAck         bool
		wantNack        bool
		wantRequeue     bool
	}{
		{
			name:            "有效消息处理成功后 ACK",
			messageType:     videoPublishedMessageType,
			messageID:       "event-1",
			body:            validBody,
			ctx:             background,
			wantFanoutCalls: 1,
			wantAck:         true,
		},
		{
			name:        "消息类型错误时进入 DLQ",
			messageType: "unknown.event",
			messageID:   "event-2",
			body:        validBody,
			ctx:         background,
			wantNack:    true,
		},
		{
			name:        "缺少 MessageID 时进入 DLQ",
			messageType: videoPublishedMessageType,
			body:        validBody,
			ctx:         background,
			wantNack:    true,
		},
		{
			name:        "JSON 格式错误时进入 DLQ",
			messageType: videoPublishedMessageType,
			messageID:   "event-3",
			body:        []byte(`{`),
			ctx:         background,
			wantNack:    true,
		},
		{
			name:        "消息内容非法时进入 DLQ",
			messageType: videoPublishedMessageType,
			messageID:   "event-4",
			body:        invalidBody,
			ctx:         background,
			wantNack:    true,
		},
		{
			name:            "Fanout 失败时进入 DLQ",
			messageType:     videoPublishedMessageType,
			messageID:       "event-5",
			body:            validBody,
			ctx:             background,
			fanoutErr:       errConsumerFanout,
			wantFanoutCalls: 1,
			wantNack:        true,
		},
		{
			name:            "context 取消时重新入队",
			messageType:     videoPublishedMessageType,
			messageID:       "event-6",
			body:            validBody,
			ctx:             canceled,
			fanoutErr:       context.Canceled,
			wantFanoutCalls: 1,
			wantNack:        true,
			wantRequeue:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ack := &fakeAcknowledger{}
			fanout := &fakeFanoutHandler{err: tt.fanoutErr}
			consumer := NewRabbitConsumer(nil, "", fanout, newDiscardLogger())

			delivery := amqp.Delivery{
				Acknowledger: ack,
				DeliveryTag:  1,
				Type:         tt.messageType,
				MessageId:    tt.messageID,
				Body:         tt.body,
			}

			consumer.handleDelivery(tt.ctx(), delivery)

			if fanout.callCount != tt.wantFanoutCalls {
				t.Errorf("expected %d fanout calls, got %d", tt.wantFanoutCalls, fanout.callCount)
			}
			if tt.wantFanoutCalls > 0 {
				if fanout.lastEvent.EventID != tt.messageID {
					t.Errorf("expected event ID %q, got %q", tt.messageID, fanout.lastEvent.EventID)
				}
				if fanout.lastEvent.VideoID != 101 {
					t.Errorf("expected video ID 101, got %d", fanout.lastEvent.VideoID)
				}
				if fanout.lastEvent.AuthorID != 202 {
					t.Errorf("expected author ID 202, got %d", fanout.lastEvent.AuthorID)
				}
				if !fanout.lastEvent.PublishedAt.Equal(publishedAt) {
					t.Errorf("expected published at %v, got %v", publishedAt, fanout.lastEvent.PublishedAt)
				}
			}

			if len(ack.calls) != 1 {
				t.Fatalf("expected one acknowledgment call, got %v", ack.calls)
			}
			got := ack.calls[0]
			if tt.wantAck && got.kind != "ack" {
				t.Errorf("expected ACK, got %v", got)
			}
			if tt.wantNack && got.kind != "nack" {
				t.Errorf("expected NACK, got %v", got)
			}
			if got.requeue != tt.wantRequeue {
				t.Errorf("expected requeue %t, got %t", tt.wantRequeue, got.requeue)
			}
			if got.multiple {
				t.Error("expected multiple=false")
			}
		})
	}
}

func TestRabbitConsumer_ConsumeIntegration(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	cfg := config.RabbitMQConfig{
		URL:          url,
		Exchange:     "test.feed.consume.events." + suffix,
		ExchangeType: "topic",
		RoutingKey:   "test.video.published." + suffix,
		Queue:        "test.feed.consume.queue." + suffix,
		DLX:          "test.feed.consume.dlx." + suffix,
		DLQ:          "test.feed.consume.dlq." + suffix,
	}

	ctx, cancel := context.WithCancel(context.Background())

	conn, err := rabbitmq.Open(ctx, cfg)
	if err != nil {
		t.Fatalf("open rabbitmq connection: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close rabbitmq connection: %v", err)
		}
	}()

	channel, err := conn.Channel()
	if err != nil {
		t.Fatalf("create consumer channel: %v", err)
	}
	defer func() {
		if err := channel.Close(); err != nil {
			t.Errorf("close consumer channel: %v", err)
		}
	}()

	publishChannel, err := conn.Channel()
	if err != nil {
		t.Fatalf("create publish channel: %v", err)
	}
	defer func() {
		if err := publishChannel.Close(); err != nil {
			t.Errorf("close publish channel: %v", err)
		}
	}()

	if err := rabbitmq.DeclareTopology(ctx, channel, cfg); err != nil {
		t.Fatalf("declare topology: %v", err)
	}
	defer func() {
		for _, queue := range []string{cfg.Queue, cfg.DLQ} {
			if _, err := channel.QueueDelete(queue, false, false, false); err != nil {
				t.Errorf("delete queue %s: %v", queue, err)
			}
		}
		for _, exchange := range []string{cfg.Exchange, cfg.DLX} {
			if err := channel.ExchangeDelete(exchange, false, false); err != nil {
				t.Errorf("delete exchange %s: %v", exchange, err)
			}
		}
	}()
	defer cancel()

	publishedAt := time.Now().UTC().Truncate(time.Millisecond)
	fanout := &signalFanoutHandler{
		done: make(chan VideoPublishedEvent, 1),
	}
	consumer := NewRabbitConsumer(channel, cfg.Queue, fanout, newDiscardLogger())

	errCh := make(chan error, 1)
	go func() {
		errCh <- consumer.Consume(ctx)
	}()

	body := marshalVideoPublishedMessage(t, videoPublishedMessage{
		VideoID:     101,
		AuthorID:    202,
		PublishedAt: publishedAt,
	})
	if err := publishChannel.PublishWithContext(
		ctx,
		cfg.Exchange,
		cfg.RoutingKey,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    "event-consume-1",
			Type:         videoPublishedMessageType,
			Timestamp:    publishedAt,
			Body:         body,
		},
	); err != nil {
		t.Fatalf("publish message: %v", err)
	}

	select {
	case event := <-fanout.done:
		if event.EventID != "event-consume-1" {
			t.Errorf("expected event ID event-consume-1, got %q", event.EventID)
		}
		if event.VideoID != 101 || event.AuthorID != 202 {
			t.Errorf("unexpected event: %+v", event)
		}
		if !event.PublishedAt.Equal(publishedAt) {
			t.Errorf("expected published at %v, got %v", publishedAt, event.PublishedAt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for fanout")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("consume returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for consumer to stop")
	}
}

type signalFanoutHandler struct {
	done chan VideoPublishedEvent
}

func (f *signalFanoutHandler) Fanout(ctx context.Context, event VideoPublishedEvent) error {
	select {
	case f.done <- event:
	default:
	}
	return nil
}

func marshalVideoPublishedMessage(t *testing.T, message videoPublishedMessage) []byte {
	t.Helper()

	body, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal message: %v", err)
	}
	return body
}
