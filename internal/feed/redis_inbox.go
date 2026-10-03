package feed

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// Redis Key 前缀，最终格式为 feed:inbox:<userID>
	inboxKeyPrefix = "feed:inbox:"
)

// RedisInbox 使用 Redis ZSet 实现 Feed 收件箱。
type RedisInbox struct {
	client *redis.Client
	maxLen int
}

// NewRedisInbox 创建 RedisInbox 实例。
func NewRedisInbox(client *redis.Client, maxLen int) *RedisInbox {
	if maxLen <= 0 {
		maxLen = int(DefaultInboxMaxLen)
	}
	return &RedisInbox{
		client: client,
		maxLen: maxLen,
	}
}

var _ Inbox = (*RedisInbox)(nil)

// Add 批量将视频写入多个用户的收件箱。
//
// 参数：
// - userIDs: 接收视频的粉丝 ID 列表
// - videoID: 视频 ID
// - publishedAt: 视频发布时间，用于生成 ZSet 的 Score
func (r *RedisInbox) Add(ctx context.Context, userIDs []uint64, videoID uint64, publishedAt time.Time) error {
	// 1. 防空切片：如果没有粉丝，直接返回，不访问 Redis
	if len(userIDs) == 0 {
		return nil
	}

	// 2. 准备 Score 和 Member
	score := float64(publishedAt.UnixMilli())
	member := formatVideoID(videoID)

	// 3. 创建 Pipeline
	// C++ 类比：相当于创建了一个命令缓冲区 std::vector<Command>
	pipe := r.client.Pipeline()

	// 4. 遍历粉丝，把 ZADD 命令写入 Pipeline
	// 注意：这里的 pipe.ZAdd 只是把命令放进队列，并没有真正发给 Redis
	for _, userID := range userIDs {
		key := inboxKeyPrefix + strconv.FormatUint(userID, 10)
		pipe.ZAdd(ctx, key, redis.Z{
			Score:  score,
			Member: member,
		})
	}

	// 5. 执行 Pipeline：一次性把所有命令打包发给 Redis
	// 相当于把缓冲区里的数据一次性 send 出去，并等待所有响应
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("add to inbox pipeline: %w", err)
	}

	return nil
}

// List 读取用户的 Feed 收件箱。
//
// 参数：
// - userID: 用户 ID
// - cursor: 上一页最后一条视频的 ID，0 表示第一页
// - limit: 期望读取的条数
func (r *RedisInbox) List(ctx context.Context, userID uint64, cursor uint64, limit int) ([]uint64, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("limit must be positive")
	}
	key := inboxKeyPrefix + strconv.FormatUint(userID, 10)

	if cursor == 0 {
		results, err := r.client.ZRevRange(ctx, key, 0, int64(limit-1)).Result()
		if err != nil {
			return nil, fmt.Errorf("zrange args: %w", err)
		}

		// 把 []string 转换成 []uint64
		videoIDs := make([]uint64, 0, len(results))
		for _, member := range results {
			id, err := parseVideoID(member)
			if err != nil {
				return nil, fmt.Errorf("parse video id %q: %w", member, err)
			}
			videoIDs = append(videoIDs, id)
		}
		return videoIDs, nil
	} else {
		// 1. 获取游标的排位
		cursorRank, err := r.client.ZRevRank(ctx, key, formatVideoID(cursor)).Result()

		if err != nil {
			if err == redis.Nil {
				// 游标不存在，说明数据已过期或不存在，返回空列表
				return []uint64{}, nil
			}
			return nil, fmt.Errorf("find cursor rank: %w", err)
		}

		// 2. 使用 ZRangeArgs 替代已弃用的 ZRevRange
		// 从 cursorRank + 1 开始取 limit 条，Rev: true 表示从大到小（最新在前）
		results, err := r.client.ZRevRange(ctx, key, cursorRank+1, cursorRank+int64(limit)).Result()
		if err != nil {
			return nil, fmt.Errorf("zrange args for feed: %w", err)
		}

		// 3. 将 []string 转换成 []uint64
		videoIDs := make([]uint64, 0, len(results))
		for _, member := range results {
			id, err := parseVideoID(member)
			if err != nil {
				return nil, fmt.Errorf("parse video id %q: %w", member, err)
			}
			videoIDs = append(videoIDs, id)
		}
		return videoIDs, nil
	}
}

// Trim 裁剪收件箱，保留最新的 maxLen 条记录。
func (r *RedisInbox) Trim(ctx context.Context, userID uint64) error {
	key := inboxKeyPrefix + strconv.FormatUint(userID, 10)

	// 保留分数最高的 maxLen 条，删除排名在 [0, -maxLen-1] 的旧数据
	err := r.client.ZRemRangeByRank(ctx, key, 0, int64(-r.maxLen-1)).Err()
	if err != nil {
		return fmt.Errorf("trim inbox: %w", err)
	}
	return nil
}

// ================= 辅助函数 =================

// formatVideoID 把 videoID 转为固定长度的字符串。
// 例如：1001 -> "00000000000000001001"
// 目的是为了在同 score 时，保持字典序与数值大小一致。
func formatVideoID(videoID uint64) string {
	return fmt.Sprintf("%020d", videoID)
}

// parseVideoID 把 Redis 返回的 member 字符串转回 uint64。
func parseVideoID(member string) (uint64, error) {
	return strconv.ParseUint(member, 10, 64)
}
