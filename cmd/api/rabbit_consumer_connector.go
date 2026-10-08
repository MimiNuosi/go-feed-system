package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/internal/feed"
	"go-feed-system/pkg/config"
	"go-feed-system/pkg/rabbitmq"
)

// rabbitConsumerConnector 每次 Connect 都创建一套独立的 RabbitMQ 消费会话。
//
// 断线重连时不复用旧 Connection 或 Channel，因为 AMQP 连接一旦异常关闭，
// 其上的 Channel 也可能已经失效。由 ConsumerSupervisor 负责重试节奏，
// Connector 只负责把一条可用会话完整组装出来。
type rabbitConsumerConnector struct {
	cfg                     config.RabbitMQConfig
	followers               feed.FollowerReader
	inbox                   feed.Inbox
	fanoutFollowerThreshold int
	retryPublisher          feed.RetryMessagePublisher
	logger                  *slog.Logger
	consumerMetrics         feed.ConsumerMetrics
}

func newRabbitConsumerConnector(
	cfg config.RabbitMQConfig,
	followers feed.FollowerReader,
	inbox feed.Inbox,
	fanoutFollowerThreshold int,
	retryPublisher feed.RetryMessagePublisher,
	logger *slog.Logger,
	consumerMetrics feed.ConsumerMetrics,
) *rabbitConsumerConnector {
	if fanoutFollowerThreshold <= 0 {
		fanoutFollowerThreshold = feed.DefaultFanoutFollowerThreshold
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &rabbitConsumerConnector{
		cfg:                     cfg,
		followers:               followers,
		inbox:                   inbox,
		fanoutFollowerThreshold: fanoutFollowerThreshold,
		retryPublisher:          retryPublisher,
		logger:                  logger,
		consumerMetrics:         consumerMetrics,
	}
}

// Connect 创建连接、声明拓扑、建立 Consumer Channel，并返回一套可运行会话。
func (c *rabbitConsumerConnector) Connect(ctx context.Context) (consumerSession, error) {
	if c == nil {
		return nil, fmt.Errorf("connect rabbitmq consumer: connector is nil")
	}
	if c.followers == nil {
		return nil, fmt.Errorf("connect rabbitmq consumer: followers reader is nil")
	}
	if c.inbox == nil {
		return nil, fmt.Errorf("connect rabbitmq consumer: inbox is nil")
	}
	if c.retryPublisher == nil {
		return nil, fmt.Errorf("connect rabbitmq consumer: retry publisher is nil")
	}

	conn, err := rabbitmq.Open(ctx, c.cfg)
	if err != nil {
		return nil, fmt.Errorf("connect rabbitmq consumer: open connection: %w", err)
	}

	topologyChannel, err := conn.Channel()
	if err != nil {
		_ = rabbitmq.Close(conn)
		return nil, fmt.Errorf("connect rabbitmq consumer: create topology channel: %w", err)
	}

	if err := rabbitmq.DeclareTopology(ctx, topologyChannel, c.cfg); err != nil {
		_ = topologyChannel.Close()
		_ = rabbitmq.Close(conn)
		return nil, fmt.Errorf("connect rabbitmq consumer: declare topology: %w", err)
	}
	if err := topologyChannel.Close(); err != nil {
		_ = rabbitmq.Close(conn)
		return nil, fmt.Errorf("connect rabbitmq consumer: close topology channel: %w", err)
	}

	consumerChannel, err := conn.Channel()
	if err != nil {
		_ = rabbitmq.Close(conn)
		return nil, fmt.Errorf("connect rabbitmq consumer: create consumer channel: %w", err)
	}

	fanoutService := feed.NewFanoutService(
		c.followers,
		c.inbox,
		c.fanoutFollowerThreshold,
	)
	consumer := feed.NewRabbitConsumer(
		consumerChannel,
		c.cfg.Queue,
		fanoutService,
		c.retryPublisher,
		c.cfg.MaxRetries,
		c.logger,
		c.consumerMetrics,
	)

	return newRabbitConsumerSession(conn, consumerChannel, consumer), nil
}

// rabbitConsumerSession 保存一次 Connect 产生的全部可关闭资源。
type rabbitConsumerSession struct {
	conn     *amqp.Connection
	channel  *amqp.Channel
	consumer managedConsumer

	closeOnce sync.Once
	closeErr  error
}

func newRabbitConsumerSession(
	conn *amqp.Connection,
	channel *amqp.Channel,
	consumer managedConsumer,
) *rabbitConsumerSession {
	return &rabbitConsumerSession{
		conn:     conn,
		channel:  channel,
		consumer: consumer,
	}
}

func (s *rabbitConsumerSession) Consumer() managedConsumer {
	if s == nil {
		return nil
	}
	return s.consumer
}

// Ready 同时检查 Connection 和 Consumer Channel 是否仍然可用。
func (s *rabbitConsumerSession) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return fmt.Errorf("rabbitmq consumer session is nil")
	}
	if s.conn == nil || s.conn.IsClosed() {
		return fmt.Errorf("rabbitmq consumer connection is not open")
	}
	if s.channel == nil || s.channel.IsClosed() {
		return fmt.Errorf("rabbitmq consumer channel is not open")
	}
	return nil
}

// Close 关闭 Consumer 持有的 Channel 和 Connection。
//
// Close 可能同时被正常退出路径和 defer 调用，因此使用 sync.Once 保证幂等。
func (s *rabbitConsumerSession) Close() error {
	if s == nil {
		return nil
	}

	s.closeOnce.Do(func() {
		var errs []error
		if s.channel != nil && !s.channel.IsClosed() {
			if err := s.channel.Close(); err != nil && !errors.Is(err, amqp.ErrClosed) {
				errs = append(errs, fmt.Errorf("close rabbitmq consumer channel: %w", err))
			}
		}
		if s.conn != nil && !s.conn.IsClosed() {
			if err := rabbitmq.Close(s.conn); err != nil && !errors.Is(err, amqp.ErrClosed) {
				errs = append(errs, fmt.Errorf("close rabbitmq consumer connection: %w", err))
			}
		}
		s.closeErr = errors.Join(errs...)
	})

	return s.closeErr
}
