package hot

import (
	"fmt"
	"testing"
	"time"
)

func TestLocalHotCache_FreshAndStaleBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	cache := newLocalHotCache(5*time.Second, 30*time.Second)
	videos := []RankedVideo{{VideoID: 1, Score: 10}}
	cache.Set("key", videos, now)

	tests := []struct {
		name      string
		at        time.Time
		wantFresh bool
		wantStale bool
	}{
		{
			name:      "fresh 期间",
			at:        now.Add(4 * time.Second),
			wantFresh: true,
			wantStale: true,
		},
		{
			name:      "fresh 边界应过期",
			at:        now.Add(5 * time.Second),
			wantFresh: false,
			wantStale: true,
		},
		{
			name:      "stale 期间",
			at:        now.Add(20 * time.Second),
			wantFresh: false,
			wantStale: true,
		},
		{
			name:      "stale 边界应过期",
			at:        now.Add(30 * time.Second),
			wantFresh: false,
			wantStale: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, fresh := cache.GetFresh("key", tt.at)
			_, stale := cache.GetStale("key", tt.at)
			if fresh != tt.wantFresh {
				t.Fatalf("expected fresh=%t, got %t", tt.wantFresh, fresh)
			}
			if stale != tt.wantStale {
				t.Fatalf("expected stale=%t, got %t", tt.wantStale, stale)
			}
		})
	}
}

func TestLocalHotCache_SetCopiesInput(t *testing.T) {
	now := time.Now()
	cache := newLocalHotCache(5*time.Second, 30*time.Second)
	videos := []RankedVideo{{VideoID: 1, Score: 10}}

	cache.Set("key", videos, now)
	videos[0].VideoID = 999

	got, ok := cache.GetFresh("key", now)
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got[0].VideoID != 1 {
		t.Fatalf("cache was mutated through input slice: %v", got)
	}
}

func TestNewLocalHotCache_Defaults(t *testing.T) {
	cache := newLocalHotCache(0, 0)
	if cache.freshTTL != defaultFreshTTL {
		t.Fatalf("expected fresh TTL %v, got %v", defaultFreshTTL, cache.freshTTL)
	}
	if cache.staleTTL != defaultStaleTTL {
		t.Fatalf("expected stale TTL %v, got %v", defaultStaleTTL, cache.staleTTL)
	}
}

func TestLocalHotCache_EvictsExpiredEntries(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	cache := newLocalHotCache(5*time.Second, 30*time.Second)
	cache.maxEntries = 3

	for i := 0; i < 3; i++ {
		cache.Set(
			fmt.Sprintf("bucket-%d", i),
			[]RankedVideo{{VideoID: uint64(i)}},
			now,
		)
	}
	if got := cache.Size(); got != 3 {
		t.Fatalf("expected 3 entries, got %d", got)
	}

	// 全部 stale 过期后再写新键：过期条目应该被回收，只留新的一条
	later := now.Add(2 * time.Minute)
	cache.Set("fresh", []RankedVideo{{VideoID: 9}}, later)

	if got := cache.Size(); got != 1 {
		t.Fatalf("expected expired entries to be evicted, got %d entries", got)
	}
	if _, ok := cache.GetStale("fresh", later); !ok {
		t.Fatal("expected the new entry to be readable")
	}
}

func TestLocalHotCache_EvictsOldestWhenFull(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	cache := newLocalHotCache(5*time.Second, time.Hour) // TTL 长到不会自然过期
	cache.maxEntries = 2
	videos := []RankedVideo{{VideoID: 1}}

	cache.Set("oldest", videos, now)
	cache.Set("newer", videos, now.Add(time.Second))
	cache.Set("newest", videos, now.Add(2*time.Second))

	if _, ok := cache.GetStale("oldest", now.Add(2*time.Second)); ok {
		t.Fatal("expected the entry with the earliest expiry to be evicted")
	}
	for _, key := range []string{"newer", "newest"} {
		if _, ok := cache.GetStale(key, now.Add(2*time.Second)); !ok {
			t.Fatalf("expected %q to survive", key)
		}
	}
}

func TestLocalHotCache_SizeIsSafeWithoutEntries(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	cache := newLocalHotCache(0, 0)

	cache.Set("a", []RankedVideo{{VideoID: 1}}, now)
	// 读路径不更新任何状态，Size 只反映写入过的条目
	if _, ok := cache.GetFresh("a", now); !ok {
		t.Fatal("expected a fresh hit")
	}
	if got := cache.Size(); got != 1 {
		t.Fatalf("expected 1 entry, got %d", got)
	}
}
