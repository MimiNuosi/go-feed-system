package hot

import (
	"strconv"
	"strings"
	"sync"
	"time"

	"go-feed-system/pkg/feedmodel"
)

const (
	// DefaultItemCacheTTL 是热榜读模型缓存的有效期。
	//
	// 取 1 秒是权衡的结果：热榜每页要 3 条 SQL 补齐视频、作者和点赞数，
	// 缓存把这部分从"每请求一次"降到"每秒一次"；
	// 代价是点赞数最多陈旧 1 秒 —— 热榜可以接受，详情接口不走这个缓存。
	DefaultItemCacheTTL = time.Second

	// defaultItemCacheMaxEntries 限制条目数，
	// 避免窗口 / 游标 / 分页大小的组合把内存撑大。
	defaultItemCacheMaxEntries = 64
)

// itemCache 缓存热榜页里"与访问者无关"的部分：
// 视频元数据、作者名、点赞数。
//
// is_liked_by 与访问者相关，绝不进缓存：命中后按请求单独查询再填充，
// 所以同一条缓存可以被所有匿名用户共享。
type itemCache struct {
	ttl        time.Duration
	maxEntries int

	mu      sync.Mutex
	entries map[string]itemCacheEntry
}

type itemCacheEntry struct {
	items     []feedmodel.Item
	expiresAt time.Time
	lastSeen  time.Time
}

// newItemCache 创建读模型缓存。
//
// ttl <= 0 表示关闭缓存（返回 nil，方法都做了 nil 保护），
// 这样调用方能显式选择"不要缓存"，而不是被迫接受一个默认值。
func newItemCache(ttl time.Duration) *itemCache {
	if ttl <= 0 {
		return nil
	}
	return &itemCache{
		ttl:        ttl,
		maxEntries: defaultItemCacheMaxEntries,
		entries:    make(map[string]itemCacheEntry),
	}
}

// itemCacheKey 用有序的视频 ID 列表做缓存键。
//
// 页面内容只取决于这组 ID 以及它们的顺序，所以这个键是完备的。
func itemCacheKey(videoIDs []uint64) string {
	var builder strings.Builder
	for i, id := range videoIDs {
		if i > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(strconv.FormatUint(id, 10))
	}
	return builder.String()
}

// Get 返回缓存条目的副本。命中后调用方可以随意修改它
// （比如填充 is_liked_by），不会污染缓存。
func (c *itemCache) Get(videoIDs []uint64, now time.Time) ([]feedmodel.Item, bool) {
	if c == nil || c.ttl <= 0 {
		return nil, false
	}
	key := itemCacheKey(videoIDs)

	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok || !now.Before(entry.expiresAt) {
		return nil, false
	}

	entry.lastSeen = now
	c.entries[key] = entry

	return cloneItems(entry.items), true
}

// Set 写入缓存，并在条目数达到上限时回收。
func (c *itemCache) Set(videoIDs []uint64, items []feedmodel.Item, now time.Time) {
	if c == nil || c.ttl <= 0 {
		return
	}
	key := itemCacheKey(videoIDs)
	stored := cloneItems(items)

	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= c.maxEntries {
		c.evictLocked(now)
	}

	c.entries[key] = itemCacheEntry{
		items:     stored,
		expiresAt: now.Add(c.ttl),
		lastSeen:  now,
	}
}

// Size 返回当前条目数，用于观测与测试。
func (c *itemCache) Size() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// evictLocked 先清理过期条目；仍然满员就淘汰最久未被访问的那条。
// 调用方必须持有锁。
func (c *itemCache) evictLocked(now time.Time) {
	for key, entry := range c.entries {
		if !now.Before(entry.expiresAt) {
			delete(c.entries, key)
		}
	}
	if len(c.entries) < c.maxEntries {
		return
	}

	var (
		oldestKey string
		oldest    time.Time
	)
	for key, entry := range c.entries {
		if oldestKey == "" || entry.lastSeen.Before(oldest) {
			oldestKey = key
			oldest = entry.lastSeen
		}
	}
	delete(c.entries, oldestKey)
}

// cloneItems 复制一份条目切片。
//
// feedmodel.Item 里的字段都是值类型（没有指针和切片），
// 所以浅拷贝就是完整拷贝。
func cloneItems(items []feedmodel.Item) []feedmodel.Item {
	copied := make([]feedmodel.Item, len(items))
	copy(copied, items)
	return copied
}
