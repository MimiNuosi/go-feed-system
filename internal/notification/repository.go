package notification

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type Repository interface {
	Create(ctx context.Context, item *Notification) error
	ListByUser(ctx context.Context, userID uint64, beforeID uint64, limit int) ([]Notification, error)
	UnreadCount(ctx context.Context, userID uint64) (int64, error)
	MarkAllRead(ctx context.Context, userID uint64) error
}

type GORMRepository struct {
	db *gorm.DB
}

func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

func (r *GORMRepository) Create(ctx context.Context, item *Notification) error {
	if err := r.db.WithContext(ctx).Create(item).Error; err != nil {
		return fmt.Errorf("create notification: %w", err)
	}
	return nil
}

func (r *GORMRepository) ListByUser(
	ctx context.Context,
	userID uint64,
	beforeID uint64,
	limit int,
) ([]Notification, error) {
	if limit <= 0 {
		return []Notification{}, nil
	}
	query := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}
	var items []Notification
	if err := query.Order("id DESC").Limit(limit).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	return items, nil
}

func (r *GORMRepository) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&Notification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

func (r *GORMRepository) MarkAllRead(ctx context.Context, userID uint64) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := r.db.WithContext(ctx).
		Model(&Notification{}).
		Where("user_id = ? AND read_at IS NULL", userID).
		Update("read_at", now).Error
	if err != nil {
		return fmt.Errorf("mark notifications read: %w", err)
	}
	return nil
}
