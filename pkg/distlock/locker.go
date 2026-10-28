package distlock

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	defaultRetryInterval  = 50 * time.Millisecond
	defaultReleaseTimeout = 2 * time.Second
)

type UnlockFunc func() error

type Locker interface {
	Acquire(ctx context.Context, key string, ttl time.Duration) (UnlockFunc, error)
}

type RedisLocker struct {
	client        *redis.Client
	retryInterval time.Duration
}

func NewRedisLocker(client *redis.Client) *RedisLocker {
	return &RedisLocker{
		client:        client,
		retryInterval: defaultRetryInterval,
	}
}

func (l *RedisLocker) Acquire(
	ctx context.Context,
	key string,
	ttl time.Duration,
) (UnlockFunc, error) {
	if l == nil || l.client == nil {
		return nil, fmt.Errorf("redis locker is not configured")
	}
	if key == "" {
		return nil, fmt.Errorf("lock key must not be empty")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("lock TTL must be positive")
	}

	owner := uuid.NewString()
	for {
		acquired, err := l.client.SetNX(ctx, key, owner, ttl).Result()
		if err != nil {
			return nil, fmt.Errorf("acquire redis lock: %w", err)
		}
		if acquired {
			var (
				mu       sync.Mutex
				released bool
			)
			return func() error {
				mu.Lock()
				defer mu.Unlock()
				if released {
					return nil
				}
				released = true
				releaseCtx, cancel := context.WithTimeout(
					context.Background(),
					defaultReleaseTimeout,
				)
				defer cancel()
				result, err := releaseScript.Run(
					releaseCtx,
					l.client,
					[]string{key},
					owner,
				).Int64()
				if err != nil {
					return fmt.Errorf("release redis lock: %w", err)
				}
				if result == 0 {
					return fmt.Errorf("redis lock no longer belongs to this owner")
				}
				return nil
			}, nil
		}

		timer := time.NewTimer(l.retryInterval)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		}
	}
}

var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`)
