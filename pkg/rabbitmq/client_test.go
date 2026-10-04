package rabbitmq

import (
	"context"
	"os"
	"testing"
	"time"

	"go-feed-system/pkg/config"
)

func TestOpenRequiresURL(t *testing.T) {
	if _, err := Open(context.Background(), config.RabbitMQConfig{}); err == nil {
		t.Fatal("expected empty URL to fail")
	}
}

func TestOpenAndClose(t *testing.T) {
	url := os.Getenv("TEST_RABBITMQ_URL")
	if url == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := Open(ctx, config.RabbitMQConfig{URL: url})
	if err != nil {
		t.Fatalf("open rabbitmq: %v", err)
	}
	if err := Close(conn); err != nil {
		t.Fatalf("close rabbitmq: %v", err)
	}
}
