package config

import (
	"strings"
	"testing"
	"time"
)

func TestDurationFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback time.Duration
		want     time.Duration
		wantErr  bool
	}{
		{
			name:     "未配置时使用默认值",
			value:    "",
			fallback: time.Second,
			want:     time.Second,
		},
		{
			name:     "解析合法时长",
			value:    "250ms",
			fallback: time.Second,
			want:     250 * time.Millisecond,
		},
		{
			name:     "非法时长返回错误",
			value:    "soon",
			fallback: time.Second,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OUTBOX_PUBLISH_INTERVAL", tt.value)

			got, err := durationFromEnv("OUTBOX_PUBLISH_INTERVAL", tt.fallback)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tt.want {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

func TestLoadRabbitMQConfig(t *testing.T) {
	t.Setenv("MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/test")
	t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
	t.Setenv("RABBITMQ_URL", "amqp://feed_app:secret@127.0.0.1:5672/")
	t.Setenv("RABBITMQ_EXCHANGE", "")
	t.Setenv("RABBITMQ_EXCHANGE_TYPE", "")
	t.Setenv("RABBITMQ_ROUTING_KEY", "")
	t.Setenv("RABBITMQ_QUEUE", "")
	t.Setenv("RABBITMQ_DLX", "")
	t.Setenv("RABBITMQ_DLQ", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.RabbitMQ.URL != "amqp://feed_app:secret@127.0.0.1:5672/" {
		t.Errorf("unexpected RabbitMQ URL: %q", cfg.RabbitMQ.URL)
	}
	if cfg.RabbitMQ.Exchange != defaultRabbitMQExchange {
		t.Errorf("expected exchange %q, got %q", defaultRabbitMQExchange, cfg.RabbitMQ.Exchange)
	}
	if cfg.RabbitMQ.ExchangeType != defaultRabbitMQExchangeType {
		t.Errorf("expected exchange type %q, got %q", defaultRabbitMQExchangeType, cfg.RabbitMQ.ExchangeType)
	}
	if cfg.RabbitMQ.RoutingKey != defaultRabbitMQRoutingKey {
		t.Errorf("expected routing key %q, got %q", defaultRabbitMQRoutingKey, cfg.RabbitMQ.RoutingKey)
	}
	if cfg.RabbitMQ.Queue != defaultRabbitMQQueue {
		t.Errorf("expected queue %q, got %q", defaultRabbitMQQueue, cfg.RabbitMQ.Queue)
	}
	if cfg.RabbitMQ.DLX != defaultRabbitMQDLX {
		t.Errorf("expected DLX %q, got %q", defaultRabbitMQDLX, cfg.RabbitMQ.DLX)
	}
	if cfg.RabbitMQ.DLQ != defaultRabbitMQDLQ {
		t.Errorf("expected DLQ %q, got %q", defaultRabbitMQDLQ, cfg.RabbitMQ.DLQ)
	}
	if cfg.RabbitMQ.RetryExchange != defaultRabbitMQRetryExchange {
		t.Errorf("expected retry exchange %q, got %q", defaultRabbitMQRetryExchange, cfg.RabbitMQ.RetryExchange)
	}
	if cfg.RabbitMQ.RetryQueue != defaultRabbitMQRetryQueue {
		t.Errorf("expected retry queue %q, got %q", defaultRabbitMQRetryQueue, cfg.RabbitMQ.RetryQueue)
	}
	if cfg.RabbitMQ.MaxRetries != defaultRabbitMQMaxRetries {
		t.Errorf("expected max retries %d, got %d", defaultRabbitMQMaxRetries, cfg.RabbitMQ.MaxRetries)
	}
	if cfg.RabbitMQ.RetryDelay != defaultRabbitMQRetryDelay {
		t.Errorf("expected retry delay %v, got %v", defaultRabbitMQRetryDelay, cfg.RabbitMQ.RetryDelay)
	}
	if cfg.Feed.FanoutFollowerThreshold != defaultFanoutThreshold {
		t.Errorf(
			"expected fanout threshold %d, got %d",
			defaultFanoutThreshold,
			cfg.Feed.FanoutFollowerThreshold,
		)
	}
	if cfg.Feed.BigAuthorCacheTTL != defaultBigAuthorCacheTTL {
		t.Errorf(
			"expected big author cache TTL %v, got %v",
			defaultBigAuthorCacheTTL,
			cfg.Feed.BigAuthorCacheTTL,
		)
	}
}

func TestLoadRequiresRabbitMQURL(t *testing.T) {
	t.Setenv("MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/test")
	t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
	t.Setenv("RABBITMQ_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing RABBITMQ_URL to fail validation")
	}
}
