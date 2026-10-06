package feed

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	bigAuthorCacheKeyPrefix  = "feed:big-authors:"
	DefaultBigAuthorCacheTTL = time.Minute
)

// BigAuthorCache 缓存当前用户关注作者中的大 V ID。
type BigAuthorCache interface {
	Get(ctx context.Context, userID uint64) ([]uint64, bool, error)
	Set(ctx context.Context, userID uint64, authorIDs []uint64) error
	Delete(ctx context.Context, userID uint64) error
}

type RedisBigAuthorCache struct {
	client *redis.Client
	ttl    time.Duration
}

func NewRedisBigAuthorCache(client *redis.Client, ttl time.Duration) *RedisBigAuthorCache {
	if ttl <= 0 {
		ttl = DefaultBigAuthorCacheTTL
	}
	return &RedisBigAuthorCache{
		client: client,
		ttl:    ttl,
	}
}

var _ BigAuthorCache = (*RedisBigAuthorCache)(nil)

func (c *RedisBigAuthorCache) Get(
	ctx context.Context,
	userID uint64,
) ([]uint64, bool, error) {
	key := bigAuthorCacheKey(userID)
	result, err := c.client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get big author cache: %w", err)
	}

	var authorIDs []uint64
	if err := json.Unmarshal([]byte(result), &authorIDs); err != nil {
		return nil, false, fmt.Errorf("get big author cache: %w", err)
	}
	if authorIDs == nil {
		authorIDs = []uint64{}
	}

	return authorIDs, true, nil
}

func (c *RedisBigAuthorCache) Set(
	ctx context.Context,
	userID uint64,
	authorIDs []uint64,
) error {
	key := bigAuthorCacheKey(userID)
	if authorIDs == nil {
		authorIDs = []uint64{}
	}
	value, err := json.Marshal(authorIDs)
	if err != nil {
		return fmt.Errorf("set big author cache: %w", err)
	}
	if err := c.client.Set(ctx, key, value, c.ttl).Err(); err != nil {
		return fmt.Errorf("set big author cache: %w", err)
	}
	return nil
}

func (c *RedisBigAuthorCache) Delete(ctx context.Context, userID uint64) error {
	if err := c.client.Del(ctx, bigAuthorCacheKey(userID)).Err(); err != nil {
		return fmt.Errorf("delete big author cache: %w", err)
	}
	return nil
}

func bigAuthorCacheKey(userID uint64) string {
	return bigAuthorCacheKeyPrefix + strconv.FormatUint(userID, 10)
}
