package feed

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/rabbitmq"
)

const (
	videoPublishedMessageType = "video.published"
	retryCountHeader          = "x-retry-count"
)

// videoPublishedMessage 是 Consumer 侧的消息契约。
//
// 它只依赖 JSON 字段，不直接复用 outbox.VideoPublishedPayload，
// 避免 Feed 模块依赖 Outbox 的存储实现。
type videoPublishedMessage struct {
	VideoID     uint64    `json:"video_id"`
	AuthorID    uint64    `json:"author_id"`
	PublishedAt time.Time `json:"published_at"`
}

// FanoutHandler 是 Consumer 需要的业务处理能力。
//
// FanoutService 隐式实现这个接口，测试时可以注入 Fake。
type FanoutHandler interface {
	Fanout(ctx context.Context, event VideoPublishedEvent) error
}

// RetryMessagePublisher 负责把失败消息重新发布到 Retry Exchange。
type RetryMessagePublisher interface {
	Publish(ctx context.Context, message rabbitmq.Message) error
}

// ConsumerMetrics 是 Feed Consumer 需要的窄指标接口。
type ConsumerMetrics interface {
	IncDLQMessage(reason string)
}

// RabbitConsumer 从 RabbitMQ 队列消费视频发布事件。
type RabbitConsumer struct {
	channel        *amqp.Channel
	queue          string
	fanout         FanoutHandler
	retryPublisher RetryMessagePublisher
	maxRetries     int
	logger         *slog.Logger
	metrics        ConsumerMetrics
}

func NewRabbitConsumer(
	channel *amqp.Channel,
	queue string,
	fanout FanoutHandler,
	retryPublisher RetryMessagePublisher,
	maxRetries int,
	logger *slog.Logger,
	metrics ConsumerMetrics,
) *RabbitConsumer {
	return &RabbitConsumer{
		channel:        channel,
		queue:          queue,
		fanout:         fanout,
		retryPublisher: retryPublisher,
		maxRetries:     maxRetries,
		logger:         logger,
		metrics:        metrics,
	}
}

// Consume 启动长期消费循环，直到 context 被取消。
func (c *RabbitConsumer) Consume(ctx context.Context) error {
	if c.channel == nil || c.queue == "" || c.fanout == nil || c.retryPublisher == nil || c.logger == nil {
		return fmt.Errorf("consume feed events: consumer is not properly initialized")
	}

	if err := c.channel.Qos(1, 0, false); err != nil {
		return fmt.Errorf("consume feed events: set qos: %w", err)
	}

	deliveries, err := c.channel.Consume(
		c.queue,
		"feed_consumer",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("consume feed events: start consuming: %w", err)
	}

	for {
		select {
		case delivery, ok := <-deliveries:
			if !ok {
				return fmt.Errorf("consume feed events: deliveries channel closed")
			}
			c.handleDelivery(ctx, delivery)
		case <-ctx.Done():
			if err := c.channel.Cancel("feed_consumer", false); err != nil {
				return fmt.Errorf("consume feed events: cancel consumer: %w", err)
			}
			return nil
		}
	}
}

