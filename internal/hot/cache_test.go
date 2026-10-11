package hot

import (
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
