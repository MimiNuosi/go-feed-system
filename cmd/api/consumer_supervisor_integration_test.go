package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-feed-system/internal/feed"
	"go-feed-system/pkg/config"
)

func TestConsumerSupervisorIntegration_ReconnectsAfterConnectionClosed(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	cfg := config.RabbitMQConfig{
		URL:           url,
		Exchange:      "test.feed.supervisor.events." + suffix,
		ExchangeType:  "topic",
		RoutingKey:    "test.video.published." + suffix,
		Queue:         "test.feed.supervisor.queue." + suffix,
		DLX:           "test.feed.supervisor.dlx." + suffix,
		DLQ:           "test.feed.supervisor.dlq." + suffix,
		RetryExchange: "test.feed.supervisor.retry." + suffix,
		RetryQueue:    "test.feed.supervisor.retry.queue." + suffix,
		MaxRetries:    3,
		RetryDelay:    100 * time.Millisecond,
	}

	t.Cleanup(func() {
		cleanupRabbitConsumerTopology(t, cfg)
	})

	connector := newRabbitConsumerConnector(
		cfg,
		&connectorFollowerReader{},
		&connectorInbox{},
		feed.DefaultFanoutFollowerThreshold,
		&connectorRetryPublisher{},
		newSupervisorTestLogger(),
		nil,
	)
	supervisor := NewConsumerSupervisor(
		connector,
		10*time.Millisecond,
		20*time.Millisecond,
		newSupervisorTestLogger(),
		nil,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runErrCh := make(chan error, 1)
	go func() {
		runErrCh <- supervisor.Run(ctx)
	}()

	firstSession := waitForSupervisorSession(t, ctx, supervisor, nil)
	if err := firstSession.Ready(ctx); err != nil {
		t.Fatalf("first session should be ready: %v", err)
	}

	// 模拟 Broker 重启或网络断开：直接关闭当前 Connection 和 Channel。
	if err := firstSession.Close(); err != nil {
		t.Fatalf("close first session: %v", err)
	}

	secondSession := waitForSupervisorSession(t, ctx, supervisor, firstSession)
	if secondSession == firstSession {
		t.Fatal("expected supervisor to replace the failed session")
	}
	if err := secondSession.Ready(ctx); err != nil {
		t.Fatalf("second session should be ready: %v", err)
	}

	cancel()
	select {
	case err := <-runErrCh:
		if err != nil {
			t.Fatalf("supervisor returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for supervisor to stop")
	}

	if err := supervisor.Close(); err != nil {
		t.Fatalf("close supervisor: %v", err)
	}
}

func waitForSupervisorSession(
	t *testing.T,
	ctx context.Context,
	supervisor *ConsumerSupervisor,
	previous consumerSession,
) consumerSession {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for {
		session := currentSupervisorSession(supervisor)
		if session != nil && session != previous {
			if err := session.Ready(ctx); err == nil {
				return session
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for supervisor session")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func currentSupervisorSession(supervisor *ConsumerSupervisor) consumerSession {
	supervisor.mu.RLock()
	defer supervisor.mu.RUnlock()
	return supervisor.session
}
