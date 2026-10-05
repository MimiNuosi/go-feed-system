package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	amqp "github.com/rabbitmq/amqp091-go"

	"go-feed-system/internal/feed"
	"go-feed-system/internal/outbox"
	"go-feed-system/pkg/config"
	"go-feed-system/pkg/rabbitmq"
)

// feedMessaging 聚合 Feed 消费和 Outbox 发布所需的后台组件。
type feedMessaging struct {
	rabbitConn      *amqp.Connection
	consumerChannel *amqp.Channel
	producer        *rabbitmq.Producer
	consumer        feedConsumerRunner
	worker          outboxWorkerRunner
}

type feedConsumerRunner interface {
	Consume(ctx context.Context) error
}

type outboxWorkerRunner interface {
	Run(ctx context.Context) error
}

func newFeedMessaging(
	ctx context.Context,
	cfg config.Config,
	repository *outbox.GORMRepository,
	inbox feed.Inbox,
	followers feed.FollowerReader,
	logger *slog.Logger,
) (*feedMessaging, error) {
	// 1. 建立 Consumer 使用的 RabbitMQ Connection。
	rabbitConn, err := rabbitmq.Open(ctx, cfg.RabbitMQ)
	if err != nil {
		return nil, fmt.Errorf("open rabbitmq: %w", err)
	}

	// 2. 使用独立 Channel 声明拓扑。拓扑声明完成后可以关闭该 Channel。
	topologyChannel, err := rabbitConn.Channel()
	if err != nil {
		_ = rabbitmq.Close(rabbitConn)
		return nil, fmt.Errorf("create rabbitmq topology channel: %w", err)
	}
	if err := rabbitmq.DeclareTopology(ctx, topologyChannel, cfg.RabbitMQ); err != nil {
		_ = topologyChannel.Close()
		_ = rabbitmq.Close(rabbitConn)
		return nil, fmt.Errorf("declare rabbitmq topology: %w", err)
	}
	if err := topologyChannel.Close(); err != nil {
		_ = rabbitmq.Close(rabbitConn)
		return nil, fmt.Errorf("close rabbitmq topology channel: %w", err)
	}

	// 3. Consumer 使用自己的 Channel。
	consumerChannel, err := rabbitConn.Channel()
	if err != nil {
		_ = rabbitmq.Close(rabbitConn)
		return nil, fmt.Errorf("create rabbitmq consumer channel: %w", err)
	}

	// 4. 组装 Feed 消费链路。
	fanoutService := feed.NewFanoutService(followers, inbox)
	consumer := feed.NewRabbitConsumer(
		consumerChannel,
		cfg.RabbitMQ.Queue,
		fanoutService,
		logger,
	)

	// 5. 组装 Outbox 发布链路。Producer 自己维护发送 Channel 和重连。
	producer := rabbitmq.NewProducer(cfg.RabbitMQ, logger)
	messagePublisher := outbox.NewRabbitMQPublisher(producer)
	outboxPublisher := outbox.NewPublisher(
		repository,
		messagePublisher,
		outbox.DefaultPublishBatchSize,
		logger,
	)
	worker := outbox.NewWorker(
		outboxPublisher,
		cfg.Outbox.PublishInterval,
		logger,
	)

	return &feedMessaging{
		rabbitConn:      rabbitConn,
		consumerChannel: consumerChannel,
		producer:        producer,
		consumer:        consumer,
		worker:          worker,
	}, nil
}

// Run 同时运行 Feed Consumer 和 Outbox Worker。
//
// 任一服务异常退出时，会取消另一个服务并返回第一个错误。
func (m *feedMessaging) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, 2)

	go func() {
		if err := m.consumer.Consume(runCtx); err != nil {
			errCh <- fmt.Errorf("feed consumer: %w", err)
			return
		}
		errCh <- nil
	}()

	go func() {
		if err := m.worker.Run(runCtx); err != nil {
			errCh <- fmt.Errorf("outbox worker: %w", err)
			return
		}
		errCh <- nil
	}()

	var firstErr error
	for i := 0; i < 2; i++ {
		err := <-errCh
		if err != nil && firstErr == nil {
			firstErr = err
			cancel()
		}
	}

	return firstErr
}

func (m *feedMessaging) Close() error {
	var errs []error
	if m.producer != nil {
		if err := m.producer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close rabbitmq producer: %w", err))
		}
	}
	if m.consumerChannel != nil {
		if err := m.consumerChannel.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close rabbitmq consumer channel: %w", err))
		}
	}
	if m.rabbitConn != nil {
		if err := rabbitmq.Close(m.rabbitConn); err != nil {
			errs = append(errs, fmt.Errorf("close rabbitmq connection: %w", err))
		}
	}

	return errors.Join(errs...)
}
