package interaction

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type FollowRepository interface {
	Create(ctx context.Context, followerID, followeeID uint64) error
	Delete(ctx context.Context, followerID, followeeID uint64) error
	Exists(ctx context.Context, followerID, followeeID uint64) (bool, error)
	CountFollowers(ctx context.Context, userID uint64) (int64, error)
	CountFollowing(ctx context.Context, userID uint64) (int64, error)
}

type GORMFollowRepository struct {
	db *gorm.DB
}

func NewGORMFollowRepository(db *gorm.DB) *GORMFollowRepository {
	return &GORMFollowRepository{
		db: db,
	}
}

func (r *GORMFollowRepository) Create(ctx context.Context, followerID, followeeID uint64) error {
	follow := &Follow{
		FollowerID: followerID,
		FolloweeID: followeeID,
	}

	if err := r.db.WithContext(ctx).Create(follow).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrAlreadyFollowing
		}
		return fmt.Errorf("create follow: %w", err)
	}
	return nil
}

func (r *GORMFollowRepository) Delete(ctx context.Context, followerID, followeeID uint64) error {
	// 删除不存在的记录也应返回 nil，保证取消关注幂等。
	if err := r.db.WithContext(ctx).
		Where("follower_id = ? AND followee_id = ?", followerID, followeeID).
		Delete(&Follow{}).Error; err != nil {
		return fmt.Errorf("delete follow: %w", err)
	}
	return nil
}

func (r *GORMFollowRepository) Exists(ctx context.Context, followerID, followeeID uint64) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&Follow{}).
		Where("follower_id = ? AND followee_id = ?", followerID, followeeID).
		Count(&count).Error
	if err != nil {
		return false, fmt.Errorf("check follow exists: %w", err)
	}
	return count > 0, nil
}

func (r *GORMFollowRepository) CountFollowers(ctx context.Context, userID uint64) (int64, error) {
	var count int64
	// 粉丝数：谁关注了我？ => followee_id = userID
	err := r.db.WithContext(ctx).
		Model(&Follow{}).
		Where("followee_id = ?", userID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count followers: %w", err)
	}
	return count, nil
}

func (r *GORMFollowRepository) CountFollowing(ctx context.Context, userID uint64) (int64, error) {
	var count int64
	// 关注数：我关注了谁？ => follower_id = userID
	err := r.db.WithContext(ctx).
		Model(&Follow{}).
		Where("follower_id = ?", userID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count following: %w", err)
	}
	return count, nil
}
