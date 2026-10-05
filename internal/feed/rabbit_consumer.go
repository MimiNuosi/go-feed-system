package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const videoPublishedMessageType = "video.published"

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

// RabbitConsumer 从 RabbitMQ 队列消费视频发布事件。
type RabbitConsumer struct {
	channel *amqp.Channel
	queue   string
	fanout  FanoutHandler
	logger  *slog.Logger
}

func NewRabbitConsumer(
	channel *amqp.Channel,
	queue string,
	fanout FanoutHandler,
	logger *slog.Logger,
) *RabbitConsumer {
	return &RabbitConsumer{
		channel: channel,
		queue:   queue,
		fanout:  fanout,
		logger:  logger,
	}
}

// Consume 启动长期消费循环，直到 context 被取消。
func (c *RabbitConsumer) Consume(ctx context.Context) error {
	if c.channel == nil || c.queue == "" || c.fanout == nil || c.logger == nil {
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
		c.nack(delivery, false)
		return
	}

	if delivery.MessageId == "" {
		c.logger.Error("consume feed events: missing message ID")
		c.nack(delivery, false)
		return
	}

	var msg videoPublishedMessage
	if err := json.Unmarshal(delivery.Body, &msg); err != nil {
		c.logger.Error("consume feed events: failed to unmarshal message",
			"message_id", delivery.MessageId,
			"error", err,
		)
		c.nack(delivery, false)
		return
	}

	if msg.VideoID == 0 || msg.AuthorID == 0 || msg.PublishedAt.IsZero() {
		c.logger.Error("consume feed events: invalid message content",
			"message_id", delivery.MessageId,
			"video_id", msg.VideoID,
			"author_id", msg.AuthorID,
			"published_at", msg.PublishedAt,
		)
		c.nack(delivery, false)
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
		c.logger.Error("consume feed events: failed to fanout event",
			"message_id", delivery.MessageId,
			"error", err,
		)
		c.nack(delivery, false)
		return
	}

	if err := delivery.Ack(false); err != nil {
		c.logger.Error("consume feed events: failed to ack message",
			"message_id", delivery.MessageId,
			"error", err,
		)
	}
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
