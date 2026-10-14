package hot

import (
	"sync"
	"time"
)

// freshTTL 和 staleTTL 是本地缓存的两段生命周期。
// fresh: 0 到 freshTTL 之间，缓存直接命中。
// stale: freshTTL 到 staleTTL 之间，只在 Redis 故障时用于降级。
const (
	defaultFreshTTL = 5 * time.Second
	defaultStaleTTL = 30 * time.Second

	// defaultMaxEntries 限制本地榜单缓存的条目数。
	//
	// 缓存键里带时间桶，每过一分钟就会多一条新键；不设上限的话，
	// 进程跑一整天会累积上千条永远不会被清理的过期条目。
	defaultMaxEntries = 64
)

type cachedHotPage struct {
	videos     []RankedVideo
	freshUntil time.Time
	staleUntil time.Time
}

type localHotCache struct {
	mu         sync.RWMutex
	entries    map[string]cachedHotPage
	maxEntries int

	freshTTL time.Duration
	staleTTL time.Duration
}

func newLocalHotCache(freshTTL, staleTTL time.Duration) *localHotCache {
	if freshTTL <= 0 {
		freshTTL = defaultFreshTTL
	}
	if staleTTL <= freshTTL {
		staleTTL = defaultStaleTTL
	}
	return &localHotCache{
		entries:    make(map[string]cachedHotPage),
		maxEntries: defaultMaxEntries,
		freshTTL:   freshTTL,
		staleTTL:   staleTTL,
	}
}

// GetFresh 返回 fresh 状态下的缓存数据。
// 命中条件：now < freshUntil。
func (c *localHotCache) GetFresh(key string, now time.Time) ([]RankedVideo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}
	if !now.Before(entry.freshUntil) {
		return nil, false
	}
	return entry.videos, true
}

// GetStale 返回 stale 状态下的缓存数据。
// 命中条件：now < staleUntil（含 fresh 期间）。
// 只在 Redis 故障时调用。
func (c *localHotCache) GetStale(key string, now time.Time) ([]RankedVideo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, exists := c.entries[key]
	if !exists {
		return nil, false
	}
	if !now.Before(entry.staleUntil) {
		return nil, false
	}
	return entry.videos, true
}

// Set 写入一条缓存，同时计算 freshUntil 和 staleUntil，并在满员时回收。
func (c *localHotCache) Set(key string, videos []RankedVideo, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxEntries {
		c.evictLocked(now)
	}

	freshUntil := now.Add(c.freshTTL)
	staleUntil := now.Add(c.staleTTL)
	c.entries[key] = cachedHotPage{
		videos:     append([]RankedVideo(nil), videos...),
		freshUntil: freshUntil,
		staleUntil: staleUntil,
	}
}

// Size 返回当前条目数，用于观测与测试。
func (c *localHotCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.entries)
}

// evictLocked 先清理已经过期的条目；仍然满员就淘汰最早过期的那条
// （过期时间最早 = 写入最早）。
//
// 读路径只用 RLock、不更新"最后访问时间"，所以这里按写入时间淘汰而不是
// 严格 LRU —— 对一个秒级 TTL 的缓存来说，这个精度足够，
// 而换来的是读路径不加写锁。
//
// 调用方必须持有写锁。
func (c *localHotCache) evictLocked(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.staleUntil) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) < c.maxEntries {
		return
	}

	var (
		oldestKey  string
		oldestTime time.Time
	)
	for key, entry := range c.entries {
		if oldestKey == "" || entry.staleUntil.Before(oldestTime) {
			oldestKey = key
			oldestTime = entry.staleUntil
		}
	}
	delete(c.entries, oldestKey)
}
