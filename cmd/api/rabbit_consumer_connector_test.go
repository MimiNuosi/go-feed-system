package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-feed-system/internal/feed"
	"go-feed-system/pkg/config"
	"go-feed-system/pkg/rabbitmq"
)

type connectorFollowerReader struct{}

func (f *connectorFollowerReader) CountFollowers(
	ctx context.Context,
	authorID uint64,
) (int64, error) {
	return feed.DefaultFanoutFollowerThreshold, nil
}

func (f *connectorFollowerReader) ListFollowerIDs(
	ctx context.Context,
	authorID uint64,
	afterID uint64,
	limit int,
) ([]uint64, error) {
	return nil, nil
}

type connectorInbox struct{}

func (i *connectorInbox) Add(
	ctx context.Context,
	userIDs []uint64,
	videoID uint64,
	publishedAt time.Time,
) error {
	return nil
}

func (i *connectorInbox) List(
	ctx context.Context,
	userID uint64,
	cursor uint64,
	limit int,
) ([]uint64, error) {
	return nil, nil
}

func (i *connectorInbox) Trim(ctx context.Context, userID uint64) error {
	return nil
}

type connectorRetryPublisher struct{}

func (p *connectorRetryPublisher) Publish(
	ctx context.Context,
	message rabbitmq.Message,
) error {
	return nil
}

func TestRabbitConsumerConnectorConnect_RejectsMissingDependencies(t *testing.T) {
	tests := []struct {
		name           string
		followers      feed.FollowerReader
		inbox          feed.Inbox
		retryPublisher feed.RetryMessagePublisher
	}{
		{
			name:           "followers 为空",
			inbox:          &connectorInbox{},
			retryPublisher: &connectorRetryPublisher{},
		},
		{
			name:           "inbox 为空",
			followers:      &connectorFollowerReader{},
			retryPublisher: &connectorRetryPublisher{},
		},
		{
			name:      "retry publisher 为空",
			followers: &connectorFollowerReader{},
			inbox:     &connectorInbox{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connector := newRabbitConsumerConnector(
				config.RabbitMQConfig{},
				tt.followers,
				tt.inbox,
				feed.DefaultFanoutFollowerThreshold,
				tt.retryPublisher,
				newSupervisorTestLogger(),
				nil,
			)

			session, err := connector.Connect(context.Background())
			if err == nil {
				if session != nil {
					_ = session.Close()
				}
				t.Fatal("expected missing dependency error")
			}
			if session != nil {
				t.Fatalf("expected nil session, got %T", session)
			}
		})
	}
}

func TestRabbitConsumerSession_ReadyRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	session := newRabbitConsumerSession(nil, nil, nil)
	if err := session.Ready(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}

func TestRabbitConsumerConnectorConnectIntegration(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	cfg := config.RabbitMQConfig{
		URL:           url,
		Exchange:      "test.feed.connector.events." + suffix,
		ExchangeType:  "topic",
		RoutingKey:    "test.video.published." + suffix,
		Queue:         "test.feed.connector.queue." + suffix,
		DLX:           "test.feed.connector.dlx." + suffix,
		DLQ:           "test.feed.connector.dlq." + suffix,
		RetryExchange: "test.feed.connector.retry." + suffix,
		RetryQueue:    "test.feed.connector.retry.queue." + suffix,
		MaxRetries:    3,
		RetryDelay:    time.Second,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	connector := newRabbitConsumerConnector(
		cfg,
		&connectorFollowerReader{},
		&connectorInbox{},
		feed.DefaultFanoutFollowerThreshold,
		&connectorRetryPublisher{},
		newSupervisorTestLogger(),
		nil,
	)

	t.Cleanup(func() {
		cleanupRabbitConsumerTopology(t, cfg)
	})

	firstSession, err := connector.Connect(ctx)
	if err != nil {
		t.Fatalf("connect first session: %v", err)
	}
	if firstSession.Consumer() == nil {
		t.Fatal("expected first session consumer")
	}
	if err := firstSession.Ready(ctx); err != nil {
		t.Fatalf("first session should be ready: %v", err)
	}
	if err := firstSession.Close(); err != nil {
		t.Fatalf("close first session: %v", err)
	}
	if err := firstSession.Close(); err != nil {
		t.Fatalf("close first session a second time: %v", err)
	}
	if err := firstSession.Ready(ctx); err == nil {
		t.Fatal("expected closed first session to be not ready")
	}

	// 第二次连接会再次声明同一套拓扑，验证重连所需的幂等路径。
	secondSession, err := connector.Connect(ctx)
	if err != nil {
		t.Fatalf("connect second session: %v", err)
	}
	if secondSession.Consumer() == nil {
		t.Fatal("expected second session consumer")
	}
	if err := secondSession.Ready(ctx); err != nil {
		t.Fatalf("second session should be ready: %v", err)
	}
	if err := secondSession.Close(); err != nil {
		t.Fatalf("close second session: %v", err)
	}
}

func cleanupRabbitConsumerTopology(t *testing.T, cfg config.RabbitMQConfig) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := rabbitmq.Open(ctx, cfg)
	if err != nil {
		t.Errorf("open cleanup rabbitmq connection: %v", err)
		return
	}
	defer func() {
		if err := rabbitmq.Close(conn); err != nil {
			t.Errorf("close cleanup rabbitmq connection: %v", err)
		}
	}()

	channel, err := conn.Channel()
	if err != nil {
		t.Errorf("create cleanup rabbitmq channel: %v", err)
		return
	}
	defer func() {
		if err := channel.Close(); err != nil {
			t.Errorf("close cleanup rabbitmq channel: %v", err)
		}
	}()

	for _, queue := range []string{cfg.Queue, cfg.DLQ, cfg.RetryQueue} {
		if _, err := channel.QueueDelete(queue, false, false, false); err != nil {
			t.Errorf("delete queue %s: %v", queue, err)
		}
	}
	for _, exchange := range []string{cfg.Exchange, cfg.DLX, cfg.RetryExchange} {
		if err := channel.ExchangeDelete(exchange, false, false); err != nil {
			t.Errorf("delete exchange %s: %v", exchange, err)
		}
	}
}
