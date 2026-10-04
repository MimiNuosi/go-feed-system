package rabbitmq

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/pkg/config"
)

// Message 是 RabbitMQ Producer 接收的传输层消息。
//
// Outbox 适配器之后会把 outbox.Event 转换成 Message，
// 这样 pkg/rabbitmq 不需要依赖具体业务包。
type Message struct {
	MessageID string
	Type      string
	Body      []byte
	Timestamp time.Time
}

// Producer 负责保持 RabbitMQ 连接并发送 Persistent + Publisher Confirm 消息。
type Producer struct {
	cfg    config.RabbitMQConfig
	logger *slog.Logger

	mu      sync.Mutex
	conn    *amqp.Connection
	channel *amqp.Channel
}

func NewProducer(cfg config.RabbitMQConfig, logger *slog.Logger) *Producer {
	return &Producer{
		cfg:    cfg,
		logger: logger,
	}
}

// Publish 发送一条消息，并等待 RabbitMQ Publisher Confirm。
func (p *Producer) Publish(ctx context.Context, message Message) error {
	if strings.TrimSpace(message.MessageID) == "" {
		return fmt.Errorf("message id must not be empty")
	}
	if strings.TrimSpace(message.Type) == "" {
		return fmt.Errorf("message type must not be empty")
	}
	if len(message.Body) == 0 {
		return fmt.Errorf("message body must not be empty")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	channel, err := p.ensureChannel(ctx)
	if err != nil {
		return fmt.Errorf("ensure rabbitmq channel: %w", err)
	}

	// 1. 构造 amqp.Publishing 结构体
	publishing := amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent, // 2 表示消息持久化
		MessageId:    message.MessageID,
		Type:         message.Type,
		Timestamp:    message.Timestamp,
		Body:         message.Body,
	}

	// 2. 发送消息，返回一个 Confirmation 对象
	confirmation, err := channel.PublishWithDeferredConfirmWithContext(
		ctx,
		p.cfg.Exchange,
		p.cfg.RoutingKey,
		false, // mandatory
		false, // immediate
		publishing,
	)
	if err != nil {
		return fmt.Errorf("publish message to exchange %s: %w", p.cfg.Exchange, err)
	}

	// 3. 如果 confirmation 为 nil，说明 Channel 没有开启 Confirm 模式
	if confirmation == nil {
		return fmt.Errorf("publisher confirm is not enabled on channel")
	}

	// 4. 同步等待 Broker 的确认（ACK）
	ack, err := confirmation.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("wait for publisher confirm: %w", err)
	}

	// 5. 如果 ack 为 false，说明 RabbitMQ 拒收了该消息（NACK）
	if !ack {
		return fmt.Errorf("message was nacked by broker")
	}

	return nil
}

func (p *Producer) ensureChannel(ctx context.Context) (*amqp.Channel, error) {
	// 1. 如果通道存在且未关闭，直接返回复用
	if p.channel != nil && !p.channel.IsClosed() {
		return p.channel, nil
	}

	// 2. 关闭并丢弃已经失效的通道和连接
	if p.channel != nil {
		_ = p.channel.Close()
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}

	// 3. 建立新连接
	conn, err := Open(ctx, p.cfg)
	if err != nil {
		return nil, fmt.Errorf("open rabbitmq connection: %w", err)
	}

	// 4. 创建新通道
	channel, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create rabbitmq channel: %w", err)
	}

	// 5. 声明交换机（Producer 只声明 Exchange，不声明 Queue）
	err = channel.ExchangeDeclare(
		p.cfg.Exchange,     // name
		p.cfg.ExchangeType, // kind (topic)
		true,               // durable (持久化)
		false,              // autoDelete
		false,              // internal
		false,              // noWait
		nil,                // args
	)
	if err != nil {
		_ = channel.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("declare exchange %s: %w", p.cfg.Exchange, err)
	}

	// 6. 开启 Publisher Confirm 模式
	// 开启后，每次 Publish 都会返回一个 DeferredConfirmation
	if err := channel.Confirm(false); err != nil {
		_ = channel.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("enable publisher confirm: %w", err)
	}

	// 7. 保存并返回
	p.conn = conn
	p.channel = channel
	return channel, nil
}

// Close 关闭 Channel 和 Connection。
func (p *Producer) Close() error {
	p.mu.Lock()
	channel := p.channel
	conn := p.conn
	p.channel = nil
	p.conn = nil
	p.mu.Unlock()

	var errs []error
	if channel != nil {
		if err := channel.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close rabbitmq channel: %w", err))
		}
	}
	if conn != nil {
		if err := conn.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close rabbitmq connection: %w", err))
		}
	}

	return errors.Join(errs...)
}
