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
