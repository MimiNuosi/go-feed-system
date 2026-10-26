package message

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
)

type Repository interface {
	Create(ctx context.Context, item *Message) error
	ListBetween(
		ctx context.Context,
		userA uint64,
		userB uint64,
		beforeID uint64,
		limit int,
	) ([]Message, error)
	ListLatestConversations(
		ctx context.Context,
		userID uint64,
		limit int,
	) ([]Message, error)
	UnreadCount(ctx context.Context, userID uint64) (int64, error)
	UnreadBySender(
		ctx context.Context,
		userID uint64,
		senderIDs []uint64,
	) (map[uint64]int64, error)
	MarkConversationRead(ctx context.Context, userID uint64, peerID uint64) error
}

type GORMRepository struct {
	db *gorm.DB
}

func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

func (r *GORMRepository) Create(ctx context.Context, item *Message) error {
	if err := r.db.WithContext(ctx).Create(item).Error; err != nil {
		return fmt.Errorf("create message: %w", err)
	}
	return nil
}

func (r *GORMRepository) ListBetween(
	ctx context.Context,
	userA uint64,
	userB uint64,
	beforeID uint64,
	limit int,
) ([]Message, error) {
	if limit <= 0 {
		return []Message{}, nil
	}

	query := r.db.WithContext(ctx).
		Where(
			"((sender_id = ? AND receiver_id = ?) OR (sender_id = ? AND receiver_id = ?))",
			userA,
			userB,
			userB,
			userA,
		)
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}

	var items []Message
	if err := query.Order("id DESC").Limit(limit).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return items, nil
}

func (r *GORMRepository) ListLatestConversations(
	ctx context.Context,
	userID uint64,
	limit int,
) ([]Message, error) {
	if limit <= 0 {
		return []Message{}, nil
	}

	var items []Message
	err := r.db.WithContext(ctx).Raw(`
		SELECT m.*
		FROM messages m
		JOIN (
			SELECT MAX(id) AS id
			FROM messages
			WHERE sender_id = ? OR receiver_id = ?
			GROUP BY CASE WHEN sender_id = ? THEN receiver_id ELSE sender_id END
		) latest ON latest.id = m.id
		ORDER BY m.id DESC
		LIMIT ?
	`, userID, userID, userID, limit).Scan(&items).Error
	if err != nil {
		return nil, fmt.Errorf("list latest conversations: %w", err)
	}
	return items, nil
}

func (r *GORMRepository) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&Message{}).
		Where("receiver_id = ? AND read_at IS NULL", userID).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count unread messages: %w", err)
	}
	return count, nil
}

func (r *GORMRepository) UnreadBySender(
	ctx context.Context,
	userID uint64,
	senderIDs []uint64,
) (map[uint64]int64, error) {
	result := make(map[uint64]int64, len(senderIDs))
	if len(senderIDs) == 0 {
		return result, nil
	}

	var rows []struct {
		SenderID uint64
		Count    int64
	}
	err := r.db.WithContext(ctx).
		Model(&Message{}).
		Select("sender_id, COUNT(*) AS count").
		Where("receiver_id = ? AND read_at IS NULL AND sender_id IN ?", userID, senderIDs).
		Group("sender_id").
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("count unread messages by sender: %w", err)
	}
	for _, row := range rows {
		result[row.SenderID] = row.Count
	}
	return result, nil
}

func (r *GORMRepository) MarkConversationRead(
	ctx context.Context,
	userID uint64,
	peerID uint64,
) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := r.db.WithContext(ctx).
		Model(&Message{}).
		Where(
			"receiver_id = ? AND sender_id = ? AND read_at IS NULL",
			userID,
			peerID,
		).
		Update("read_at", now).Error
	if err != nil {
		return fmt.Errorf("mark conversation read: %w", err)
	}
	return nil
}
