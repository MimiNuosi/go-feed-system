package feed

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"go-feed-system/internal/user"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// UserReader 是 Feed 批量补充作者信息的能力。
type UserReader interface {
	GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
}

// LikeReader 是 Feed 批量读取点赞信息的能力。
type LikeReader interface {
	CountByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error)
	LikedVideoIDsByUser(
		ctx context.Context,
		userID uint64,
		videoIDs []uint64,
	) (map[uint64]bool, error)
}

// InboxReader 是 Feed 读取 Redis 收件箱的能力。
type InboxReader interface {
	List(ctx context.Context, userID uint64, cursor uint64, limit int) ([]uint64, error)
}

type Service struct {
	videos Repository
	users  UserReader
	likes  LikeReader
	inbox  InboxReader
	logger *slog.Logger
}

func NewService(
	videos Repository,
	users UserReader,
	likes LikeReader,
	inbox InboxReader,
	logger *slog.Logger,
) *Service {
	return &Service{
		videos: videos,
		users:  users,
		likes:  likes,
		inbox:  inbox,
		logger: logger,
	}
}

// ListFollowing 返回当前用户的关注 Feed。
func (s *Service) ListFollowing(
	ctx context.Context,
	userID uint64,
	cursor *Cursor,
	pageSize int,
) (*Page, error) {
	// 1. 校验 userID
	if userID == 0 {
		return nil, ErrInvalidInput
	}

	// 2. 处理 pageSize 默认值和最大值
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}

	// 3. 分别读取 MySQL 纯拉 Feed 和 Redis Inbox，然后合并。
	//
	// TODO(阶段 6.6)：
	// 1. MySQL：调用 ListFollowing(ctx, userID, cursor, pageSize+1)。
	// 2. Redis：调用 inbox.List(ctx, userID, 0, DefaultInboxMaxLen)，
	//    读取最多 1000 个 videoID。
	// 3. 使用 videos.ListByIDs(ctx, pushVideoIDs) 批量补充 Redis 视频元数据。
	// 4. Redis 读取失败时记录 Warn，并降级为仅使用 MySQL records。
	// 5. 按 video ID 去重，优先保留 MySQL 查询出的 VideoRecord。
	// 6. 第二页及以后过滤掉不满足 (created_at, id) < (cursor) 的 Redis 记录。
	// 7. 按 created_at DESC、id DESC 排序。
	// 8. 再执行下面的 HasMore 裁剪、批量补充和游标生成。
	// 3.1 查 MySQL (现有代码)
	mysqlRecords, err := s.videos.ListFollowing(ctx, userID, cursor, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list following: fetch videos: %w", err)
	}

	// 3.2 查 Redis 收件箱
	var pushIDs []uint64
	if s.inbox != nil {
		pushIDs, err = s.inbox.List(ctx, userID, 0, DefaultInboxMaxLen)
		if err != nil {
			// 降级处理：Redis 挂了不能影响 Feed，记录 Warn，继续使用 MySQL 结果
			logger.Warn("feed: redis inbox failed, fallback to mysql only", "error", err)
			pushIDs = nil
		}
	}

	// 3.3 批量拿 Redis 视频的元数据
	var pushRecords []VideoRecord
	if len(pushIDs) > 0 {
		pushRecords, err = s.videos.ListVisibleByIDs(ctx, userID, pushIDs)
		if err != nil {
			logger.Warn("feed: list videos by push ids failed, fallback to mysql only", "error", err)
			pushRecords = nil // 降级
		}
	}

	// 3.4 合并、去重、排序（核心！）
	// 注意：Go 的 map 是无序的，所以去重可以用 map，但排序必须用 slice。
	// 另外：MySQL 查出来的已经在 cursor 之前了，不需要再次过滤。Redis 的数据需要按 cursor 过滤。
	mergedMap := make(map[uint64]VideoRecord)

	// 优先放入 MySQL 数据（因为它是事实数据源）
	for _, r := range mysqlRecords {
		mergedMap[r.ID] = r
	}

	// 放入 Redis 数据（去重：如果 map 里已有，说明是重复的，跳过或保留 MySQL 版本）
	for _, r := range pushRecords {
		// 如果指定了 cursor，Redis 数据也要过滤
		if cursor != nil {
			if r.CreatedAt.After(cursor.CreatedAt) ||
				(r.CreatedAt.Equal(cursor.CreatedAt) && r.ID >= cursor.ID) {
				continue // 跳过比游标还新的数据
			}
		}
		if _, exists := mergedMap[r.ID]; !exists {
			mergedMap[r.ID] = r
		}
	}

	// 把 map 转成 slice
	var records []VideoRecord
	for _, r := range mergedMap {
		records = append(records, r)
	}

	// 排序：按创建时间倒序，如果时间相同按 ID 倒序
	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})

	// 4. 判断 HasMore 并裁剪
	hasMore := len(records) > pageSize
	if hasMore {
		records = records[:pageSize]
	}

	// 5. 空 Feed 直接返回，避免不必要的批量查询
	if len(records) == 0 {
		return &Page{
			Items:      []Item{},
			NextCursor: "",
			HasMore:    false,
		}, nil
	}

	// 6. 收集去重后的作者 ID 和视频 ID
	authorIDSet := make(map[uint64]struct{})
	videoIDSet := make(map[uint64]struct{})
	for _, r := range records {
		authorIDSet[r.AuthorID] = struct{}{}
		videoIDSet[r.ID] = struct{}{}
	}

	authorIDs := make([]uint64, 0, len(authorIDSet))
	for id := range authorIDSet {
		authorIDs = append(authorIDs, id)
	}

	videoIDs := make([]uint64, 0, len(videoIDSet))
	for id := range videoIDSet {
		videoIDs = append(videoIDs, id)
	}

	// 7. 批量查询：作者信息、点赞总数、当前用户点赞状态
	userMap, err := s.users.GetByIDs(ctx, authorIDs)
	if err != nil {
		return nil, fmt.Errorf("list following: batch get authors: %w", err)
	}

	likeCountMap, err := s.likes.CountByVideoIDs(ctx, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("list following: batch count likes: %w", err)
	}

	likedMap, err := s.likes.LikedVideoIDsByUser(ctx, userID, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("list following: batch get liked status: %w", err)
	}

	// 8. 按记录顺序组装 Item（绝对不能遍历 map，否则顺序会乱）
	items := make([]Item, 0, len(records))
	for _, r := range records {
		// 作者兜底
		username := "已注销用户"
		if u, ok := userMap[r.AuthorID]; ok && u != nil {
			username = u.Username
		}

		// 点赞数兜底
		likeCount := int64(0)
		if count, ok := likeCountMap[r.ID]; ok {
			likeCount = count
		}

		// 点赞状态兜底
		isLiked := false
		if liked, ok := likedMap[r.ID]; ok {
			isLiked = liked
		}

		items = append(items, Item{
			ID: r.ID,
			Author: AuthorInfo{
				ID:       r.AuthorID,
				Username: username,
			},
			Title:       r.Title,
			Description: r.Description,
			ContentType: r.ContentType,
			CreatedAt:   r.CreatedAt,
			LikeCount:   likeCount,
			IsLikedBy:   isLiked,
		})
	}

	// 9. 生成 NextCursor
	nextCursor := ""
	if hasMore {
		last := records[len(records)-1]
		nextCursor, err = encodeCursor(Cursor{
			CreatedAt: last.CreatedAt,
			ID:        last.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("list following: encode cursor: %w", err)
		}
	}

	return &Page{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}