func (c *RabbitConsumer) handleDelivery(ctx context.Context, delivery amqp.Delivery) {
	if delivery.Type != videoPublishedMessageType {
		c.logger.Error("consume feed events: unexpected message type",
			"type", delivery.Type,
			"message_id", delivery.MessageId,
		)
		c.deadLetter(delivery, "invalid_type")
		return
	}

	if delivery.MessageId == "" {
		c.logger.Error("consume feed events: missing message ID")
		c.deadLetter(delivery, "missing_message_id")
		return
	}

	var msg videoPublishedMessage
	if err := json.Unmarshal(delivery.Body, &msg); err != nil {
		c.logger.Error("consume feed events: failed to unmarshal message",
			"message_id", delivery.MessageId,
			"error", err,
		)
		c.deadLetter(delivery, "invalid_json")
		return
	}

	if msg.VideoID == 0 || msg.AuthorID == 0 || msg.PublishedAt.IsZero() {
		c.logger.Error("consume feed events: invalid message content",
			"message_id", delivery.MessageId,
			"video_id", msg.VideoID,
			"author_id", msg.AuthorID,
			"published_at", msg.PublishedAt,
		)
		c.deadLetter(delivery, "invalid_content")
		return
	}

	event := VideoPublishedEvent{
		EventID:     delivery.MessageId,
		VideoID:     msg.VideoID,
		AuthorID:    msg.AuthorID,
		PublishedAt: msg.PublishedAt,
	}

	if err := c.fanout.Fanout(ctx, event); err != nil {
		if ctx.Err() != nil {
			c.logger.Warn("consume feed events: context canceled during fanout",
				"message_id", delivery.MessageId,
				"error", err,
			)
			c.nack(delivery, true)
			return
		}
		if errors.Is(err, ErrInvalidInput) {
			c.logger.Error("consume feed events: permanent fanout failure",
				"message_id", delivery.MessageId,
				"error", err,
			)
			c.deadLetter(delivery, "permanent_error")
			return
		}
		c.retryOrDeadLetter(ctx, delivery, err)
		return
	}

	if err := delivery.Ack(false); err != nil {
		c.logger.Error("consume feed events: failed to ack message",
			"message_id", delivery.MessageId,
			"error", err,
		)
	}
}

func (c *RabbitConsumer) retryOrDeadLetter(
	ctx context.Context,
	delivery amqp.Delivery,
	fanoutErr error,
) {
	retryCount := retryCountFromHeaders(delivery.Headers)
	if retryCount >= c.maxRetries {
		c.logger.Error("consume feed events: max retries exceeded",
			"message_id", delivery.MessageId,
			"retry_count", retryCount,
			"max_retries", c.maxRetries,
			"error", fanoutErr,
		)
		c.deadLetter(delivery, "max_retries")
		return
	}

	headers := cloneHeaders(delivery.Headers)
	headers[retryCountHeader] = int32(retryCount + 1)

	message := rabbitmq.Message{
		MessageID: delivery.MessageId,
		Type:      delivery.Type,
		Body:      delivery.Body,
		Timestamp: delivery.Timestamp,
		Headers:   headers,
	}

	if err := c.retryPublisher.Publish(ctx, message); err != nil {
		c.logger.Error("consume feed events: publish retry failed",
			"message_id", delivery.MessageId,
			"retry_count", retryCount+1,
			"error", err,
		)
		c.nack(delivery, true)
		return
	}

	if err := delivery.Ack(false); err != nil {
		c.logger.Error("consume feed events: ack retried message failed",
			"message_id", delivery.MessageId,
			"error", err,
		)
	}
}

func retryCountFromHeaders(headers amqp.Table) int {
	if headers == nil {
		return 0
	}

	switch value := headers[retryCountHeader].(type) {
	case int:
		return value
	case int32:
		return int(value)
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func cloneHeaders(headers amqp.Table) amqp.Table {
	cloned := make(amqp.Table, len(headers)+1)
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

func (c *RabbitConsumer) nack(delivery amqp.Delivery, requeue bool) {
	if err := delivery.Nack(false, requeue); err != nil {
		c.logger.Error("consume feed events: failed to nack message",
			"message_id", delivery.MessageId,
			"requeue", requeue,
			"error", err,
		)
	}
}

func (c *RabbitConsumer) deadLetter(delivery amqp.Delivery, reason string) {
	if err := delivery.Nack(false, false); err != nil {
		c.logger.Error("consume feed events: failed to dead-letter message",
			"message_id", delivery.MessageId,
			"reason", reason,
			"error", err,
		)
		return
	}

	if c.metrics != nil {
		c.metrics.IncDLQMessage(reason)
	}
}
