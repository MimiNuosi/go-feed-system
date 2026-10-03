package feed

import (
	"context"
	"fmt"
	"time"
)

const (
	DefaultInboxMaxLen     = 1000
	DefaultFanoutBatchSize = 500
)

// VideoPublishedEvent 是视频发布后用于 Feed 写扩散的事件。
type VideoPublishedEvent struct {
	EventID     string
	VideoID     uint64
	AuthorID    uint64
	PublishedAt time.Time
}

// FollowerReader 提供写扩散需要的粉丝 ID。
type FollowerReader interface {
	ListFollowerIDs(
		ctx context.Context,
		authorID uint64,
		afterID uint64,
		limit int,
	) ([]uint64, error)
}

// FanoutService 负责把视频发布事件写入粉丝收件箱。
type FanoutService struct {
	followers FollowerReader
	inbox     Inbox
}

func NewFanoutService(
	followers FollowerReader,
	inbox Inbox,
) *FanoutService {
	return &FanoutService{
		followers: followers,
		inbox:     inbox,
	}
}

// Fanout 把一个视频发布事件写入作者的所有粉丝收件箱。
func (s *FanoutService) Fanout(
	ctx context.Context,
	event VideoPublishedEvent,
) error {
	// 1. 校验事件参数，防止脏数据进入扩散流程
	if event.EventID == "" {
		return fmt.Errorf("fanout: empty event id: %w", ErrInvalidInput)
	}
	if event.VideoID == 0 || event.AuthorID == 0 {
		return fmt.Errorf("fanout: invalid video id or author id: %w", ErrInvalidInput)
	}
	if event.PublishedAt.IsZero() {
		return fmt.Errorf("fanout: zero published at: %w", ErrInvalidInput)
	}

	// 2. 分页读取粉丝，避免一次性把所有粉丝加载到内存
	afterID := uint64(0)
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("fanout: context canceled: %w", err)
		}

		followers, err := s.followers.ListFollowerIDs(
			ctx,
			event.AuthorID,
			afterID,
			DefaultFanoutBatchSize,
		)
		if err != nil {
			// 粉丝查询失败：包装错误返回，触发 MQ 重试
			return fmt.Errorf("fanout: list followers: %w", err)
		}

		// 如果这一批为空，说明粉丝已经遍历完
		if len(followers) == 0 {
			break
		}

		// 3. 批量写入这批粉丝的收件箱（使用 Redis Pipeline 一次性 ZADD）
		if err := s.inbox.Add(ctx, followers, event.VideoID, event.PublishedAt); err != nil {
			// 写入失败：包装错误返回，触发 MQ 重试
			return fmt.Errorf("fanout: add to inbox: %w", err)
		}

		// 4. 每个粉丝执行 Trim，防止收件箱无限增长
		// 注意：这里逐个调用 Trim 会发多次网络请求，未来可以让 Inbox 提供批量 Trim
		for _, userID := range followers {
			if err := s.inbox.Trim(ctx, userID); err != nil {
				return fmt.Errorf("fanout: trim inbox for user %d: %w", userID, err)
			}
		}

		// 5. 更新游标，准备拉取下一页粉丝
		nextAfterID := followers[len(followers)-1]
		if nextAfterID <= afterID {
			return fmt.Errorf(
				"fanout: follower cursor did not advance: previous=%d next=%d: %w",
				afterID,
				nextAfterID,
				ErrInvalidInput,
			)
		}
		afterID = nextAfterID

		// 6. 如果这一批不足 batchSize，说明已经是最后一页
		if len(followers) < DefaultFanoutBatchSize {
			break
		}
	}

	return nil
}
