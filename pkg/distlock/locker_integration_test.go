package distlock

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisLockerMutualExclusionAndRelease(t *testing.T) {
	client := openLockerTestRedis(t)
	defer client.Close()
	locker := NewRedisLocker(client)
	key := "test:lock:" + uuid.NewString()

	releaseFirst, err := locker.Acquire(context.Background(), key, time.Second)
	if err != nil {
		t.Fatalf("acquire first: %v", err)
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := locker.Acquire(waitCtx, key, time.Second); err == nil {
		t.Fatal("expected second acquire to wait until timeout")
	}

	if err := releaseFirst(); err != nil {
		t.Fatalf("release first: %v", err)
	}
	if err := releaseFirst(); err != nil {
		t.Fatalf("second release should be idempotent: %v", err)
	}

	releaseSecond, err := locker.Acquire(context.Background(), key, time.Second)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	if err := releaseSecond(); err != nil {
		t.Fatalf("release second: %v", err)
	}
}

func openLockerTestRedis(t *testing.T) *redis.Client {
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
