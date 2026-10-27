package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPAddr          = "127.0.0.1:8080"
	defaultReadHeaderTimeout = 5 * time.Second
	defaultShutdownTimeout   = 10 * time.Second
	defaultJWTIssuer         = "go-feed-system"
	defaultJWTTTL            = 2 * time.Hour
	defaultMySQLMaxOpenConns = 20
	defaultMySQLMaxIdleConns = 5
	defaultMySQLConnLifetime = 30 * time.Minute
	defaultLocalStorageDir   = "./data/videos"
	defaultOutboxPublish     = time.Second

	defaultRabbitMQExchange      = "feed.events"
	defaultRabbitMQExchangeType  = "topic"
	defaultRabbitMQRoutingKey    = "video.published"
	defaultRabbitMQQueue         = "feed.fanout.video.published"
	defaultRabbitMQDLX           = "feed.dlx"
	defaultRabbitMQDLQ           = "feed.fanout.video.published.dlq"
	defaultRabbitMQRetryExchange = "feed.retry"
	defaultRabbitMQRetryQueue    = "feed.fanout.video.published.retry"
	defaultRabbitMQMaxRetries    = 3
	defaultRabbitMQRetryDelay    = 5 * time.Second
	defaultFanoutThreshold       = 1000
	defaultBigAuthorCacheTTL     = time.Minute

	defaultRateLimitEnabled          = true
	defaultRateLimitGlobalBurst      = 2000
	defaultRateLimitGlobalInterval   = time.Millisecond
	defaultRateLimitIdentityBurst    = 480
	defaultRateLimitIdentityInterval = 250 * time.Millisecond
)

// Config 保存应用启动时需要的全部配置。
// 依赖从 main 显式传给各个组件，不通过全局变量获取。
type Config struct {
	HTTP      HTTPConfig
	MySQL     MySQLConfig
	Auth      AuthConfig
	Storage   StorageConfig
	Redis     RedisConfig
	Outbox    OutboxConfig
	RabbitMQ  RabbitMQConfig
	Feed      FeedConfig
	RateLimit RateLimitConfig
}

type HTTPConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
	TrustedProxies    []string
}

type MySQLConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

type AuthConfig struct {
	JWTSecret string
	JWTIssuer string
	JWTTTL    time.Duration
}

