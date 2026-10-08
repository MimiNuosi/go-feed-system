package outbox

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const DefaultPublishBatchSize = 100

// EventPublisher 是消息发送能力的抽象。
//
// MVP 阶段使用 Fake 或日志实现；接入 RabbitMQ 后，只需要替换具体实现。
type EventPublisher interface {
	Publish(ctx context.Context, event Event) error
}

// PublishMetrics 是 Outbox 发布阶段需要的窄指标接口。
type PublishMetrics interface {
	IncOutboxPublish(result string)
}

// Publisher 负责把数据库中待发送的 Outbox 事件投递给消息系统。
type Publisher struct {
	repository Repository
	publisher  EventPublisher
	batchSize  int
	logger     *slog.Logger
	metrics    PublishMetrics
}

func NewPublisher(
	repository Repository,
	publisher EventPublisher,
	batchSize int,
	logger *slog.Logger,
	metrics PublishMetrics,
) *Publisher {
	if batchSize <= 0 {
		batchSize = DefaultPublishBatchSize
	}

	return &Publisher{
		repository: repository,
		publisher:  publisher,
		batchSize:  batchSize,
		logger:     logger,
		metrics:    metrics,
	}
}

// PublishPending 扫描并发布一批待处理事件。
//
// 返回成功标记为 published 的事件数量。
func (p *Publisher) PublishPending(ctx context.Context) (int, error) {
	// 1. 拉取一批待发送的事件
	events, err := p.repository.ListPending(ctx, p.batchSize)
	if err != nil {
		return 0, fmt.Errorf("publish pending: list: %w", err)
	}

	publishedCount := 0

	// 2. 逐个投递
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return publishedCount, fmt.Errorf("publish pending: context canceled: %w", err)
		}

		// 2.1 调用事件发布器（MVP 阶段是 Fake/日志，未来是 RabbitMQ）
		if err := p.publisher.Publish(ctx, event); err != nil {
			p.incPublishMetric("error")

			// 2.2 发布失败：记录重试次数
			// 注意：IncrementRetry 使用 SQL 原子自增，并发安全
			if retryErr := p.repository.IncrementRetry(ctx, event.ID, err.Error()); retryErr != nil {
				// 记录日志，但不要用 retryErr 覆盖 publish 的错误，因为 publish 失败才是主要问题
				p.logger.Error("outbox: increment retry failed",
					"event_id", event.EventID,
					"publish_error", err,
					"increment_error", retryErr,
				)
			}

			p.logger.Error("outbox: publish event failed",
				"event_id", event.EventID,
				"event_type", event.EventType,
				"error", err,
			)

			// 核心：遇到错误立即返回，不继续处理后续事件。
			// 避免 MQ 宕机时，把后面所有事件的重试次数也一起耗尽。
			return publishedCount, fmt.Errorf("publish pending: event %s: %w", event.EventID, err)
		}

		// 2.3 发布成功：标记为 published
		if err := p.repository.MarkPublished(ctx, event.ID, time.Now()); err != nil {
			p.incPublishMetric("mark_error")

			// 发送成功但标记失败，这是一个非常危险的中间态。
			// 下次轮询还会把这条事件当成 pending 重新发送。
			// 我们只能记录日志并返回错误，让上层感知到异常。
			p.logger.Error("outbox: mark published failed",
				"event_id", event.EventID,
				"error", err,
			)
			return publishedCount, fmt.Errorf("publish pending: mark published %s: %w", event.EventID, err)
		}

		p.incPublishMetric("success")
		publishedCount++
	}

	// 3. 返回成功处理的数量
	return publishedCount, nil
}

func (p *Publisher) incPublishMetric(result string) {
	if p.metrics != nil {
		p.metrics.IncOutboxPublish(result)
	}
}
