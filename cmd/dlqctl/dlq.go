package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/config"
	"go-feed-system/pkg/rabbitmq"
)

const retryCountHeader = "x-retry-count"

type deadLetterSummary struct {
	MessageID  string
	Type       string
	RetryCount int
	BodySize   int
	Timestamp  time.Time
}

type messagePublisher interface {
	Publish(ctx context.Context, message rabbitmq.Message) error
}

type dlqSession struct {
	conn     *amqp.Connection
	channel  *amqp.Channel
	producer *rabbitmq.Producer
}

func openDLQSession(
	ctx context.Context,
	cfg config.RabbitMQConfig,
	logger *slog.Logger,
) (*dlqSession, error) {
	conn, err := rabbitmq.Open(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open rabbitmq connection: %w", err)
	}

	channel, err := conn.Channel()
	if err != nil {
		_ = rabbitmq.Close(conn)
		return nil, fmt.Errorf("create rabbitmq channel: %w", err)
	}
	if err := rabbitmq.DeclareTopology(ctx, channel, cfg); err != nil {
		_ = channel.Close()
		_ = rabbitmq.Close(conn)
		return nil, fmt.Errorf("declare rabbitmq topology: %w", err)
	}

	return &dlqSession{
		conn:     conn,
		channel:  channel,
		producer: rabbitmq.NewProducer(cfg, logger),
	}, nil
}

func (s *dlqSession) Close() error {
	var errs []error
	if s.producer != nil {
		if err := s.producer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close producer: %w", err))
		}
	}
	if s.channel != nil {
		if err := s.channel.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
			errs = append(errs, fmt.Errorf("close channel: %w", err))
		}
	}
	if s.conn != nil {
		if err := rabbitmq.Close(s.conn); err != nil && !errors.Is(err, amqp.ErrClosed) {
			errs = append(errs, fmt.Errorf("close connection: %w", err))
		}
	}
	return errors.Join(errs...)
}

func listDeadLetters(
	ctx context.Context,
	cfg config.RabbitMQConfig,
	channel *amqp.Channel,
	limit int,
) ([]deadLetterSummary, error) {
	limit = normalizeLimit(limit, defaultListLimit)
	deliveries, err := scanDLQ(ctx, cfg, channel, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		requeueDeliveries(deliveries)
	}()

	summaries := make([]deadLetterSummary, 0, len(deliveries))
	for _, delivery := range deliveries {
		summaries = append(summaries, summarizeDelivery(delivery))
	}
	return summaries, nil
}

func replayDeadLetter(
	ctx context.Context,
	cfg config.RabbitMQConfig,
	channel *amqp.Channel,
	publisher messagePublisher,
	messageID string,
	execute bool,
	scanLimit int,
) (deadLetterSummary, bool, error) {
	if strings.TrimSpace(messageID) == "" {
		return deadLetterSummary{}, false, fmt.Errorf("replay dead letter: message id is empty")
	}
	if publisher == nil {
		return deadLetterSummary{}, false, fmt.Errorf("replay dead letter: publisher is nil")
	}

	scanLimit = normalizeLimit(scanLimit, defaultReplayScanLimit)
	deliveries, err := scanDLQ(ctx, cfg, channel, scanLimit)
	if err != nil {
		return deadLetterSummary{}, false, err
	}
	defer func() {
		requeueDeliveries(deliveries)
	}()

	for i, delivery := range deliveries {
		if delivery.MessageId != messageID {
			continue
		}

		summary := summarizeDelivery(delivery)
		if !execute {
			return summary, true, nil
		}
		if err := publisher.Publish(ctx, replayMessage(delivery)); err != nil {
			return summary, true, fmt.Errorf("publish replay message %q: %w", messageID, err)
		}
		if err := delivery.Ack(false); err != nil {
			return summary, true, fmt.Errorf("ack replayed message %q: %w", messageID, err)
		}

		// 目标消息已经 ACK，从待重新入队列表中移除。
		deliveries = append(deliveries[:i], deliveries[i+1:]...)
		return summary, true, nil
	}

	return deadLetterSummary{}, false, nil
}

func scanDLQ(
	ctx context.Context,
	cfg config.RabbitMQConfig,
	channel *amqp.Channel,
	limit int,
) ([]amqp.Delivery, error) {
	if channel == nil {
		return nil, fmt.Errorf("scan dlq: channel is nil")
	}

	deliveries := make([]amqp.Delivery, 0, limit)
	for len(deliveries) < limit {
		if err := ctx.Err(); err != nil {
			requeueDeliveries(deliveries)
			return nil, fmt.Errorf("scan dlq: %w", err)
		}

		delivery, ok, err := channel.Get(cfg.DLQ, false)
		if err != nil {
			requeueDeliveries(deliveries)
			return nil, fmt.Errorf("scan dlq: get message: %w", err)
		}
		if !ok {
			break
		}
		deliveries = append(deliveries, delivery)
	}

	return deliveries, nil
}

func requeueDeliveries(deliveries []amqp.Delivery) {
	for i := len(deliveries) - 1; i >= 0; i-- {
		_ = deliveries[i].Nack(false, true)
	}
}

func summarizeDelivery(delivery amqp.Delivery) deadLetterSummary {
	return deadLetterSummary{
		MessageID:  delivery.MessageId,
		Type:       delivery.Type,
		RetryCount: retryCountFromHeaders(delivery.Headers),
		BodySize:   len(delivery.Body),
		Timestamp:  delivery.Timestamp,
	}
}

func replayMessage(delivery amqp.Delivery) rabbitmq.Message {
	return rabbitmq.Message{
		MessageID: delivery.MessageId,
		Type:      delivery.Type,
		Body:      delivery.Body,
		Timestamp: delivery.Timestamp,
		Headers:   headersForReplay(delivery.Headers),
	}
}

func headersForReplay(headers amqp.Table) amqp.Table {
	cloned := make(amqp.Table, len(headers))
	for key, value := range headers {
		if shouldResetReplayHeader(key) {
			continue
		}
		cloned[key] = value
	}
	return cloned
}

func shouldResetReplayHeader(key string) bool {
	if key == retryCountHeader {
		return true
	}
	if key == "x-death" {
		return true
	}
	return strings.HasPrefix(key, "x-first-death-") ||
		strings.HasPrefix(key, "x-last-death-")
}

func retryCountFromHeaders(headers amqp.Table) int {
	if headers == nil {
		return 0
	}

	switch value := headers[retryCountHeader].(type) {
	case int:
		return value
	case int8:
		return int(value)
	case int16:
		return int(value)
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func normalizeLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}
