package video

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"go-feed-system/internal/outbox"
)

type Repository interface {
	Create(ctx context.Context, video *Video) error
	CreateWithOutbox(ctx context.Context, video *Video, event *outbox.Event) error
	FindByID(ctx context.Context, id uint64) (*Video, error)
}

type GORMRepository struct {
	db           *gorm.DB
	outboxWriter outbox.Writer
}

func NewGORMRepository(db *gorm.DB, outboxWriter outbox.Writer) *GORMRepository {
	return &GORMRepository{
		db:           db,
		outboxWriter: outboxWriter,
	}
}

func (r *GORMRepository) Create(ctx context.Context, video *Video) error {
	return r.createWithTx(ctx, r.db, video)
}

func (r *GORMRepository) CreateWithOutbox(
	ctx context.Context,
	video *Video,
	event *outbox.Event,
) error {
	if video == nil || event == nil {
		return fmt.Errorf("invalid video or outbox event: %w", ErrInvalidInput)
	}
	if r.outboxWriter == nil {
		return errors.New("outbox writer is nil")
	}

	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := r.createWithTx(ctx, tx, video); err != nil {
			return fmt.Errorf("create video in transaction: %w", err)
		}

		event.AggregateID = video.ID

		payload := outbox.VideoPublishedPayload{
			VideoID:     video.ID,
			AuthorID:    video.AuthorID,
			PublishedAt: video.CreatedAt,
		}
		payloadBytes, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal video published event: %w", err)
		}
		event.Payload = payloadBytes

		if err := r.outboxWriter.CreateWithTx(ctx, tx, event); err != nil {
			return fmt.Errorf("create outbox event in transaction: %w", err)
		}

		return nil
	})
}

func (r *GORMRepository) createWithTx(ctx context.Context, tx *gorm.DB, video *Video) error {
	if err := tx.WithContext(ctx).Create(video).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrConflict
		}
		return fmt.Errorf("create video: %w", err)
	}

	return nil
}

func (r *GORMRepository) FindByID(ctx context.Context, id uint64) (*Video, error) {
	// TODO(阶段 3.1)：按主键查询视频。
	//
	// 查询不到时返回 ErrNotFound，不要泄露 gorm.ErrRecordNotFound。
	var video Video

	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&video).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find video by id: %w", err)
	}
	return &video, nil
}
