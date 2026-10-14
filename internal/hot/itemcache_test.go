package hot

import (
	"testing"
	"time"

	"go-feed-system/pkg/feedmodel"
)

func testItems(ids ...uint64) []feedmodel.Item {
	items := make([]feedmodel.Item, 0, len(ids))
	for _, id := range ids {
		items = append(items, feedmodel.Item{ID: id, Title: "v"})
	}
	return items
}

func TestItemCache_TTLAndCopyIsolation(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	cache := newItemCache(time.Second)
	ids := []uint64{3, 1}

	cache.Set(ids, testItems(3, 1), now)

	got, ok := cache.Get(ids, now)
	if !ok {
		t.Fatal("expected a cache hit right after Set")
	}
	if len(got) != 2 || got[0].ID != 3 {
		t.Fatalf("unexpected cached items: %+v", got)
	}

	// 修改取出来的副本，不应影响缓存里的内容
	got[0].IsLikedBy = true
	got[0].Title = "changed"
	again, ok := cache.Get(ids, now)
	if !ok {
		t.Fatal("expected a second cache hit")
	}
	if again[0].IsLikedBy || again[0].Title != "v" {
		t.Fatalf("cache entry was mutated by the caller: %+v", again[0])
	}

	// 过期后未命中
	if _, ok := cache.Get(ids, now.Add(time.Second)); ok {
		t.Fatal("expected the entry to expire")
	}
}

func TestItemCache_KeyDependsOnOrder(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	cache := newItemCache(time.Minute)
	cache.Set([]uint64{1, 2}, testItems(1, 2), now)

	if _, ok := cache.Get([]uint64{2, 1}, now); ok {
		t.Fatal("order matters: different order must be a different key")
	}
	if _, ok := cache.Get([]uint64{1, 2}, now); !ok {
		t.Fatal("same order must hit the cache")
	}
}

func TestItemCache_EvictsWhenFull(t *testing.T) {
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	cache := newItemCache(time.Minute)
	cache.maxEntries = 4

	// 塞满并用掉 TTL，让第一批条目过期
	for i := uint64(1); i <= 4; i++ {
		cache.Set([]uint64{i}, testItems(i), now)
	}
	if got := cache.Size(); got != 4 {
		t.Fatalf("expected 4 entries, got %d", got)
	}

	// 过了一轮 TTL 再写：过期条目应该被回收
	later := now.Add(2 * time.Minute)
	cache.Set([]uint64{9}, testItems(9), later)
	if got := cache.Size(); got != 1 {
		t.Fatalf("expected expired entries to be evicted, got %d entries", got)
	}

	// 未过期且已满时，淘汰最久未访问的那条
	cache2 := newItemCache(time.Hour)
	cache2.maxEntries = 2
	cache2.Set([]uint64{1}, testItems(1), now)
	cache2.Set([]uint64{2}, testItems(2), now)
	if _, ok := cache2.Get([]uint64{1}, now.Add(time.Second)); !ok {
		t.Fatal("expected key 1 to be present")
	}
	cache2.Set([]uint64{3}, testItems(3), now.Add(2*time.Second))
	if _, ok := cache2.Get([]uint64{2}, now.Add(2*time.Second)); ok {
		t.Fatal("expected the least recently used entry (2) to be evicted")
	}
	if _, ok := cache2.Get([]uint64{1}, now.Add(2*time.Second)); !ok {
		t.Fatal("expected the recently used entry (1) to survive")
	}
}

func TestItemCache_NilWhenDisabled(t *testing.T) {
	if cache := newItemCache(0); cache != nil {
		t.Fatal("ttl = 0 should disable the cache")
	}

	// nil 接收者必须安全：禁用缓存时所有方法都要能调用
	var disabled *itemCache
	if _, ok := disabled.Get([]uint64{1}, time.Now()); ok {
		t.Fatal("disabled cache must not hit")
	}
	disabled.Set([]uint64{1}, testItems(1), time.Now())
	if got := disabled.Size(); got != 0 {
		t.Fatalf("disabled cache size = %d, want 0", got)
	}
}
