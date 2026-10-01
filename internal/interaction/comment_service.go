package interaction

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
)

const (
	DefaultCommentPageSize = 20
	MaxCommentPageSize     = 100
	MaxCommentLength       = 1000
)

// CommentUserReader 是评论列表需要的批量用户查询能力。
//
// 单独定义接口，避免 FollowService 只依赖 GetByID 时被迫实现批量方法。
type CommentUserReader interface {
	GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
}

type CommentService struct {
	comments CommentRepository
	videos   VideoReader
	users    CommentUserReader
}

func NewCommentService(
	comments CommentRepository,
	videos VideoReader,
	users CommentUserReader,
) *CommentService {
	return &CommentService{
		comments: comments,
		videos:   videos,
		users:    users,
	}
}

// Create 发布评论。
func (s *CommentService) Create(
	ctx context.Context,
	userID, videoID uint64,
	content string,
) (*Comment, error) {
	// 1. 校验参数
	if userID == 0 || videoID == 0 {
		return nil, ErrInvalidInput
	}

	// 2. 内容校验（去掉首尾空格，防止空评论，限制长度）
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return nil, ErrInvalidInput
	}
	if utf8.RuneCountInString(trimmedContent) > MaxCommentLength {
		return nil, ErrInvalidInput
	}

	// 3. 校验视频是否存在
	_, err := s.videos.GetByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return nil, ErrInvalidTarget
		}
		return nil, fmt.Errorf("create comment: check video: %w", err)
	}

	// 4. 构造 Comment 并写入数据库
	comment := &Comment{
		VideoID: videoID,
		UserID:  userID,
		Content: trimmedContent,
	}
	if err := s.comments.Create(ctx, comment); err != nil {
		return nil, fmt.Errorf("create comment: %w", err)
	}

	// 5. 返回创建后的评论
	return comment, nil
}

// List 按游标分页查询评论。
func (s *CommentService) List(
	ctx context.Context,
	videoID, cursor uint64,
	pageSize int,
) (*CommentPage, error) {
	// 1. 校验 videoID 和分页参数
	if videoID == 0 {
		return nil, ErrInvalidInput
	}
	if pageSize <= 0 {
		pageSize = DefaultCommentPageSize
	}
	if pageSize > MaxCommentPageSize {
		pageSize = MaxCommentPageSize
	}

	// 2. 确认视频存在
	_, err := s.videos.GetByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return nil, ErrInvalidTarget
		}
		return nil, fmt.Errorf("list comments: check video: %w", err)
	}

	// 3. 调用 Repository 多查一条，用于计算 HasMore
	comments, err := s.comments.ListByVideoID(ctx, videoID, cursor, pageSize+1)
	if err != nil {
		return nil, fmt.Errorf("list comments: fetch: %w", err)
	}

	// 4. 判断 HasMore 并截断多余数据
	hasMore := len(comments) > pageSize
	if hasMore {
		comments = comments[:pageSize]
	}

	// 5. 收集作者 ID（使用 map 去重）
	userIDSet := make(map[uint64]struct{}, len(comments))
	for _, c := range comments {
		userIDSet[c.UserID] = struct{}{}
	}

	// 把 map 转成切片传给批量查询接口
	userIDs := make([]uint64, 0, len(userIDSet))
	for id := range userIDSet {
		userIDs = append(userIDs, id)
	}

	// 6. 批量查询用户信息（避免 N+1）
	userMap := make(map[uint64]*user.User)
	if len(userIDs) > 0 {
		userMap, err = s.users.GetByIDs(ctx, userIDs)
		if err != nil {
			return nil, fmt.Errorf("list comments: batch fetch users: %w", err)
		}
	}

	// 7. 组装 CommentItem 列表
	items := make([]CommentItem, 0, len(comments))
	for _, c := range comments {
		username := "已注销用户"
		if u, ok := userMap[c.UserID]; ok && u != nil {
			username = u.Username
		}

		items = append(items, CommentItem{
			ID:        c.ID,
			VideoID:   c.VideoID,
			UserID:    c.UserID,
			Username:  username,
			Content:   c.Content,
			CreatedAt: c.CreatedAt,
		})
	}

	// 8. 计算 NextCursor
	nextCursor := ""
	if hasMore && len(items) > 0 {
		nextCursor = strconv.FormatUint(items[len(items)-1].ID, 10)
	}

	return &CommentPage{
		Items:      items,
		HasMore:    hasMore,
		NextCursor: nextCursor,
	}, nil
}

// Delete 删除自己的评论。
func (s *CommentService) Delete(ctx context.Context, userID, commentID uint64) error {
	// 1. 校验参数
	if userID == 0 || commentID == 0 {
		return ErrInvalidInput
	}

	// 2. 查询评论是否存在
	comment, err := s.comments.FindByID(ctx, commentID)
	if err != nil {
		if errors.Is(err, ErrInvalidTarget) {
			// 已经不存在，视为已删除，直接返回成功（幂等）
			return nil
		}
		return fmt.Errorf("delete comment: find: %w", err)
	}

	// 3. 权限校验：只有作者能删
	if comment.UserID != userID {
		return ErrForbidden
	}

	// 4. 执行软删除
	if err := s.comments.Delete(ctx, commentID, userID); err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}

	return nil
}
