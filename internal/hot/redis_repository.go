package hot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var recordViewScript = redis.NewScript(`
local inserted = redis.call("SET", KEYS[1], "1", "NX", "PX", ARGV[3])
if not inserted then
    return 0
end

redis.call("HINCRBY", KEYS[2], ARGV[1], 1)
redis.call("ZINCRBY", KEYS[3], 1, ARGV[2])
redis.call("ZINCRBY", KEYS[4], 1, ARGV[2])

return 1
`)

const defaultViewDedupTTL = 5 * time.Minute

type RedisRepository struct {
	client       *redis.Client
	viewDedupTTL time.Duration
}

func NewRedisRepository(client *redis.Client) *RedisRepository {
	return &RedisRepository{
		client:       client,
		viewDedupTTL: defaultViewDedupTTL,
	}
}

var _ RankingRepository = (*RedisRepository)(nil)

func (r *RedisRepository) RecordView(
	ctx context.Context,
	videoID uint64,
	viewerKey string,
	occurredAt time.Time,
) (bool, error) {
	if videoID == 0 {
		return false, fmt.Errorf("record view: video ID is zero: %w", ErrInvalidInput)
	}
	if strings.TrimSpace(viewerKey) == "" {
		return false, fmt.Errorf("record view: viewer key is empty: %w", ErrInvalidInput)
	}
	if occurredAt.IsZero() {
		return false, fmt.Errorf("record view: occurred at is zero: %w", ErrInvalidInput)
	}

	dedupKey := ViewDedupKey(videoID, viewerKey)
	pendingKey := PendingViewsKey()

	bucket1h, err := WindowOneHour.CurrentBucketKey(occurredAt)
	if err != nil {
		return false, fmt.Errorf("record view: build 1h bucket: %w", err)
	}

	bucket24h, err := WindowTwentyFourH.CurrentBucketKey(occurredAt)
	if err != nil {
		return false, fmt.Errorf("record view: build 24h bucket: %w", err)
	}

	videoIDValue := strconv.FormatUint(videoID, 10) // 普通
	paddedVideoID := formatVideoID(videoID)         // 20 位
	ttlMilliseconds := strconv.FormatInt(r.viewDedupTTL.Milliseconds(), 10)

	result, err := recordViewScript.Run(
		ctx,
		r.client,
		[]string{dedupKey, pendingKey, bucket1h, bucket24h},
		videoIDValue,
		paddedVideoID,
		ttlMilliseconds,
	).Int64()
	if err != nil {
		return false, fmt.Errorf("record view: run script: %w", err)
	}

	return result == 1, nil
}

func (r *RedisRepository) TopVideoIDs(
	ctx context.Context,
	window Window,
	at time.Time,
	cursor *Cursor,
	limit int,
) ([]RankedVideo, error) {
	// 第一版只支持首页。后续增加 cursor 时，需要基于 (score, videoID)
	// 继续读取，并过滤同分位置之前的记录。
	if cursor != nil {
		return nil, fmt.Errorf("top video ids: cursor not supported yet: %w", ErrInvalidInput)
	}
	if window == "" {
		return nil, fmt.Errorf("top video ids: window is empty: %w", ErrInvalidInput)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("top video ids: limit is not positive: %w", ErrInvalidInput)
	}
	if at.IsZero() {
		return nil, fmt.Errorf("top video ids: at is zero: %w", ErrInvalidInput)
	}

	bucketKeys, err := window.BucketKeys(at)
	if err != nil {
		return nil, fmt.Errorf("top video ids: build bucket keys: %w", err)
	}

	destKey := fmt.Sprintf("hot:video:%s:union:%d", window, time.Now().UnixNano())

	err = r.client.ZUnionStore(ctx, destKey, &redis.ZStore{
		Keys:    bucketKeys,
		Weights: nil,
	}).Err()
	if err != nil {
		return nil, fmt.Errorf("top video ids: ZUNIONSTORE: %w", err)
	}
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = r.client.Del(cleanCtx, destKey).Err()
	}()

	err = r.client.Expire(ctx, destKey, 10*time.Second).Err()
	if err != nil {
		return nil, fmt.Errorf("top video ids: set TTL for dest key: %w", err)
	}

	values, err := r.client.ZRevRangeWithScores(ctx, destKey, 0, int64(limit-1)).Result()
	if err != nil {
		return nil, fmt.Errorf("top video ids: ZREVRANGE: %w", err)
	}

	rankedVideos := make([]RankedVideo, 0, len(values))
	for _, z := range values {
		member, ok := z.Member.(string)
		if !ok {
			return nil, fmt.Errorf("top video ids: unexpected member type: %T", z.Member)
		}
		videoID, err := strconv.ParseUint(member, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("top video ids: parse video ID: %w", err)
		}
		rankedVideos = append(rankedVideos, RankedVideo{
			VideoID: videoID,
			Score:   z.Score,
		})
	}

	return rankedVideos, nil
}

func formatVideoID(videoID uint64) string {
	return fmt.Sprintf("%020d", videoID)
}
