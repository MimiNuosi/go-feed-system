package feed

import (
	"context"
	"fmt"

	"gorm.io/gorm"
)

// statusReady 与 video.StatusReady 取值一致。
//
// feed 包在生产代码里不依赖 video 包（只有测试用），所以这里单独声明一次：
// 只有可播放的视频才该出现在 Feed 里。
const statusReady = "ready"

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
	ListBigAuthorIDs(ctx context.Context, followerID uint64, threshold int) ([]uint64, error)
	ListFollowingByAuthorIDs(
		ctx context.Context,
		authorIDs []uint64,
		cursor *Cursor,
		limit int,
	) ([]VideoRecord, error)
	// ListLatest 查询全站最新视频（不依赖关注关系）。
	ListLatest(ctx context.Context, cursor *Cursor, limit int) ([]VideoRecord, error)
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
		Select("v.id, v.author_id, v.title, v.description, v.content_type, v.view_count, v.created_at").
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

// ListLatest 查询全站最新视频，最新在前。
//
// 和 ListFollowing 的区别只有一个：不 JOIN 关注表。
// 所以它不需要登录，是陌生人发现新内容的入口。
// 排序与游标和关注 Feed 保持一致，用 (created_at, id) 复合游标 ——
// 同一毫秒可能发布多条视频，只用时间戳定位会把它们漏掉或算重。
func (r *GORMRepository) ListLatest(
	ctx context.Context,
	cursor *Cursor,
	limit int,
) ([]VideoRecord, error) {
	query := r.db.WithContext(ctx).
		Table("videos AS v").
		Select("v.id, v.author_id, v.title, v.description, v.content_type, v.view_count, v.created_at").
		Where("v.status = ?", statusReady)

	if cursor != nil {
		query = query.Where("(v.created_at, v.id) < (?, ?)", cursor.CreatedAt, cursor.ID)
	}

	var records []VideoRecord
	err := query.
		Order("v.created_at DESC, v.id DESC").
		Limit(limit).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("list latest feed: %w", err)
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
		Select("v.id, v.author_id, v.title, v.description, v.content_type, v.view_count, v.created_at").
		Joins("JOIN follows AS f ON f.followee_id = v.author_id").
		Where("f.follower_id = ?", followerID).
		Where("v.id IN ?", videoIDs).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("list videos by ids: %w", err)
	}
	return records, nil
}

func (r *GORMRepository) ListBigAuthorIDs(
	ctx context.Context,
	followerID uint64,
	threshold int,
) ([]uint64, error) {
	if followerID == 0 || threshold <= 0 {
		return []uint64{}, nil
	}

	var authorIDs []uint64
	err := r.db.WithContext(ctx).
		Table("follows AS cf").
		Select("cf.followee_id").
		Joins("JOIN follows AS af ON af.followee_id = cf.followee_id").
		Where("cf.follower_id = ?", followerID).
		Group("cf.followee_id").
		Having("COUNT(af.follower_id) >= ?", threshold).
		Pluck("cf.followee_id", &authorIDs).Error
	if err != nil {
		return nil, fmt.Errorf("list big author ids: %w", err)
	}
	return authorIDs, nil
}

func (r *GORMRepository) ListFollowingByAuthorIDs(
	ctx context.Context,
	authorIDs []uint64,
	cursor *Cursor,
	limit int,
) ([]VideoRecord, error) {
	if len(authorIDs) == 0 || limit <= 0 {
		return []VideoRecord{}, nil
	}

	query := r.db.WithContext(ctx).
		Table("videos AS v").
		Select("v.id, v.author_id, v.title, v.description, v.content_type, v.view_count, v.created_at").
		Where("v.author_id IN ?", authorIDs)

	if cursor != nil {
		query = query.Where("(v.created_at, v.id) < (?, ?)", cursor.CreatedAt, cursor.ID)
	}

	query = query.Order("v.created_at DESC, v.id DESC").Limit(limit)

	var records []VideoRecord
	if err := query.Find(&records).Error; err != nil {
		return nil, fmt.Errorf("list following by author ids: %w", err)
	}

	return records, nil
}
