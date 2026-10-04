package outbox

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Writer 负责把 Outbox 事件写入数据库。
//
// CreateWithTx 接收外部传入的事务对象，使视频记录和事件能够原子提交。
type Writer interface {
	CreateWithTx(ctx context.Context, tx *gorm.DB, event *Event) error
}

type Repository interface {
	Writer

	Create(ctx context.Context, event *Event) error
	ListPending(ctx context.Context, limit int) ([]Event, error)
	MarkPublished(ctx context.Context, id uint64, publishedAt time.Time) error
	IncrementRetry(ctx context.Context, id uint64, lastError string) error
}

type GORMRepository struct {
	db *gorm.DB
}

func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

func (r *GORMRepository) Create(ctx context.Context, event *Event) error {
	if err := r.db.WithContext(ctx).Create(event).Error; err != nil {
		return fmt.Errorf("create outbox event: %w", err)
	}
	return nil
}

func (r *GORMRepository) CreateWithTx(ctx context.Context, tx *gorm.DB, event *Event) error {
	if err := tx.WithContext(ctx).Create(event).Error; err != nil {
		return fmt.Errorf("create outbox event with tx: %w", err)
	}
	return nil
}

func (r *GORMRepository) ListPending(ctx context.Context, limit int) ([]Event, error) {
	if limit <= 0 {
		return []Event{}, nil
	}

	var events []Event
	// 核心：按 id ASC 排序，保证先产生的事件先发送（FIFO），
	// 同时 id 作为 created_at 相同时的第二排序键，保证分页稳定。
	err := r.db.WithContext(ctx).
		Where("status = ?", StatusPending).
		Order("id ASC").
		Limit(limit).
		Find(&events).Error
	if err != nil {
		return nil, fmt.Errorf("list pending outbox events: %w", err)
	}

	return events, nil
}

func (r *GORMRepository) MarkPublished(ctx context.Context, id uint64, publishedAt time.Time) error {
	// 只允许 pending -> published，重复标记保持幂等。
	err := r.db.WithContext(ctx).
		Model(&Event{}).
		Where("id = ? AND status = ?", id, StatusPending).
		Updates(map[string]interface{}{
			"status":       StatusPublished,
			"published_at": publishedAt,
			"updated_at":   time.Now(),
		}).Error
	if err != nil {
		return fmt.Errorf("mark outbox event published: %w", err)
	}
	return nil
}

func (r *GORMRepository) IncrementRetry(ctx context.Context, id uint64, lastError string) error {
	// 核心：使用 gorm.Expr 实现 SQL 层面的原子自增
	// 如果不使用原子自增，两个 publisher 同时处理同一条数据时会互相覆盖 retry_count
	err := r.db.WithContext(ctx).
		Model(&Event{}).
		Where("id = ? AND status = ?", id, StatusPending).
		Updates(map[string]interface{}{
			"retry_count": gorm.Expr("retry_count + 1"),
			"last_error":  truncateLastError(lastError),
			"updated_at":  time.Now(),
		}).Error
	if err != nil {
		return fmt.Errorf("increment outbox retry: %w", err)
	}
	return nil
}

func truncateLastError(value string) string {
	const maxRunes = 500

	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}

	return string(runes[:maxRunes])
}
