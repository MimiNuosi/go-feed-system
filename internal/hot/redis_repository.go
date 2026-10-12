package hot

import (
	"context"
	"fmt"
	"log/slog"
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

var addScoreScript = redis.NewScript(`
redis.call("ZINCRBY", KEYS[1], ARGV[2], ARGV[1])
redis.call("ZINCRBY", KEYS[2], ARGV[2], ARGV[1])
return 1
`)

const defaultViewDedupTTL = 5 * time.Minute

type RedisRepository struct {
	client       *redis.Client
	logger       *slog.Logger
	viewDedupTTL time.Duration
}

func NewRedisRepository(client *redis.Client) *RedisRepository {
	return &RedisRepository{
		client:       client,
		logger:       slog.Default(),
		viewDedupTTL: defaultViewDedupTTL,
	}
}

var _ RankingRepository = (*RedisRepository)(nil)
var _ ScoreWriter = (*RedisRepository)(nil)
var _ PendingViewStore = (*RedisRepository)(nil)

// PendingViews 读取最多 limit 条待回写的浏览量增量。
//
// 脏数据（field 不是数字、value 不是整数）会被跳过并记日志：
// 一条脏数据不应该把整个批次卡死。
//
// HGetAll 会把整个 Hash 一次读回来再按 limit 截断，所以截断掉的是哪几条
// 是随机的。当前量级下没有影响（处理过的条目每轮都会 ack 掉），
// 但 Hash 长到几万条时应该改用 HSCAN 分批读，避免一次性拉进内存。
func (r *RedisRepository) PendingViews(
	ctx context.Context,
	limit int,
) (map[uint64]int64, error) {
	if limit <= 0 {
		return map[uint64]int64{}, nil
	}

	raw, err := r.client.HGetAll(ctx, PendingViewsKey()).Result()
	if err != nil {
		return nil, fmt.Errorf("pending views: HGETALL: %w", err)
	}

	result := make(map[uint64]int64, len(raw))
	for field, value := range raw {
		videoID, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			r.logger.Warn("pending views: parse video ID failed", "field", field, "error", err)
			continue
		}

		delta, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			r.logger.Warn("pending views: parse delta failed", "value", value, "error", err)
			continue
		}

		result[videoID] = delta

		if len(result) >= limit {
			break
		}
	}

	return result, nil
}

// AckPendingView 在增量成功落库后，删除对应的 Hash 字段。
//
// HDEL 的 field 必须和 HINCRBY 时完全一致：普通十进制 videoID
// （见 recordViewScript 的 ARGV[1]）。格式不一致会删不掉，
// 增量被反复累加，这是这类代码最容易踩的坑。
//
// 删 0 条说明已经被别的实例处理过，按成功处理即可（幂等）。
func (r *RedisRepository) AckPendingView(ctx context.Context, videoID uint64) error {
	if videoID == 0 {
		return fmt.Errorf("ack pending view: video ID is zero: %w", ErrInvalidInput)
	}

	field := strconv.FormatUint(videoID, 10)
	if err := r.client.HDel(ctx, PendingViewsKey(), field).Err(); err != nil {
		return fmt.Errorf("ack pending view: HDEL: %w", err)
	}
	return nil
}

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

func (r *RedisRepository) AddScore(
	ctx context.Context,
	videoID uint64,
	delta int64,
	at time.Time,
) error {
	if videoID == 0 {
		return fmt.Errorf("add hot score: video ID is zero: %w", ErrInvalidInput)
	}
	if at.IsZero() {
		return fmt.Errorf("add hot score: at is zero: %w", ErrInvalidInput)
	}
	if delta == 0 {
		return nil
	}

	bucket1h, err := WindowOneHour.CurrentBucketKey(at)
	if err != nil {
		return fmt.Errorf("add hot score: build 1h bucket: %w", err)
	}
	bucket24h, err := WindowTwentyFourH.CurrentBucketKey(at)
	if err != nil {
		return fmt.Errorf("add hot score: build 24h bucket: %w", err)
	}
	_, err = addScoreScript.Run(
		ctx,
		r.client,
		[]string{bucket1h, bucket24h},
		formatVideoID(videoID),
		delta,
	).Int64()
	if err != nil {
		return fmt.Errorf("add hot score: run script: %w", err)
	}
	return nil
}

func formatVideoID(videoID uint64) string {
	return fmt.Sprintf("%020d", videoID)
}
