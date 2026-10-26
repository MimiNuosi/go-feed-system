package interaction

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go-feed-system/internal/video"
)

// VideoReader 是点赞 Service 需要的视频查询能力。
//
// 接口定义在使用方 interaction 包中，类似 C++ 的依赖倒置：
// Service 依赖抽象，不直接依赖 GORM 或 VideoRepository。
type VideoReader interface {
	GetByID(ctx context.Context, id uint64) (*video.Video, error)
}

type LikeService struct {
	likes     LikeRepository
	videos    VideoReader
	hotScores HotScoreRecorder
}

func NewLikeService(
	likes LikeRepository,
	videos VideoReader,
	hotScores HotScoreRecorder,
) *LikeService {
	return &LikeService{
		likes:     likes,
		videos:    videos,
		hotScores: hotScores,
	}
}

func (s *LikeService) Like(ctx context.Context, userID, videoID uint64) error {
	// 1. 校验参数
	if userID == 0 || videoID == 0 {
		return ErrInvalidInput
	}

	// 2. 确认视频存在
	_, err := s.videos.GetByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return ErrInvalidTarget
		}
		return fmt.Errorf("like video: check video: %w", err)
	}

	// 3. 创建点赞关系
	err = s.likes.Create(ctx, userID, videoID)
	if err != nil {
		// 处理特殊错误
		if errors.Is(err, ErrAlreadyLiked) {
			return nil // 幂等：重复点赞视为成功
		}
		if errors.Is(err, ErrInvalidTarget) {
			return ErrInvalidTarget // 处理并发场景下视频被删的外键错误
		}
		return fmt.Errorf("like video: create like: %w", err)
	}

	s.recordHotScore(ctx, videoID, 3)
	return nil
}

func (s *LikeService) Unlike(ctx context.Context, userID, videoID uint64) error {
	// 1. 校验参数
	if userID == 0 || videoID == 0 {
		return ErrInvalidInput
	}

	// 2. 直接调用 Delete（无需查视频）
	// 原因：取消点赞的目标是“消除点赞状态”，视频是否存在不影响这个目标的达成
	if deleted, err := s.likes.Delete(ctx, userID, videoID); err != nil {
		return fmt.Errorf("unlike video: delete like: %w", err)
	} else if !deleted {
		return nil
	}

	s.recordHotScore(ctx, videoID, -3)
	return nil
}

func (s *LikeService) recordHotScore(ctx context.Context, videoID uint64, delta int64) {
	if s.hotScores == nil {
		return
	}
	s.hotScores.AddScore(ctx, videoID, delta, time.Now())
}

// ListLikedVideoEdges 返回一页"我赞过的视频"，按点赞时间倒序。
func (s *LikeService) ListLikedVideoEdges(
	ctx context.Context,
	userID uint64,
	cursor uint64,
	limit int,
) ([]LikeEdge, bool, error) {
	if userID == 0 {
		return nil, false, ErrInvalidInput
	}
	limit = normalizeProfilePageSize(limit)
	edges, err := s.likes.ListLikedVideoEdges(ctx, userID, cursor, limit+1)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(edges) > limit
	if hasMore {
		edges = edges[:limit]
	}
	return edges, hasMore, nil
}

// CountLikesReceivedByAuthor 返回作者所有作品收到的点赞数。
func (s *LikeService) CountLikesReceivedByAuthor(ctx context.Context, authorID uint64) (int64, error) {
	if authorID == 0 {
		return 0, ErrInvalidInput
	}
	return s.likes.CountLikesReceivedByAuthor(ctx, authorID)
}

// LikeState 是视频详情和 Feed 展示需要的组合状态。
type LikeState struct {
	Count     int64
	IsLikedBy bool
}

// GetState 先校验视频存在，再组合“点赞总数”和“当前用户是否点赞”。
//
// 需要 404 语义的调用方用它。视频详情页不要用：详情路径上调用方
// 已经加载过视频，这里的校验会变成一次多余的数据库往返，
// 详情页应该用 GetLikeState。
func (s *LikeService) GetState(ctx context.Context, userID, videoID uint64) (*LikeState, error) {
	if videoID == 0 {
		return nil, ErrInvalidInput
	}

	// 确认视频存在，把「目标不存在」翻译成互动领域的错误
	if _, err := s.videos.GetByID(ctx, videoID); err != nil {
		if errors.Is(err, video.ErrNotFound) {
			return nil, ErrInvalidTarget
		}
		return nil, fmt.Errorf("get state: check video: %w", err)
	}

	return s.likeState(ctx, userID, videoID)
}

// likeState 读取点赞统计：先算总数，登录用户再查是否点过赞。
//
// 匿名用户（userID == 0）只查一次：他的“是否点赞”必然是 false。
func (s *LikeService) likeState(ctx context.Context, userID, videoID uint64) (*LikeState, error) {
	if videoID == 0 {
		return nil, ErrInvalidInput
	}

	count, err := s.likes.CountByVideoID(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("get state: count likes: %w", err)
	}

	state := &LikeState{Count: count, IsLikedBy: false}

	if userID == 0 {
		return state, nil
	}

	isLiked, err := s.likes.Exists(ctx, userID, videoID)
	if err != nil {
		return nil, fmt.Errorf("get state: check exists: %w", err)
	}
	state.IsLikedBy = isLiked

	return state, nil
}

// GetLikeState 是给 video 包使用的窄接口适配方法。
//
// 它刻意不校验视频是否存在：调用方（视频详情 Handler）已经加载过视频，
// 再校验一次就是白白多一轮数据库往返 —— 压测里这占了详情请求
// 4 条查询中的 1 条。
//
// 它将互动领域的错误转换成 video 包可以识别的错误，
// 避免 video 包为了处理详情响应而反向依赖 interaction 包。
func (s *LikeService) GetLikeState(ctx context.Context, viewerID, videoID uint64) (int64, bool, error) {
	state, err := s.likeState(ctx, viewerID, videoID)
	if err != nil {
		// 处理 ErrInvalidTarget
		if errors.Is(err, ErrInvalidTarget) {
			return 0, false, video.ErrNotFound
		}
		return 0, false, fmt.Errorf("get video state err: %w", err)
	}
	return state.Count, state.IsLikedBy, nil
}

// CountByVideoIDs 批量获取视频点赞数。
// 用于 Feed 流组装，避免逐条查询产生 N+1 问题。
func (s *LikeService) CountByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error) {
	// Service 层依然要做空切片校验，防御性编程
	if len(videoIDs) == 0 {
		return make(map[uint64]int64), nil
	}

	countMap, err := s.likes.CountByVideoIDs(ctx, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("like service: batch count by video ids: %w", err)
	}
	return countMap, nil
}

// LikedVideoIDsByUser 批量获取当前用户对多个视频的点赞状态。
// 用于 Feed 流组装。
func (s *LikeService) LikedVideoIDsByUser(ctx context.Context, userID uint64, videoIDs []uint64) (map[uint64]bool, error) {
	if userID == 0 {
		return nil, ErrInvalidInput
	}
	if len(videoIDs) == 0 {
		return make(map[uint64]bool), nil
	}

	likedMap, err := s.likes.LikedVideoIDsByUser(ctx, userID, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("like service: batch check liked status: %w", err)
	}
	return likedMap, nil
}
