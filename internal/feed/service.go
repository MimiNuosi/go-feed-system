package feed

import (
	"context"
	"fmt"

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

type Service struct {
	videos Repository
	users  UserReader
	likes  LikeReader
}

func NewService(videos Repository, users UserReader, likes LikeReader) *Service {
	return &Service{
		videos: videos,
		users:  users,
		likes:  likes,
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

	// 3. 调用 Repository，多查一条用于判断 HasMore
	records, err := s.videos.ListFollowing(ctx, userID, cursor, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list following: fetch videos: %w", err)
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
