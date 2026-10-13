// Package ratelimit 提供按 key 维度的进程内令牌桶限流。
//
// 为什么额度放在进程内存里：当前只有一个 api 实例，本地额度就是精确额度。
// 水平扩展后每个实例会各自持有完整额度（集群总额度变成 N 倍），
// 那时需要换成 Redis + Lua 做全局额度，或者在构造时按实例数分摊。
// 另外，保护系统的东西不应该依赖系统里最脆弱的组件：
// 每次请求都去 Redis 取令牌，等于在流量峰值时给 Redis 加压。
package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

const (
	// defaultIdleTTL 是空闲桶的保留时间。
	defaultIdleTTL = 10 * time.Minute
	// defaultSweepEvery 是两次清扫之间的最小间隔。
	//
	// 每次请求都全量扫一遍是 O(n)，高频请求下会退化成 O(n^2)，
	// 所以这里限制清扫频率。
	defaultSweepEvery = time.Minute
)

// Config 描述一个令牌桶的容量和补充速度。
type Config struct {
	// Burst 是桶容量，也就是允许的瞬时突发请求数。
	Burst int
	// Interval 是补充一个令牌的时间间隔。
	// 例如 250ms 表示长期平均 4 rps。
	Interval time.Duration
}

// KeyedLimiter 为每个 key 维护一个独立的令牌桶。
//
// 桶是按需创建的，因此必须回收：只要攻击者不停更换 key，
// 内部映射就会无限增长，最终把内存吃光。
type KeyedLimiter struct {
	config     Config
	idleTTL    time.Duration
	sweepEvery time.Duration

	// now 允许测试注入假时钟，生产环境就是 time.Now。
	now func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

func NewKeyedLimiter(config Config) (*KeyedLimiter, error) {
	if config.Burst <= 0 {
		return nil, fmt.Errorf("rate limit burst must be positive, got %d", config.Burst)
	}
	if config.Interval <= 0 {
		return nil, fmt.Errorf("rate limit interval must be positive, got %s", config.Interval)
	}

	return &KeyedLimiter{
		config:     config,
		idleTTL:    defaultIdleTTL,
		sweepEvery: defaultSweepEvery,
		now:        time.Now,
		buckets:    make(map[string]*bucket),
	}, nil
}

// RetryAfter 返回被限流后建议客户端等待的时间。
//
// 最坏情况下要等一个 Interval 才能拿到下一个令牌。
func (l *KeyedLimiter) RetryAfter() time.Duration {
	return l.config.Interval
}

// Size 返回当前保留的桶数量，用于观测和测试。
func (l *KeyedLimiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Allow 判断某个 key 现在能否放行一个请求。
//
// 空 key 一律放行：调用方（中间件）已经决定"没有身份维度就跳过这一层"，
// 这里再兜一次底，避免把所有人算成同一个身份。
func (l *KeyedLimiter) Allow(key string) bool {
	if key == "" {
		return true
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweepLocked(now)

	b, ok := l.buckets[key]
	if !ok {
		// 新 key 从满桶开始，这样第一次访问不会因为"还没攒够令牌"被拒。
		b = &bucket{
			tokens:     float64(l.config.Burst),
			lastRefill: now,
		}
		l.buckets[key] = b
	}
	b.lastSeen = now

	// TODO(阶段 9.6)：令牌桶的核心算法由你实现。
	//
	// 目标：按经过的时间补充令牌，桶里不足一个令牌时拒绝这次请求。
	//
	// 可用的字段：
	//   b.tokens           当前令牌数（float64，因为补充是连续的小数）
	//   b.lastRefill       上次补充的时间
	//   l.config.Burst     桶容量
	//   l.config.Interval  补充一个令牌需要的时间
	//
	// 建议步骤：
	//   1. elapsed := now.Sub(b.lastRefill)
	//   2. 按时间补充：b.tokens += float64(elapsed) / float64(l.config.Interval)
	//   3. 不能超过桶容量，超过就 clamp 回 Burst
	//   4. b.lastRefill = now
	//   5. 令牌够 1 就扣掉并返回 true，否则返回 false
	//
	// 注意：一定要用乘除算，不要写 for 循环一个个加令牌。
	// 空闲一小时后循环要跑几百万次，等于自己给自己造了一个卡顿点。
	return true
}

// sweepLocked 回收长期空闲的桶。调用方必须持有锁。
func (l *KeyedLimiter) sweepLocked(now time.Time) {
	if l.lastSweep.IsZero() {
		l.lastSweep = now
		return
	}
	if now.Sub(l.lastSweep) < l.sweepEvery {
		return
	}

	l.lastSweep = now
	for key, b := range l.buckets {
		if now.Sub(b.lastSeen) > l.idleTTL {
			delete(l.buckets, key)
		}
	}
}
