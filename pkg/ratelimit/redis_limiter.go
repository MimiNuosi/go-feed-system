package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const redisFallbackLogInterval = 5 * time.Second

// redisTokenBucketScript 原子完成“补令牌、clamp、扣令牌、续 TTL”。
//
// KEYS[1] 桶 key
// ARGV[1] 当前时间（微秒）
// ARGV[2] burst
// ARGV[3] 补充一个令牌的间隔（微秒）
// ARGV[4] key 空闲 TTL（毫秒）
var redisTokenBucketScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
local interval_us = tonumber(ARGV[3])
local ttl_ms = tonumber(ARGV[4])

local values = redis.call("HMGET", key, "tokens", "last_refill")
local tokens = tonumber(values[1])
local last_refill = tonumber(values[2])

if tokens == nil then
    tokens = burst
end
if last_refill == nil then
    last_refill = now
end

if now > last_refill then
    local elapsed = now - last_refill
    tokens = math.min(burst, tokens + elapsed / interval_us)
    last_refill = now
end

local allowed = 0
if tokens >= 1 then
    tokens = tokens - 1
    allowed = 1
end

redis.call("HSET", key, "tokens", tokens, "last_refill", last_refill)
redis.call("PEXPIRE", key, ttl_ms)
return { allowed, tokens }
`)

type RedisMetrics interface {
	IncRateLimitBackend(backend string, result string)
}

// RedisKeyedLimiter 使用共享 Redis 令牌桶，并在 Redis 故障时降级到本地桶。
type RedisKeyedLimiter struct {
	client    *redis.Client
	config    Config
	namespace string
	fallback  *KeyedLimiter
	logger    *slog.Logger
	metrics   RedisMetrics
	idleTTL   time.Duration
	now       func() time.Time

	logMu           sync.Mutex
	lastFallbackLog time.Time
}

func NewRedisKeyedLimiter(
	client *redis.Client,
	config Config,
	namespace string,
	logger *slog.Logger,
	metrics RedisMetrics,
) (*RedisKeyedLimiter, error) {
	if client == nil {
		return nil, fmt.Errorf("redis client is nil")
	}
	if namespace == "" {
		return nil, fmt.Errorf("rate limit namespace must not be empty")
	}
	fallback, err := NewKeyedLimiter(config)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisKeyedLimiter{
		client:    client,
		config:    config,
		namespace: namespace,
		fallback:  fallback,
		logger:    logger,
		metrics:   metrics,
		idleTTL:   defaultIdleTTL,
		now:       time.Now,
	}, nil
}

func (l *RedisKeyedLimiter) RetryAfter() time.Duration {
	return l.config.Interval
}

func (l *RedisKeyedLimiter) Allow(
	ctx context.Context,
	key string,
) (bool, error) {
	if key == "" {
		return true, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	now := l.now()
	intervalMicros := l.config.Interval.Microseconds()
	if intervalMicros <= 0 {
		intervalMicros = 1
	}
	redisKey := l.redisKey(key)
	result, err := redisTokenBucketScript.Run(
		ctx,
		l.client,
		[]string{redisKey},
		now.UnixMicro(),
		l.config.Burst,
		intervalMicros,
		l.idleTTL.Milliseconds(),
	).Result()
	if err != nil {
		l.recordBackend("redis", "error")
		l.logFallback(err)
		allowed, fallbackErr := l.fallback.Allow(ctx, key)
		if fallbackErr != nil {
			return false, fallbackErr
		}
		l.recordBackend("local", resultLabel(allowed))
		return allowed, nil
	}

	allowed, err := parseRedisBucketResult(result)
	if err != nil {
		l.recordBackend("redis", "invalid_result")
		l.logFallback(err)
		allowed, fallbackErr := l.fallback.Allow(ctx, key)
		if fallbackErr != nil {
			return false, fallbackErr
		}
		l.recordBackend("local", resultLabel(allowed))
		return allowed, nil
	}
	l.recordBackend("redis", resultLabel(allowed))
	return allowed, nil
}

func (l *RedisKeyedLimiter) redisKey(key string) string {
	return "rate_limit:" + l.namespace + ":" + key
}

func (l *RedisKeyedLimiter) recordBackend(backend string, result string) {
	if l.metrics != nil {
		l.metrics.IncRateLimitBackend(backend, result)
	}
}

func (l *RedisKeyedLimiter) logFallback(err error) {
	now := l.now()
	l.logMu.Lock()
	if !l.lastFallbackLog.IsZero() &&
		now.Sub(l.lastFallbackLog) < redisFallbackLogInterval {
		l.logMu.Unlock()
		return
	}
	l.lastFallbackLog = now
	l.logMu.Unlock()

	l.logger.Warn(
		"redis rate limiter unavailable, using local fallback",
		"namespace", l.namespace,
		"error", err,
	)
}

func parseRedisBucketResult(result any) (bool, error) {
	values, ok := result.([]any)
	if !ok || len(values) < 1 {
		return false, fmt.Errorf("unexpected redis rate limit result %T", result)
	}
	allowed, ok := values[0].(int64)
	if !ok {
		return false, fmt.Errorf("unexpected redis rate limit decision %T", values[0])
	}
	return allowed == 1, nil
}

func resultLabel(allowed bool) string {
	if allowed {
		return "allow"
	}
	return "deny"
}
