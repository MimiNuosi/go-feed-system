package interaction

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// CommentRepository 定义评论模块的持久化能力。
type CommentRepository interface {
	// Create 创建评论。
	//
	// 数据库错误需要保留调用链。
	Create(ctx context.Context, comment *Comment) error

	// FindByID 查询一条未删除的评论。
	//
	// 查询不到时返回 ErrInvalidTarget，不暴露 gorm.ErrRecordNotFound。
	FindByID(ctx context.Context, id uint64) (*Comment, error)

	// ListByVideoID 按 ID 游标查询评论。
	//
	// lastID == 0 表示首页，lastID > 0 时只查询更小的 ID。
	// 结果按 id DESC 排序，limit 由 Service 传入，通常为 pageSize+1。
	ListByVideoID(ctx context.Context, videoID, lastID uint64, limit int) ([]Comment, error)

	// Delete 按评论 ID 和作者 ID 执行软删除。
	//
	// 删除不存在的记录也返回 nil，保持幂等。
	// 其他数据库错误需要保留调用链。
	Delete(ctx context.Context, commentID, userID uint64) error
}

type GORMCommentRepository struct {
	db *gorm.DB
}

func NewGORMCommentRepository(db *gorm.DB) *GORMCommentRepository {
	return &GORMCommentRepository{
		db: db,
	}
}

func (r *GORMCommentRepository) Create(ctx context.Context, comment *Comment) error {
	if err := r.db.WithContext(ctx).Create(comment).Error; err != nil {
		return fmt.Errorf("create comment: %w", err)
	}
	return nil
}

func (r *GORMCommentRepository) FindByID(ctx context.Context, id uint64) (*Comment, error) {
	var comment Comment
	// 由于模型包含 gorm.DeletedAt，GORM 会自动加上 deleted_at IS NULL 的条件
	if err := r.db.WithContext(ctx).First(&comment, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrInvalidTarget // 转换为业务错误
		}
		return nil, fmt.Errorf("find comment by id: %w", err)
	}
	return &comment, nil
}

func (r *GORMCommentRepository) ListByVideoID(
	ctx context.Context,
	videoID, lastID uint64,
	limit int,
) ([]Comment, error) {
	var comments []Comment

	query := r.db.WithContext(ctx).Model(&Comment{}).Where("video_id = ?", videoID)

	// 游标分页核心：如果传了 lastID，就加 id < lastID 的条件
	if lastID > 0 {
		query = query.Where("id < ?", lastID)
	}

	// 按 id 降序，取 limit 条（这个 limit 通常是 Service 传进来的 pageSize + 1，用于判断 has_more）
	if err := query.Order("id DESC").Limit(limit).Find(&comments).Error; err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	return comments, nil
}

func (r *GORMCommentRepository) Delete(ctx context.Context, commentID, userID uint64) error {
	// 条件软删除：只能删除自己的评论
	// GORM 遇到 gorm.DeletedAt 会自动把 Delete 转换为 UPDATE deleted_at = now()
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", commentID, userID).
		Delete(&Comment{}).Error
	if err != nil {
		return fmt.Errorf("delete comment: %w", err)
	}
	// 删除 0 行也返回 nil，保持幂等
	return nil
}
