package outbox

import (
	"context"
	"fmt"

	"go-feed-system/pkg/rabbitmq"
)

// MessagePublisher 是底层消息传输能力。
//
// rabbitmq.Producer 将来会隐式实现这个接口。
// Outbox 只依赖这个窄接口，测试时可以注入 Fake。
type MessagePublisher interface {
	Publish(ctx context.Context, message rabbitmq.Message) error
}

// RabbitMQPublisher 把业务 Outbox Event 转换为 RabbitMQ 传输消息。
type RabbitMQPublisher struct {
	publisher MessagePublisher
}

func NewRabbitMQPublisher(publisher MessagePublisher) *RabbitMQPublisher {
	return &RabbitMQPublisher{
		publisher: publisher,
	}
}

var _ EventPublisher = (*RabbitMQPublisher)(nil)

// Publish 把 outbox.Event 映射成 rabbitmq.Message 后交给底层 Producer。
func (p *RabbitMQPublisher) Publish(ctx context.Context, event Event) error {
	if p == nil || p.publisher == nil {
		return fmt.Errorf("publish outbox event through rabbitmq: publisher is nil")
	}

	message := rabbitmq.Message{
		MessageID: event.EventID,
		Type:      event.EventType,
		Body:      event.Payload,
		Timestamp: event.CreatedAt,
	}

	if err := p.publisher.Publish(ctx, message); err != nil {
		return fmt.Errorf("publish outbox event through rabbitmq: %w", err)
	}

	return nil
}
