package interaction

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// LikeRepository 定义点赞模块需要的持久化能力。
//
// Repository 只负责数据库操作，不判断“视频是否存在”这类业务规则；
// 业务规则交给 LikeService。
type LikeRepository interface {
	// Create 创建一条点赞关系。
	//
	// UNIQUE(user_id, video_id) 冲突时返回 ErrAlreadyLiked；
	// 其他数据库错误需要保留调用链。
	Create(ctx context.Context, userID, videoID uint64) error

	// Delete 删除一条点赞关系。
	//
	// 删除不存在的记录也返回 nil，保证取消点赞幂等。
	// 数据库错误需要包装上下文。
	Delete(ctx context.Context, userID, videoID uint64) error

	// Exists 判断用户是否已经点赞视频。
	Exists(ctx context.Context, userID, videoID uint64) (bool, error)

	// CountByVideoID 统计一个视频收到的点赞数。
	//
	// 第一阶段不维护 videos.like_count 冗余字段。
	CountByVideoID(ctx context.Context, videoID uint64) (int64, error)

	// 批量查询：一次查出多个视频的点赞数
	CountByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error)

	// 批量查询：一次查出当前用户给哪些视频点过赞
	LikedVideoIDsByUser(ctx context.Context, userID uint64, videoIDs []uint64) (map[uint64]bool, error)
}

type GORMLikeRepository struct {
	db *gorm.DB
}

func NewGORMLikeRepository(db *gorm.DB) *GORMLikeRepository {
	return &GORMLikeRepository{
		db: db,
	}
}

func (r *GORMLikeRepository) Create(ctx context.Context, userID, videoID uint64) error {
	like := &Like{
		UserID:  userID,
		VideoID: videoID,
	}

	err := r.db.WithContext(ctx).Create(like).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrAlreadyLiked
		}
		if errors.Is(err, gorm.ErrForeignKeyViolated) {
			return fmt.Errorf("create like: video no longer exists: %w", ErrInvalidTarget)
		}
		return fmt.Errorf("create like: %w", err)
	}
	return nil
}

func (r *GORMLikeRepository) Delete(ctx context.Context, userID, videoID uint64) error {
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND video_id = ?", userID, videoID).
		Delete(&Like{}).Error
	if err != nil {
		return fmt.Errorf("delete like: %w", err)
	}
	return nil
}

func (r *GORMLikeRepository) Exists(ctx context.Context, userID, videoID uint64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&Like{}).
		Where("user_id = ? AND video_id = ?", userID, videoID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check like exists: %w", err)
	}
	return count > 0, nil
}

func (r *GORMLikeRepository) CountByVideoID(ctx context.Context, videoID uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&Like{}).
		Where("video_id = ?", videoID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count likes by video: %w", err)
	}
	return count, nil
}

func (r *GORMLikeRepository) CountByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error) {
	// 1. 防空切片：如果没有视频 ID，直接返回空 map，不访问数据库
	if len(videoIDs) == 0 {
		return make(map[uint64]int64), nil
	}

	// 2. 定义接收结果的结构体
	type CountResult struct {
		VideoID uint64
		Count   int64
	}
	var results []CountResult

	// 3. 核心 SQL：SELECT video_id, COUNT(*) as count FROM likes WHERE video_id IN (...) GROUP BY video_id
	err := r.db.WithContext(ctx).
		Model(&Like{}).
		Select("video_id, COUNT(*) as count").
		Where("video_id IN ?", videoIDs).
		Group("video_id").
		Scan(&results).Error
	if err != nil {
		return nil, fmt.Errorf("batch count likes: %w", err)
	}

	// 4. 转换格式：把切片变成 map，方便 Service 层 O(1) 查找
	countMap := make(map[uint64]int64, len(results))
	for _, r := range results {
		countMap[r.VideoID] = r.Count
	}
	return countMap, nil
}

func (r *GORMLikeRepository) LikedVideoIDsByUser(ctx context.Context, userID uint64, videoIDs []uint64) (map[uint64]bool, error) {
	// 1. 防空切片
	if len(videoIDs) == 0 {
		return make(map[uint64]bool), nil
	}

	// 2. 接收结果：只查 video_id 字段
	var likedVideoIDs []uint64
	// 核心 SQL：SELECT video_id FROM likes WHERE user_id = ? AND video_id IN (...)
	err := r.db.WithContext(ctx).
		Model(&Like{}).
		Where("user_id = ? AND video_id IN ?", userID, videoIDs).
		Pluck("video_id", &likedVideoIDs).Error
	if err != nil {
		return nil, fmt.Errorf("batch check liked status: %w", err)
	}

	// 3. 转换格式：变成 map[uint64]bool
	likedMap := make(map[uint64]bool, len(likedVideoIDs))
	for _, id := range likedVideoIDs {
		likedMap[id] = true
	}
	return likedMap, nil
}
