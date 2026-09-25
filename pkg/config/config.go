package config

import (
	"fmt"
	"os"
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
)

// Config 保存应用启动时需要的全部配置。
// 依赖从 main 显式传给各个组件，不通过全局变量获取。
type Config struct {
	HTTP  HTTPConfig
	MySQL MySQLConfig
	Auth  AuthConfig
}

type HTTPConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
	ShutdownTimeout   time.Duration
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

// Load 从环境变量和默认值构造配置。
//
// 当前只支持少量环境变量，后续增加 MySQL、Redis、RabbitMQ 时，
// 继续在这里集中解析，不要把 os.Getenv 散落到业务代码中。
func Load() (Config, error) {
	cfg := Config{
		HTTP: HTTPConfig{
			Addr:              envOrDefault("HTTP_ADDR", defaultHTTPAddr),
			ReadHeaderTimeout: defaultReadHeaderTimeout,
			ShutdownTimeout:   defaultShutdownTimeout,
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

	return nil
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}

	return value
}
