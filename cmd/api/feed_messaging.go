package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go-feed-system/internal/feed"
	"go-feed-system/internal/outbox"
	"go-feed-system/pkg/config"
	"go-feed-system/pkg/rabbitmq"
)

// feedMessaging 聚合 Feed 消费和 Outbox 发布所需的后台组件。
type feedMessaging struct {
	consumerSupervisor consumerSupervisorRunner
	producer           *rabbitmq.Producer
	retryProducer      *rabbitmq.Producer
	worker             outboxWorkerRunner
	logger             *slog.Logger
}

type consumerSupervisorRunner interface {
	Run(ctx context.Context) error
	Ready(ctx context.Context) error
	Close() error
}

type outboxWorkerRunner interface {
	Run(ctx context.Context) error
}

type feedMessagingMetrics interface {
	consumerSupervisorMetrics
	feed.ConsumerMetrics
	outbox.PublishMetrics
}

func newFeedMessaging(
	cfg config.Config,
	repository *outbox.GORMRepository,
	inbox feed.Inbox,
	followers feed.FollowerReader,
	metrics feedMessagingMetrics,
	logger *slog.Logger,
) (*feedMessaging, error) {
	if repository == nil {
		return nil, fmt.Errorf("create feed messaging: outbox repository is nil")
	}
	if inbox == nil {
		return nil, fmt.Errorf("create feed messaging: inbox is nil")
	}
	if followers == nil {
		return nil, fmt.Errorf("create feed messaging: followers reader is nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	// 1. Producer 自己维护发送 Channel 和重连，这里只进行无网络 I/O 的构造。
	producer := rabbitmq.NewProducer(cfg.RabbitMQ, logger)
	retryConfig := cfg.RabbitMQ
	retryConfig.Exchange = cfg.RabbitMQ.RetryExchange
	retryProducer := rabbitmq.NewProducer(retryConfig, logger)

	// 2. Consumer 的连接、拓扑声明和重连全部由 Supervisor + Connector 管理。
	connector := newRabbitConsumerConnector(
		cfg.RabbitMQ,
		followers,
		inbox,
		cfg.Feed.FanoutFollowerThreshold,
		retryProducer,
		logger,
		metrics,
	)
	consumerSupervisor := NewConsumerSupervisor(
		connector,
		defaultReconnectMinBackoff,
		defaultReconnectMaxBackoff,
		logger,
		metrics,
	)

	// 3. Outbox Worker 只依赖 Publisher，和 Consumer 生命周期完全独立。
	messagePublisher := outbox.NewRabbitMQPublisher(producer)
	outboxPublisher := outbox.NewPublisher(
		repository,
		messagePublisher,
		outbox.DefaultPublishBatchSize,
		logger,
		metrics,
	)
	worker := outbox.NewWorker(
		outboxPublisher,
		cfg.Outbox.PublishInterval,
		logger,
	)

	return &feedMessaging{
		consumerSupervisor: consumerSupervisor,
		producer:           producer,
		retryProducer:      retryProducer,
		worker:             worker,
		logger:             logger,
	}, nil
}

// Run 同时运行 Feed Consumer Supervisor 和 Outbox Worker。
func (m *feedMessaging) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	supervisorDone := make(chan struct{})
	workerErrCh := make(chan error, 1)

	go func() {
		defer close(supervisorDone)
		m.runConsumerSupervisor(runCtx)
	}()

	go func() {
		workerErrCh <- m.worker.Run(runCtx)
	}()

	select {
	case <-ctx.Done():
		cancel()
		<-supervisorDone
		return <-workerErrCh
	case err := <-workerErrCh:
		cancel()
		<-supervisorDone
		if err != nil {
			return fmt.Errorf("outbox worker: %w", err)
		}
		return fmt.Errorf("outbox worker stopped unexpectedly")
	}
}

func (m *feedMessaging) runConsumerSupervisor(ctx context.Context) {
	logger := m.logger
	if logger == nil {
		logger = slog.Default()
	}

	for {
		err := m.consumerSupervisor.Run(ctx)
		if ctx.Err() != nil {
			return
		}

		// ConsumerSupervisor 会自行处理 RabbitMQ 连接和消费错误。
		// 运行到这里说明守护循环本身意外退出，因此由外层退避后重新启动。
		if err != nil {
			logger.Error("consumer supervisor stopped unexpectedly", "error", err)
		} else {
			logger.Warn("consumer supervisor stopped unexpectedly")
		}

		timer := time.NewTimer(defaultReconnectMinBackoff)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

func (m *feedMessaging) Close() error {
	var errs []error
	if m.consumerSupervisor != nil {
		if err := m.consumerSupervisor.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close consumer supervisor: %w", err))
		}
	}
	if m.producer != nil {
		if err := m.producer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close rabbitmq producer: %w", err))
		}
	}
	if m.retryProducer != nil {
		if err := m.retryProducer.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close rabbitmq retry producer: %w", err))
		}
	}

	return errors.Join(errs...)
}

func (m *feedMessaging) Ready(ctx context.Context) error {
	if m.consumerSupervisor == nil {
		return fmt.Errorf("consumer supervisor is not configured")
	}
	return m.consumerSupervisor.Ready(ctx)
}
