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

type fakeRetryPublisher struct {
	err       error
	callCount int
	last      rabbitmq.Message
}

type fakeConsumerMetrics struct {
	dlqReasons []string
}

func (f *fakeConsumerMetrics) IncDLQMessage(reason string) {
	f.dlqReasons = append(f.dlqReasons, reason)
}

func (f *fakeRetryPublisher) Publish(ctx context.Context, message rabbitmq.Message) error {
	f.callCount++
	f.last = message
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
		headers     amqp.Table
		maxRetries  int

		wantFanoutCalls int
		wantRetryCalls  int
		wantRetryCount  int
		wantAck         bool
		wantNack        bool
		wantRequeue     bool
		wantDLQReason   string
	}{
		{
			name:            "有效消息处理成功后 ACK",
			messageType:     videoPublishedMessageType,
			messageID:       "event-1",
			body:            validBody,
			ctx:             background,
			wantFanoutCalls: 1,
			maxRetries:      3,
			wantAck:         true,
		},
		{
			name:          "消息类型错误时进入 DLQ",
			messageType:   "unknown.event",
			messageID:     "event-2",
			body:          validBody,
			ctx:           background,
			maxRetries:    3,
			wantNack:      true,
			wantDLQReason: "invalid_type",
		},
		{
			name:          "缺少 MessageID 时进入 DLQ",
			messageType:   videoPublishedMessageType,
			body:          validBody,
			ctx:           background,
			maxRetries:    3,
			wantNack:      true,
			wantDLQReason: "missing_message_id",
		},
		{
			name:          "JSON 格式错误时进入 DLQ",
			messageType:   videoPublishedMessageType,
			messageID:     "event-3",
			body:          []byte(`{`),
			ctx:           background,
			maxRetries:    3,
			wantNack:      true,
			wantDLQReason: "invalid_json",
		},
		{
			name:          "消息内容非法时进入 DLQ",
			messageType:   videoPublishedMessageType,
			messageID:     "event-4",
			body:          invalidBody,
			ctx:           background,
			maxRetries:    3,
			wantNack:      true,
			wantDLQReason: "invalid_content",
		},
		{
			name:            "Fanout 临时失败时发布重试并 ACK",
			messageType:     videoPublishedMessageType,
			messageID:       "event-5",
			body:            validBody,
			ctx:             background,
			fanoutErr:       errConsumerFanout,
			maxRetries:      3,
			wantFanoutCalls: 1,
			wantRetryCalls:  1,
			wantRetryCount:  1,
			wantAck:         true,
		},
		{
			name:            "达到最大重试次数时进入 DLQ",
			messageType:     videoPublishedMessageType,
			messageID:       "event-6",
			body:            validBody,
			ctx:             background,
			fanoutErr:       errConsumerFanout,
			headers:         amqp.Table{retryCountHeader: int32(3)},
			maxRetries:      3,
			wantFanoutCalls: 1,
			wantNack:        true,
			wantDLQReason:   "max_retries",
		},
		{
			name:            "Fanout 永久错误时直接进入 DLQ",
			messageType:     videoPublishedMessageType,
			messageID:       "event-7",
			body:            validBody,
			ctx:             background,
			fanoutErr:       ErrInvalidInput,
			maxRetries:      3,
			wantFanoutCalls: 1,
			wantNack:        true,
			wantDLQReason:   "permanent_error",
		},
		{
			name:            "context 取消时重新入队",
			messageType:     videoPublishedMessageType,
			messageID:       "event-8",
			body:            validBody,
			ctx:             canceled,
			fanoutErr:       context.Canceled,
			maxRetries:      3,
			wantFanoutCalls: 1,
			wantNack:        true,
			wantRequeue:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ack := &fakeAcknowledger{}
			fanout := &fakeFanoutHandler{err: tt.fanoutErr}
			retryPublisher := &fakeRetryPublisher{}
			metrics := &fakeConsumerMetrics{}
			consumer := NewRabbitConsumer(
				nil,
				"",
				fanout,
				retryPublisher,
				tt.maxRetries,
				newDiscardLogger(),
				metrics,
			)

			delivery := amqp.Delivery{
				Acknowledger: ack,
				DeliveryTag:  1,
				Type:         tt.messageType,
				MessageId:    tt.messageID,
				Body:         tt.body,
				Headers:      tt.headers,
			}

			consumer.handleDelivery(tt.ctx(), delivery)

			if fanout.callCount != tt.wantFanoutCalls {
				t.Errorf("expected %d fanout calls, got %d", tt.wantFanoutCalls, fanout.callCount)
			}
			if retryPublisher.callCount != tt.wantRetryCalls {
				t.Errorf("expected %d retry calls, got %d", tt.wantRetryCalls, retryPublisher.callCount)
			}
			if tt.wantRetryCalls > 0 {
				if retryPublisher.last.MessageID != tt.messageID {
					t.Errorf("expected retry message ID %q, got %q", tt.messageID, retryPublisher.last.MessageID)
				}
				if got := retryCountFromHeaders(retryPublisher.last.Headers); got != tt.wantRetryCount {
					t.Errorf("expected retry count %d, got %d", tt.wantRetryCount, got)
				}
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
			if tt.wantDLQReason == "" {
				if len(metrics.dlqReasons) != 0 {
					t.Errorf("expected no DLQ metric, got %v", metrics.dlqReasons)
				}
			} else if len(metrics.dlqReasons) != 1 || metrics.dlqReasons[0] != tt.wantDLQReason {
				t.Errorf("expected DLQ reason %q, got %v", tt.wantDLQReason, metrics.dlqReasons)
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
		URL:           url,
		Exchange:      "test.feed.consume.events." + suffix,
		ExchangeType:  "topic",
		RoutingKey:    "test.video.published." + suffix,
		Queue:         "test.feed.consume.queue." + suffix,
		DLX:           "test.feed.consume.dlx." + suffix,
		DLQ:           "test.feed.consume.dlq." + suffix,
		RetryExchange: "test.feed.consume.retry." + suffix,
		RetryQueue:    "test.feed.consume.retry.queue." + suffix,
		MaxRetries:    3,
		RetryDelay:    time.Second,
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
		for _, queue := range []string{cfg.Queue, cfg.DLQ, cfg.RetryQueue} {
			if _, err := channel.QueueDelete(queue, false, false, false); err != nil {
				t.Errorf("delete queue %s: %v", queue, err)
			}
		}
		for _, exchange := range []string{cfg.Exchange, cfg.DLX, cfg.RetryExchange} {
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
	consumer := NewRabbitConsumer(
		channel,
		cfg.Queue,
		fanout,
		&fakeRetryPublisher{},
		cfg.MaxRetries,
		newDiscardLogger(),
		nil,
	)

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

func TestRabbitConsumer_RetryIntegration(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	cfg := config.RabbitMQConfig{
		URL:           url,
		Exchange:      "test.feed.retry.events." + suffix,
		ExchangeType:  "topic",
		RoutingKey:    "test.video.published." + suffix,
		Queue:         "test.feed.retry.queue." + suffix,
		DLX:           "test.feed.retry.dlx." + suffix,
		DLQ:           "test.feed.retry.dlq." + suffix,
		RetryExchange: "test.feed.retry.exchange." + suffix,
		RetryQueue:    "test.feed.retry.retry-queue." + suffix,
		MaxRetries:    3,
		RetryDelay:    500 * time.Millisecond,
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
	defer cancel()

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
		for _, queue := range []string{cfg.Queue, cfg.DLQ, cfg.RetryQueue} {
			if _, err := channel.QueueDelete(queue, false, false, false); err != nil {
				t.Errorf("delete queue %s: %v", queue, err)
			}
		}
		for _, exchange := range []string{cfg.Exchange, cfg.DLX, cfg.RetryExchange} {
			if err := channel.ExchangeDelete(exchange, false, false); err != nil {
				t.Errorf("delete exchange %s: %v", exchange, err)
			}
		}
	}()

	retryConfig := cfg
	retryConfig.Exchange = cfg.RetryExchange
	retryProducer := rabbitmq.NewProducer(retryConfig, newDiscardLogger())
	defer func() {
		if err := retryProducer.Close(); err != nil {
			t.Errorf("close retry producer: %v", err)
		}
	}()

	fanout := &retryThenSuccessFanout{
		success: make(chan struct{}),
	}
	consumer := NewRabbitConsumer(
		channel,
		cfg.Queue,
		fanout,
		retryProducer,
		cfg.MaxRetries,
		newDiscardLogger(),
		nil,
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- consumer.Consume(ctx)
	}()

	now := time.Now().UTC().Truncate(time.Millisecond)
	body := marshalVideoPublishedMessage(t, videoPublishedMessage{
		VideoID:     301,
		AuthorID:    401,
		PublishedAt: now,
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
			MessageId:    "event-retry-1",
			Type:         videoPublishedMessageType,
			Timestamp:    now,
			Body:         body,
		},
	); err != nil {
		t.Fatalf("publish message: %v", err)
	}

	select {
	case <-fanout.success:
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for retry success")
	}

	if fanout.calls != 2 {
		t.Fatalf("expected two fanout attempts, got %d", fanout.calls)
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

type retryThenSuccessFanout struct {
	calls   int
	success chan struct{}
}

func (f *retryThenSuccessFanout) Fanout(ctx context.Context, event VideoPublishedEvent) error {
	f.calls++
	if f.calls == 1 {
		return errConsumerFanout
	}
	select {
	case f.success <- struct{}{}:
	default:
	}
	return nil
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
