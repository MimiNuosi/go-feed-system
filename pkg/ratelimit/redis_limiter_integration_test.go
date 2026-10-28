package ratelimit

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type fakeRedisMetrics struct {
	backendResults map[string]int
}

func (m *fakeRedisMetrics) IncRateLimitBackend(backend string, result string) {
	if m.backendResults == nil {
		m.backendResults = make(map[string]int)
	}
	m.backendResults[backend+":"+result]++
}

func TestRedisKeyedLimiterSharesBucketAcrossInstances(t *testing.T) {
	client := openTestRedisClient(t)
	defer client.Close()

	metrics := &fakeRedisMetrics{}
	config := Config{Burst: 3, Interval: time.Hour}
	namespace := "integration:" + uuid.NewString()
	first, err := NewRedisKeyedLimiter(client, config, namespace, nil, metrics)
	if err != nil {
		t.Fatalf("create first limiter: %v", err)
	}
	second, err := NewRedisKeyedLimiter(client, config, namespace, nil, metrics)
	if err != nil {
		t.Fatalf("create second limiter: %v", err)
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	first.now = func() time.Time { return now }
	second.now = func() time.Time { return now }

	tests := []struct {
		limiter *RedisKeyedLimiter
		want    bool
	}{
		{limiter: first, want: true},
		{limiter: second, want: true},
		{limiter: first, want: true},
		{limiter: second, want: false},
	}
	for i, tt := range tests {
		allowed, err := tt.limiter.Allow(context.Background(), "same-user")
		if err != nil {
			t.Fatalf("step %d: Allow error: %v", i, err)
		}
		if allowed != tt.want {
			t.Fatalf("step %d: allowed=%v, want %v", i, allowed, tt.want)
		}
	}
	if metrics.backendResults["redis:allow"] != 3 {
		t.Fatalf("redis allow metrics = %d, want 3", metrics.backendResults["redis:allow"])
	}
	if metrics.backendResults["redis:deny"] != 1 {
		t.Fatalf("redis deny metrics = %d, want 1", metrics.backendResults["redis:deny"])
	}
	ttl, err := client.PTTL(context.Background(), first.redisKey("same-user")).Result()
	if err != nil || ttl <= 0 {
		t.Fatalf("expected shared bucket TTL, got ttl=%s err=%v", ttl, err)
	}
}

func TestRedisKeyedLimiterFallsBackWhenRedisFails(t *testing.T) {
	client := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		MaxRetries:   -1,
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	defer client.Close()

	metrics := &fakeRedisMetrics{}
	limiter, err := NewRedisKeyedLimiter(
		client,
		Config{Burst: 1, Interval: time.Hour},
		"fallback:"+uuid.NewString(),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		metrics,
	)
	if err != nil {
		t.Fatalf("create limiter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	startedAt := time.Now()
	first, err := limiter.Allow(ctx, "user")
	if err != nil {
		t.Fatalf("first Allow error: %v", err)
	}
	second, err := limiter.Allow(ctx, "user")
	if err != nil {
		t.Fatalf("second Allow error: %v", err)
	}
	if !first || second {
		t.Fatalf("fallback decisions = %v, %v; want true, false", first, second)
	}
	if elapsed := time.Since(startedAt); elapsed > 500*time.Millisecond {
		t.Fatalf("fallback was too slow: %s", elapsed)
	}
	if metrics.backendResults["redis:error"] < 2 {
		t.Fatalf("redis error metrics = %d, want at least 2", metrics.backendResults["redis:error"])
	}
	if metrics.backendResults["local:allow"] != 1 ||
		metrics.backendResults["local:deny"] != 1 {
		t.Fatalf("unexpected local metrics: %v", metrics.backendResults)
	}
}

func TestParseRedisBucketResult(t *testing.T) {
	allowed, err := parseRedisBucketResult([]any{int64(1), "2.5"})
	if err != nil || !allowed {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
	if _, err := parseRedisBucketResult("bad"); err == nil {
		t.Fatal("expected invalid result error")
	}
}

func openTestRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set TEST_REDIS_ADDR to run Redis integration test")
	}
	db := 0
	if raw := os.Getenv("TEST_REDIS_DB"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			t.Fatalf("invalid TEST_REDIS_DB: %v", err)
		}
		db = value
	}
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("TEST_REDIS_PASSWORD"),
		DB:       db,
	})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Fatalf("ping redis: %v", err)
	}
	return client
}
