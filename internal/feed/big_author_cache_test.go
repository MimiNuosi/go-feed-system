package feed

import (
	"context"
	"testing"
	"time"
)

func TestRedisBigAuthorCache(t *testing.T) {
	client := openTestRedis(t)
	cache := NewRedisBigAuthorCache(client, time.Minute)
	ctx := context.Background()

	authorIDs, hit, err := cache.Get(ctx, 101)
	if err != nil {
		t.Fatalf("get missing cache: %v", err)
	}
	if hit || authorIDs != nil {
		t.Fatalf("expected cache miss, got hit=%t authorIDs=%v", hit, authorIDs)
	}

	if err := cache.Set(ctx, 101, nil); err != nil {
		t.Fatalf("set empty cache: %v", err)
	}
	authorIDs, hit, err = cache.Get(ctx, 101)
	if err != nil {
		t.Fatalf("get empty cache: %v", err)
	}
	if !hit || len(authorIDs) != 0 {
		t.Fatalf("expected cached empty list, got hit=%t authorIDs=%v", hit, authorIDs)
	}

	want := []uint64{10, 20, 30}
	if err := cache.Set(ctx, 202, want); err != nil {
		t.Fatalf("set cache: %v", err)
	}
	got, hit, err := cache.Get(ctx, 202)
	if err != nil {
		t.Fatalf("get cache: %v", err)
	}
	if !hit {
		t.Fatal("expected cache hit")
	}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}

	ttl, err := client.TTL(ctx, bigAuthorCacheKey(202)).Result()
	if err != nil {
		t.Fatalf("get cache TTL: %v", err)
	}
	if ttl <= 0 || ttl > time.Minute {
		t.Fatalf("expected TTL in (0, 1m], got %v", ttl)
	}

	if err := cache.Delete(ctx, 202); err != nil {
		t.Fatalf("delete cache: %v", err)
	}
	_, hit, err = cache.Get(ctx, 202)
	if err != nil {
		t.Fatalf("get deleted cache: %v", err)
	}
	if hit {
		t.Fatal("expected deleted cache to miss")
	}
}
