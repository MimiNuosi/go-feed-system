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
)

type cachedHotPage struct {
	videos     []RankedVideo
	freshUntil time.Time
	staleUntil time.Time
}

type localHotCache struct {
	mu      sync.RWMutex
	entries map[string]cachedHotPage

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
		entries:  make(map[string]cachedHotPage),
		freshTTL: freshTTL,
		staleTTL: staleTTL,
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

// Set 写入一条缓存，同时计算 freshUntil 和 staleUntil。
func (c *localHotCache) Set(key string, videos []RankedVideo, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	freshUntil := now.Add(c.freshTTL)
	staleUntil := now.Add(c.staleTTL)
	c.entries[key] = cachedHotPage{
		videos:     append([]RankedVideo(nil), videos...),
		freshUntil: freshUntil,
		staleUntil: staleUntil,
	}
}
