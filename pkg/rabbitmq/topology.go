package rabbitmq

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/config"
)

// DeclareTopology 声明 Feed 事件所需的 Exchange、Queue、Binding 和 DLQ。
//
// 声明是幂等操作：
//   - 第一次执行会创建资源。
//   - 后续执行会检查已存在的资源是否参数一致。
//   - durable 资源会跨 RabbitMQ 重启保留。
func DeclareTopology(
	ctx context.Context,
	channel *amqp.Channel,
	cfg config.RabbitMQConfig,
) error {
	if channel == nil {
		return fmt.Errorf("rabbitmq channel must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("declare rabbitmq topology: %w", err)
	}

	// 1. 声明主 Exchange
	if err := channel.ExchangeDeclare(
		cfg.Exchange, cfg.ExchangeType, true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare main exchange %s: %w", cfg.Exchange, err)
	}

	// 2. 声明死信 Exchange (DLX)
	if err := channel.ExchangeDeclare(
		cfg.DLX, cfg.ExchangeType, true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare dead letter exchange %s: %w", cfg.DLX, err)
	}

	// 3. 声明主 Queue (需要绑定死信交换机的参数)
	mainQueueArgs := amqp.Table{
		"x-dead-letter-exchange": cfg.DLX,
	}
	if _, err := channel.QueueDeclare(
		cfg.Queue, true, false, false, false, mainQueueArgs,
	); err != nil {
		return fmt.Errorf("declare main queue %s: %w", cfg.Queue, err)
	}

	// 4. 声明 DLQ
	if _, err := channel.QueueDeclare(
		cfg.DLQ, true, false, false, false, nil,
	); err != nil {
		return fmt.Errorf("declare dead letter queue %s: %w", cfg.DLQ, err)
	}

	// 5. 绑定主 Queue
	if err := channel.QueueBind(
		cfg.Queue, cfg.RoutingKey, cfg.Exchange, false, nil,
	); err != nil {
		return fmt.Errorf("bind main queue %s: %w", cfg.Queue, err)
	}

	// 6. 绑定 DLQ
	if err := channel.QueueBind(
		cfg.DLQ, cfg.RoutingKey, cfg.DLX, false, nil,
	); err != nil {
		return fmt.Errorf("bind dead letter queue %s: %w", cfg.DLQ, err)
	}

	return nil
}
