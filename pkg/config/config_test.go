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

func TestLoadRateLimitConfig(t *testing.T) {
	setMinimalValidEnv := func(t *testing.T) {
		t.Helper()
		t.Setenv("MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/test")
		t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
		t.Setenv("RABBITMQ_URL", "amqp://feed_app:secret@127.0.0.1:5672/")
	}

	clearRateLimitEnv := func(t *testing.T) {
		t.Helper()
		for _, key := range []string{
			"RATE_LIMIT_ENABLED",
			"RATE_LIMIT_GLOBAL_BURST",
			"RATE_LIMIT_GLOBAL_INTERVAL",
			"RATE_LIMIT_IDENTITY_BURST",
			"RATE_LIMIT_IDENTITY_INTERVAL",
		} {
			t.Setenv(key, "")
		}
	}

	t.Run("默认开启并使用默认额度", func(t *testing.T) {
		setMinimalValidEnv(t)
		clearRateLimitEnv(t)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if !cfg.RateLimit.Enabled {
			t.Error("expected rate limiting to be enabled by default")
		}
		if cfg.RateLimit.Global.Burst != defaultRateLimitGlobalBurst {
			t.Errorf(
				"expected global burst %d, got %d",
				defaultRateLimitGlobalBurst,
				cfg.RateLimit.Global.Burst,
			)
		}
		if cfg.RateLimit.Global.Interval != defaultRateLimitGlobalInterval {
			t.Errorf(
				"expected global interval %v, got %v",
				defaultRateLimitGlobalInterval,
				cfg.RateLimit.Global.Interval,
			)
		}
		if cfg.RateLimit.Identity.Burst != defaultRateLimitIdentityBurst {
			t.Errorf(
				"expected identity burst %d, got %d",
				defaultRateLimitIdentityBurst,
				cfg.RateLimit.Identity.Burst,
			)
		}
		if cfg.RateLimit.Identity.Interval != defaultRateLimitIdentityInterval {
			t.Errorf(
				"expected identity interval %v, got %v",
				defaultRateLimitIdentityInterval,
				cfg.RateLimit.Identity.Interval,
			)
		}
	})

	t.Run("可以覆盖额度用于压测", func(t *testing.T) {
		setMinimalValidEnv(t)
		clearRateLimitEnv(t)
		t.Setenv("RATE_LIMIT_GLOBAL_BURST", "100000")
		t.Setenv("RATE_LIMIT_GLOBAL_INTERVAL", "10us")
		t.Setenv("RATE_LIMIT_IDENTITY_BURST", "50000")
		t.Setenv("RATE_LIMIT_IDENTITY_INTERVAL", "10us")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if cfg.RateLimit.Global.Burst != 100000 {
			t.Errorf("expected global burst 100000, got %d", cfg.RateLimit.Global.Burst)
		}
		if cfg.RateLimit.Global.Interval != 10*time.Microsecond {
			t.Errorf(
				"expected global interval 10us, got %v",
				cfg.RateLimit.Global.Interval,
			)
		}
	})

	t.Run("可以整体关闭限流", func(t *testing.T) {
		setMinimalValidEnv(t)
		clearRateLimitEnv(t)
		t.Setenv("RATE_LIMIT_ENABLED", "false")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if cfg.RateLimit.Enabled {
			t.Error("expected rate limiting to be disabled")
		}
	})

	t.Run("非法时长报错", func(t *testing.T) {
		setMinimalValidEnv(t)
		clearRateLimitEnv(t)
		t.Setenv("RATE_LIMIT_GLOBAL_INTERVAL", "fast")

		if _, err := Load(); err == nil {
			t.Fatal("expected an invalid duration to fail")
		}
	})

	t.Run("额度非正数时报错", func(t *testing.T) {
		setMinimalValidEnv(t)
		clearRateLimitEnv(t)
		t.Setenv("RATE_LIMIT_GLOBAL_BURST", "0")

		if _, err := Load(); err == nil {
			t.Fatal("expected a non-positive burst to fail validation")
		}
	})
}

func TestLoadTrustedProxies(t *testing.T) {
	setMinimalEnv := func(t *testing.T) {
		t.Helper()
		t.Setenv("MYSQL_DSN", "user:pass@tcp(127.0.0.1:3306)/test")
		t.Setenv("JWT_SECRET", strings.Repeat("x", 32))
		t.Setenv("RABBITMQ_URL", "amqp://feed_app:secret@127.0.0.1:5672/")
	}

	t.Run("解析多个代理", func(t *testing.T) {
		setMinimalEnv(t)
		t.Setenv("HTTP_TRUSTED_PROXIES", "172.16.0.0/12, 127.0.0.1")

		cfg, err := Load()
		if err != nil {
			t.Fatalf("load config: %v", err)
		}
		if len(cfg.HTTP.TrustedProxies) != 2 {
			t.Fatalf("trusted proxies = %v", cfg.HTTP.TrustedProxies)
		}
	})

	t.Run("拒绝非法代理", func(t *testing.T) {
		setMinimalEnv(t)
		t.Setenv("HTTP_TRUSTED_PROXIES", "not-a-proxy")

		if _, err := Load(); err == nil {
			t.Fatal("expected invalid trusted proxy to fail")
		}
	})
}
