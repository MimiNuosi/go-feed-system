package leader

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestRedisStoreLeaseLifecycle(t *testing.T) {
	client := openLeaderTestRedis(t)
	defer client.Close()
	store := NewRedisStore(client)
	name := "integration:" + uuid.NewString()
	ctx := context.Background()

	acquired, err := store.Acquire(ctx, name, "owner-1", time.Second)
	if err != nil || !acquired {
		t.Fatalf("acquire = %v, err=%v", acquired, err)
	}
	acquired, err = store.Acquire(ctx, name, "owner-2", time.Second)
	if err != nil || acquired {
		t.Fatalf("second acquire = %v, err=%v", acquired, err)
	}
	renewed, err := store.Renew(ctx, name, "owner-2", time.Second)
	if err != nil || renewed {
		t.Fatalf("wrong owner renewed=%v err=%v", renewed, err)
	}
	renewed, err = store.Renew(ctx, name, "owner-1", time.Second)
	if err != nil || !renewed {
		t.Fatalf("owner renew = %v, err=%v", renewed, err)
	}
	if err := store.Release(ctx, name, "owner-1"); err != nil {
		t.Fatalf("release: %v", err)
	}
	acquired, err = store.Acquire(ctx, name, "owner-2", time.Second)
	if err != nil || !acquired {
		t.Fatalf("acquire after release = %v, err=%v", acquired, err)
	}
	_ = store.Release(ctx, name, "owner-2")
}

func openLeaderTestRedis(t *testing.T) *redis.Client {
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
