package ratelimit

import (
	"sync"
	"testing"
	"time"
)

// fakeClock 让令牌补充的测试不依赖真实时间。
type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestLimiter(t *testing.T, config Config) (*KeyedLimiter, *fakeClock) {
	t.Helper()

	clock := &fakeClock{now: time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)}
	limiter, err := NewKeyedLimiter(config)
	if err != nil {
		t.Fatalf("new keyed limiter: %v", err)
	}
	limiter.now = clock.Now
	return limiter, clock
}

type step struct {
	advance time.Duration
	key     string
	want    bool
}

func TestNewKeyedLimiter_RejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name   string
		config Config
	}{
		{"burst 为 0", Config{Burst: 0, Interval: time.Second}},
		{"burst 为负", Config{Burst: -1, Interval: time.Second}},
		{"interval 为 0", Config{Burst: 10, Interval: 0}},
		{"interval 为负", Config{Burst: 10, Interval: -time.Second}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewKeyedLimiter(tt.config); err == nil {
				t.Fatal("expected an error for invalid config")
			}
		})
	}
}

func TestKeyedLimiter_Allow(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		steps  []step
	}{
		{
			name:   "桶容量内放行，超出就拒绝",
			config: Config{Burst: 3, Interval: time.Second},
			steps: []step{
				{key: "a", want: true},
				{key: "a", want: true},
				{key: "a", want: true},
				{key: "a", want: false},
			},
		},
		{
			name:   "按时间补充令牌",
			config: Config{Burst: 1, Interval: time.Second},
			steps: []step{
				{key: "a", want: true},
				{key: "a", want: false},
				{advance: 500 * time.Millisecond, key: "a", want: false},
				{advance: 500 * time.Millisecond, key: "a", want: true},
				{key: "a", want: false},
			},
		},
		{
			name:   "长时间空闲不会爆桶",
			config: Config{Burst: 2, Interval: time.Second},
			steps: []step{
				{key: "a", want: true},
				{key: "a", want: true},
				{key: "a", want: false},
				{advance: time.Hour, key: "a", want: true},
				{key: "a", want: true},
				{key: "a", want: false},
			},
		},
		{
			name:   "不同 key 互不影响",
			config: Config{Burst: 1, Interval: time.Second},
			steps: []step{
				{key: "a", want: true},
				{key: "a", want: false},
				{key: "b", want: true},
			},
		},
		{
			name:   "空 key 一律放行",
			config: Config{Burst: 1, Interval: time.Second},
			steps: []step{
				{key: "", want: true},
				{key: "", want: true},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter, clock := newTestLimiter(t, tt.config)

			for i, s := range tt.steps {
				clock.Advance(s.advance)
				if got := limiter.Allow(s.key); got != s.want {
					t.Fatalf(
						"step %d (key=%q, advance=%s): Allow = %v, want %v",
						i, s.key, s.advance, got, s.want,
					)
				}
			}
		})
	}
}

// TestKeyedLimiter_ReclaimsIdleBuckets 验证"换 key 刷内存"这条路被堵住了。
func TestKeyedLimiter_ReclaimsIdleBuckets(t *testing.T) {
	limiter, clock := newTestLimiter(t, Config{Burst: 10, Interval: time.Second})

	for _, key := range []string{"a", "b", "c"} {
		if !limiter.Allow(key) {
			t.Fatalf("expected key %q to be allowed", key)
		}
	}
	if got := limiter.Size(); got != 3 {
		t.Fatalf("expected 3 buckets, got %d", got)
	}

	// 还没超过 idleTTL：只补一次清扫机会，老桶应该留下。
	clock.Advance(5 * time.Minute)
	if !limiter.Allow("d") {
		t.Fatal("expected key d to be allowed")
	}
	if got := limiter.Size(); got != 4 {
		t.Fatalf("expected 4 buckets before idle TTL, got %d", got)
	}

	// 超过 idleTTL 后再来一次请求会触发清扫，老桶被回收。
	clock.Advance(11 * time.Minute)
	if !limiter.Allow("e") {
		t.Fatal("expected key e to be allowed")
	}
	if got := limiter.Size(); got != 1 {
		t.Fatalf("expected only the fresh bucket to survive, got %d buckets", got)
	}
}

func TestKeyedLimiter_AllowIsConcurrencySafe(t *testing.T) {
	const (
		burst      = 100
		goroutines = 50
		callsEach  = 10
	)

	limiter, _ := newTestLimiter(t, Config{Burst: burst, Interval: time.Second})

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		allowed int
	)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < callsEach; j++ {
				if limiter.Allow("same-key") {
					mu.Lock()
					allowed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	// 时钟是冻结的，不会补充任何令牌，放行数必须正好等于桶容量。
	if allowed != burst {
		t.Fatalf("expected exactly %d allowed, got %d", burst, allowed)
	}
}
