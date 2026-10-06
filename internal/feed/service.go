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
	videos                  Repository
	users                   UserReader
	likes                   LikeReader
	inbox                   InboxReader
	bigAuthorCache          BigAuthorCache
	fanoutFollowerThreshold int
	logger                  *slog.Logger
}

func NewService(
	videos Repository,
	users UserReader,
	likes LikeReader,
	inbox InboxReader,
	bigAuthorCache BigAuthorCache,
	fanoutFollowerThreshold int,
	logger *slog.Logger,
) *Service {
	if fanoutFollowerThreshold <= 0 {
		fanoutFollowerThreshold = DefaultFanoutFollowerThreshold
	}
	return &Service{
		videos:                  videos,
		users:                   users,
		likes:                   likes,
		inbox:                   inbox,
		bigAuthorCache:          bigAuthorCache,
		fanoutFollowerThreshold: fanoutFollowerThreshold,
		logger:                  logger,
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

	// 3. 按读侧分流策略加载候选记录。
	records, err := s.loadCandidateRecords(ctx, userID, cursor, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list following: load candidates: %w", err)
	}

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

// loadCandidateRecords 按读侧分流策略加载候选视频记录。
//
// 返回的记录：
//   - 已应用 cursor 过滤（第二页及以后不会返回比 cursor 新的数据）
//   - 已去重
//   - 已按 (created_at DESC, id DESC) 排序
//   - 尚未裁剪到 pageSize，也未补作者信息 / 点赞状态
//
// limit 语义是 pageSize+1，用于上层判断 hasMore。
func (s *Service) loadCandidateRecords(
	ctx context.Context,
	userID uint64,
	cursor *Cursor,
	limit int,
) ([]VideoRecord, error) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}

	// 分支 A：没有 Redis 收件箱 -> 退化到纯 MySQL 拉模式
	if s.inbox == nil {
		return s.videos.ListFollowing(ctx, userID, cursor, limit)
	}

	// 分支 B：Redis 正常 -> 读侧分流。
	//
	// 顺序必须是：先查大 V（MySQL），再查 Redis。
	// 如果先查 Redis 再查大 V 失败，已经拿到的 Redis 数据就白费了；
	// 而且 MySQL 是事实源，Redis 是加速层，先拿事实源更符合直觉。
	//
	// 步骤 1：查大 V 作者 ID。
	bigAuthorIDs, err := s.getBigAuthorIDs(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list following by big author ids: %w", err)
	}

	// 步骤 2：查大 V 视频。
	mysqlRecords, err := s.videos.ListFollowingByAuthorIDs(ctx, bigAuthorIDs, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list following by big author ids: %w", err)
	}

	// 步骤 3：查 Redis 收件箱。失败时必须回退到全量 MySQL。
	pushIDs, err := s.inbox.List(ctx, userID, 0, DefaultInboxMaxLen)
	if err != nil {
		logger.Warn("feed: redis inbox failed, fallback to mysql only", "error", err)
		return s.videos.ListFollowing(ctx, userID, cursor, limit)
	}

	// 步骤 4：批量拿 Redis 视频的元数据。
	var pushRecords []VideoRecord
	if len(pushIDs) > 0 {
		pushRecords, err = s.videos.ListVisibleByIDs(ctx, userID, pushIDs)
		if err != nil {
			logger.Warn("feed: list videos by push ids failed, fallback to mysql only", "error", err)
			return s.videos.ListFollowing(ctx, userID, cursor, limit)
		}
	}

	// 步骤 5：合并、去重、排序。
	records := mergeVideoRecords(mysqlRecords, pushRecords, cursor)

	// 步骤 6：Redis 数据不足时，从 MySQL 补充普通作者历史。
	return s.backfillFromMySQL(ctx, userID, cursor, limit, records)
}

// backfillFromMySQL 在 Redis 数据不足以填满当前页时补充 MySQL 历史。
//
// limit 是 pageSize+1。Redis 故障的直接降级路径不会调用这个方法，
// 因为 loadCandidateRecords 已经在 Redis 错误分支直接返回了全量 MySQL 结果。
func (s *Service) backfillFromMySQL(
	ctx context.Context,
	userID uint64,
	cursor *Cursor,
	limit int,
	records []VideoRecord,
) ([]VideoRecord, error) {
	if len(records) >= limit {
		return records, nil
	}

	mysqlRecords, err := s.videos.ListFollowing(ctx, userID, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("feed: mysql backfill: %w", err)
	}

	return mergeVideoRecords(mysqlRecords, records, cursor), nil
}

func (s *Service) getBigAuthorIDs(ctx context.Context, userID uint64) ([]uint64, error) {
	logger := s.logger
	if logger == nil {
		logger = slog.Default()
	}

	if s.bigAuthorCache == nil {
		return s.videos.ListBigAuthorIDs(ctx, userID, s.fanoutFollowerThreshold)
	}

	authorIDs, hit, err := s.bigAuthorCache.Get(ctx, userID)
	if err != nil {
		logger.Warn("feed: get big author cache failed, fallback to mysql", "error", err)
		return s.videos.ListBigAuthorIDs(ctx, userID, s.fanoutFollowerThreshold)
	}
	if hit {
		return authorIDs, nil
	}

	authorIDs, err = s.videos.ListBigAuthorIDs(ctx, userID, s.fanoutFollowerThreshold)
	if err != nil {
		return nil, fmt.Errorf("list big author ids: %w", err)
	}

	if err := s.bigAuthorCache.Set(ctx, userID, authorIDs); err != nil {
		logger.Warn("feed: set big author cache failed", "error", err)
	}

	return authorIDs, nil
}

// mergeVideoRecords 把 MySQL 记录和 Redis 记录合并、去重、按复合游标排序。
//
// 这是一个纯函数：不碰 ctx、不碰 DB、不碰 Redis，容易用 Table-Driven Tests 覆盖。
//
// 优先级：MySQL 记录优先（它是事实源），Redis 记录遇到重复 ID 就跳过。
// 游标过滤：两条来源都统一过滤一次，虽然 MySQL 查询已应用过 cursor，
//
//	但统一过滤可以保证函数本身语义完整，不依赖调用方。
func mergeVideoRecords(
	mysqlRecords []VideoRecord,
	pushRecords []VideoRecord,
	cursor *Cursor,
) []VideoRecord {
	mergedMap := make(map[uint64]VideoRecord)
	for _, r := range mysqlRecords {
		if !isBeforeCursor(r, cursor) {
			continue
		}
		mergedMap[r.ID] = r
	}

	for _, r := range pushRecords {
		if cursor != nil && !isBeforeCursor(r, cursor) {
			continue // 跳过不满足游标的记录
		}
		if _, exists := mergedMap[r.ID]; !exists {
			mergedMap[r.ID] = r
		}
	}

	var records []VideoRecord
	for _, r := range mergedMap {
		records = append(records, r)
	}

	sort.Slice(records, func(i, j int) bool {
		if records[i].CreatedAt.Equal(records[j].CreatedAt) {
			return records[i].ID > records[j].ID
		}
		return records[i].CreatedAt.After(records[j].CreatedAt)
	})

	return records
}

func isBeforeCursor(record VideoRecord, cursor *Cursor) bool {
	if cursor == nil {
		return true // 没有游标，所有记录都算在前面
	}
	if record.CreatedAt.Before(cursor.CreatedAt) {
		return true
	}
	if record.CreatedAt.Equal(cursor.CreatedAt) && record.ID < cursor.ID {
		return true
	}
	return false
}