type StorageConfig struct {
	LocalDir string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type OutboxConfig struct {
	PublishInterval time.Duration
}

type RabbitMQConfig struct {
	URL           string
	Exchange      string
	ExchangeType  string
	RoutingKey    string
	Queue         string
	DLX           string
	DLQ           string
	RetryExchange string
	RetryQueue    string
	MaxRetries    int
	RetryDelay    time.Duration
}

type FeedConfig struct {
	FanoutFollowerThreshold int
	BigAuthorCacheTTL       time.Duration
}

// RateLimitConfig 控制两层令牌桶限流的额度。
//
// 为什么放在配置里而不是写死常量：压测和排障时需要临时放开额度。
// 限流额度不改的话，压到某个速率之后返回的全是 429，
// 测到的是限流器的天花板，而不是系统本身的能力。
type RateLimitConfig struct {
	// Enabled 允许整体关闭限流，压测基线时用。
	Enabled bool
	// Global 是所有请求共用的桶。
	Global RateLimitBucket
	// Identity 是登录用户各自一个的桶。
	Identity RateLimitBucket
}

// RateLimitBucket 是一个令牌桶的容量和补充速度。
type RateLimitBucket struct {
	Burst    int
	Interval time.Duration
}

// Load 从环境变量和默认值构造配置。
//
// 当前只支持少量环境变量，后续增加 MySQL、Redis、RabbitMQ 时，
// 继续在这里集中解析，不要把 os.Getenv 散落到业务代码中。
func Load() (Config, error) {
	outboxPublishInterval, err := durationFromEnv(
		"OUTBOX_PUBLISH_INTERVAL",
		defaultOutboxPublish,
	)
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	rabbitMQRetryDelay, err := durationFromEnv(
		"RABBITMQ_RETRY_DELAY",
		defaultRabbitMQRetryDelay,
	)
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	bigAuthorCacheTTL, err := durationFromEnv(
		"FEED_BIG_AUTHOR_CACHE_TTL",
		defaultBigAuthorCacheTTL,
	)
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	rateLimitGlobalInterval, err := durationFromEnv(
		"RATE_LIMIT_GLOBAL_INTERVAL",
		defaultRateLimitGlobalInterval,
	)
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	rateLimitIdentityInterval, err := durationFromEnv(
		"RATE_LIMIT_IDENTITY_INTERVAL",
		defaultRateLimitIdentityInterval,
	)
	if err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}

	cfg := Config{
		HTTP: HTTPConfig{
			Addr:              envOrDefault("HTTP_ADDR", defaultHTTPAddr),
			ReadHeaderTimeout: defaultReadHeaderTimeout,
			ShutdownTimeout:   defaultShutdownTimeout,
			TrustedProxies:    csvFromEnv("HTTP_TRUSTED_PROXIES"),
		},
		MySQL: MySQLConfig{
			DSN:             strings.TrimSpace(os.Getenv("MYSQL_DSN")),
			MaxOpenConns:    defaultMySQLMaxOpenConns,
			MaxIdleConns:    defaultMySQLMaxIdleConns,
			ConnMaxLifetime: defaultMySQLConnLifetime,
		},
		Auth: AuthConfig{
			JWTSecret: strings.TrimSpace(os.Getenv("JWT_SECRET")),
			JWTIssuer: defaultJWTIssuer,
			JWTTTL:    defaultJWTTTL,
		},
		Storage: StorageConfig{
			LocalDir: envOrDefault("LOCAL_STORAGE_DIR", defaultLocalStorageDir),
		},
		Redis: RedisConfig{
			Addr:     envOrDefault("REDIS_ADDR", "127.0.0.1:6379"),
			Password: os.Getenv("REDIS_PASSWORD"),
			DB:       envOrDefaultInt("REDIS_DB", 0), // 注意：需要新增一个 int 类型的解析函数
		},
		Outbox: OutboxConfig{
			PublishInterval: outboxPublishInterval,
		},
		RabbitMQ: RabbitMQConfig{
			URL:           strings.TrimSpace(os.Getenv("RABBITMQ_URL")),
			Exchange:      envOrDefault("RABBITMQ_EXCHANGE", defaultRabbitMQExchange),
			ExchangeType:  envOrDefault("RABBITMQ_EXCHANGE_TYPE", defaultRabbitMQExchangeType),
			RoutingKey:    envOrDefault("RABBITMQ_ROUTING_KEY", defaultRabbitMQRoutingKey),
			Queue:         envOrDefault("RABBITMQ_QUEUE", defaultRabbitMQQueue),
			DLX:           envOrDefault("RABBITMQ_DLX", defaultRabbitMQDLX),
			DLQ:           envOrDefault("RABBITMQ_DLQ", defaultRabbitMQDLQ),
			RetryExchange: envOrDefault("RABBITMQ_RETRY_EXCHANGE", defaultRabbitMQRetryExchange),
			RetryQueue:    envOrDefault("RABBITMQ_RETRY_QUEUE", defaultRabbitMQRetryQueue),
			MaxRetries:    envOrDefaultInt("RABBITMQ_MAX_RETRIES", defaultRabbitMQMaxRetries),
			RetryDelay:    rabbitMQRetryDelay,
		},
		Feed: FeedConfig{
			FanoutFollowerThreshold: envOrDefaultInt(
				"FEED_FANOUT_FOLLOWER_THRESHOLD",
				defaultFanoutThreshold,
			),
			BigAuthorCacheTTL: bigAuthorCacheTTL,
		},
		RateLimit: RateLimitConfig{
			Enabled: boolFromEnv("RATE_LIMIT_ENABLED", defaultRateLimitEnabled),
			Global: RateLimitBucket{
				Burst: envOrDefaultInt(
					"RATE_LIMIT_GLOBAL_BURST",
					defaultRateLimitGlobalBurst,
				),
				Interval: rateLimitGlobalInterval,
			},
			Identity: RateLimitBucket{
				Burst: envOrDefaultInt(
					"RATE_LIMIT_IDENTITY_BURST",
					defaultRateLimitIdentityBurst,
				),
				Interval: rateLimitIdentityInterval,
			},
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func (c Config) validate() error {
	if strings.TrimSpace(c.HTTP.Addr) == "" {
		return fmt.Errorf("HTTP_ADDR must not be empty")
	}
	if c.HTTP.ReadHeaderTimeout <= 0 {
		return fmt.Errorf("HTTP read header timeout must be positive")
	}
	if c.HTTP.ShutdownTimeout <= 0 {
		return fmt.Errorf("HTTP shutdown timeout must be positive")
	}
	for _, proxy := range c.HTTP.TrustedProxies {
		if net.ParseIP(proxy) == nil {
			if _, _, err := net.ParseCIDR(proxy); err != nil {
				return fmt.Errorf("invalid HTTP trusted proxy %q", proxy)
			}
		}
	}
	if strings.TrimSpace(c.MySQL.DSN) == "" {
		return fmt.Errorf("MYSQL_DSN must not be empty")
	}
	if c.MySQL.MaxOpenConns <= 0 {
		return fmt.Errorf("MySQL max open connections must be positive")
	}
	if c.MySQL.MaxIdleConns < 0 {
		return fmt.Errorf("MySQL max idle connections must not be negative")
	}
	if c.MySQL.ConnMaxLifetime <= 0 {
		return fmt.Errorf("MySQL connection max lifetime must be positive")
	}
	if len(c.Auth.JWTSecret) < 32 {
		return fmt.Errorf("JWT_SECRET must be at least 32 bytes")
	}
	if strings.TrimSpace(c.Auth.JWTIssuer) == "" {
		return fmt.Errorf("JWT issuer must not be empty")
	}
	if c.Auth.JWTTTL <= 0 {
		return fmt.Errorf("JWT TTL must be positive")
	}
	if strings.TrimSpace(c.Storage.LocalDir) == "" {
		return fmt.Errorf("local storage dir must not be empty")
	}
	if strings.TrimSpace(c.Redis.Addr) == "" {
		return fmt.Errorf("REDIS_ADDR must not be empty")
	}
	if c.Redis.DB < 0 {
		return fmt.Errorf("REDIS_DB must not be negative")
	}
	if c.Outbox.PublishInterval <= 0 {
		return fmt.Errorf("OUTBOX_PUBLISH_INTERVAL must be positive")
	}
	if strings.TrimSpace(c.RabbitMQ.URL) == "" {
		return fmt.Errorf("RABBITMQ_URL must not be empty")
	}
	if strings.TrimSpace(c.RabbitMQ.Exchange) == "" {
		return fmt.Errorf("RABBITMQ_EXCHANGE must not be empty")
	}
	if strings.TrimSpace(c.RabbitMQ.ExchangeType) == "" {
		return fmt.Errorf("RABBITMQ_EXCHANGE_TYPE must not be empty")
	}
	if strings.TrimSpace(c.RabbitMQ.RoutingKey) == "" {
		return fmt.Errorf("RABBITMQ_ROUTING_KEY must not be empty")
	}
	if strings.TrimSpace(c.RabbitMQ.Queue) == "" {
		return fmt.Errorf("RABBITMQ_QUEUE must not be empty")
	}
	if strings.TrimSpace(c.RabbitMQ.DLX) == "" {
		return fmt.Errorf("RABBITMQ_DLX must not be empty")
	}
	if strings.TrimSpace(c.RabbitMQ.DLQ) == "" {
		return fmt.Errorf("RABBITMQ_DLQ must not be empty")
	}
	if strings.TrimSpace(c.RabbitMQ.RetryExchange) == "" {
		return fmt.Errorf("RABBITMQ_RETRY_EXCHANGE must not be empty")
	}
	if strings.TrimSpace(c.RabbitMQ.RetryQueue) == "" {
		return fmt.Errorf("RABBITMQ_RETRY_QUEUE must not be empty")
	}
	if c.RabbitMQ.MaxRetries < 0 {
		return fmt.Errorf("RABBITMQ_MAX_RETRIES must not be negative")
	}
	if c.RabbitMQ.RetryDelay <= 0 {
		return fmt.Errorf("RABBITMQ_RETRY_DELAY must be positive")
	}
	if c.Feed.FanoutFollowerThreshold <= 0 {
		return fmt.Errorf("FEED_FANOUT_FOLLOWER_THRESHOLD must be positive")
	}
	if c.Feed.BigAuthorCacheTTL <= 0 {
		return fmt.Errorf("FEED_BIG_AUTHOR_CACHE_TTL must be positive")
	}
	if c.RateLimit.Global.Burst <= 0 {
		return fmt.Errorf("RATE_LIMIT_GLOBAL_BURST must be positive")
	}
	if c.RateLimit.Global.Interval <= 0 {
		return fmt.Errorf("RATE_LIMIT_GLOBAL_INTERVAL must be positive")
	}
	if c.RateLimit.Identity.Burst <= 0 {
		return fmt.Errorf("RATE_LIMIT_IDENTITY_BURST must be positive")
	}
	if c.RateLimit.Identity.Interval <= 0 {
		return fmt.Errorf("RATE_LIMIT_IDENTITY_INTERVAL must be positive")
	}

	return nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}

func envOrDefaultInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func csvFromEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func durationFromEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a valid duration: %w", key, err)
	}

	return parsed, nil
}

func boolFromEnv(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
