package feed

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// Repository 定义 Feed 读模型需要的查询能力。
//
// Feed 是跨领域读模型，可以在 Repository 中 JOIN follows 和 videos；
// 但作者信息和点赞状态仍由 Service 批量补充。
type Repository interface {
	// ListFollowing 查询当前用户关注作者发布的视频。
	//
	// 使用 (created_at, id) 复合排序，limit 由 Service 传入 pageSize+1。
	ListFollowing(ctx context.Context, followerID uint64, cursor *Cursor, limit int) ([]VideoRecord, error)
	ListVisibleByIDs(ctx context.Context, followerID uint64, videoIDs []uint64) ([]VideoRecord, error)
}

type GORMRepository struct {
	db *gorm.DB
}

func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{
		db: db,
	}
}

func (r *GORMRepository) ListFollowing(
	ctx context.Context,
	followerID uint64,
	cursor *Cursor,
	limit int,
) ([]VideoRecord, error) {
	// 1. 初始化查询：选择视频表，并 JOIN 关注表
	query := r.db.WithContext(ctx).
		Table("videos AS v").
		Select("v.id, v.author_id, v.title, v.description, v.content_type, v.created_at").
		Joins("JOIN follows AS f ON f.followee_id = v.author_id").
		Where("f.follower_id = ?", followerID)

	// 2. 如果游标不为空，增加复合游标条件（核心）
	// SQL 语义：(v.created_at, v.id) < (?, ?)
	if cursor != nil {
		query = query.Where("(v.created_at, v.id) < (?, ?)", cursor.CreatedAt, cursor.ID)
	}

	// 3. 稳定排序和分页
	query = query.Order("v.created_at DESC, v.id DESC").Limit(limit)

	// 4. 执行查询
	var records []VideoRecord
	if err := query.Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list following feed: %w", err)
	}

	return records, nil
}

func (r *GORMRepository) ListVisibleByIDs(
	ctx context.Context,
	followerID uint64,
	videoIDs []uint64,
) ([]VideoRecord, error) {
	if len(videoIDs) == 0 {
		return []VideoRecord{}, nil
	}

	var records []VideoRecord
	err := r.db.WithContext(ctx).
		Table("videos AS v").
		Select("v.id, v.author_id, v.title, v.description, v.content_type, v.created_at").
		Joins("JOIN follows AS f ON f.followee_id = v.author_id").
		Where("f.follower_id = ?", followerID).
		Where("v.id IN ?", videoIDs).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("list videos by ids: %w", err)
	}
	return records, nil
}
