package outbox

import (
	"context"
	"fmt"
	"log/slog"
)

const DefaultPublishBatchSize = 100

// EventPublisher 是消息发送能力的抽象。
//
// MVP 阶段使用 Fake 或日志实现；接入 RabbitMQ 后，只需要替换具体实现。
type EventPublisher interface {
	Publish(ctx context.Context, event Event) error
}

// Publisher 负责把数据库中待发送的 Outbox 事件投递给消息系统。
type Publisher struct {
	repository Repository
	publisher  EventPublisher
	batchSize  int
	logger     *slog.Logger
}

func NewPublisher(
	repository Repository,
	publisher EventPublisher,
	batchSize int,
	logger *slog.Logger,
) *Publisher {
	if batchSize <= 0 {
		batchSize = DefaultPublishBatchSize
	}

	return &Publisher{
		repository: repository,
		publisher:  publisher,
		batchSize:  batchSize,
		logger:     logger,
	}
}

// PublishPending 扫描并发布一批待处理事件。
//
// 返回成功标记为 published 的事件数量。
func (p *Publisher) PublishPending(ctx context.Context) (int, error) {
	// TODO(阶段 6.3)：
	// 1. ListPending 读取一批事件。
	// 2. 逐个调用 publisher.Publish。
	// 3. 发送成功后 MarkPublished。
	// 4. 发送失败后 IncrementRetry，并记录日志。
	// 5. 思考：第一条事件失败后，是继续处理后面的，还是立即返回？
	//
	// 至少一次语义下，发送成功但 MarkPublished 失败时，下次会重复发送。
	// 因此真正的消费者必须按 EventID 或 VideoID 做幂等处理。
	return 0, fmt.Errorf("publish pending outbox events: %w", ErrNotImplemented)
}
