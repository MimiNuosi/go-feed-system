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

func (r *GORMRepository) ListReadyByIDs(
	ctx context.Context,
	ids []uint64,
) ([]Video, error) {
	if len(ids) == 0 {
		return []Video{}, nil
	}

	var videos []Video

	err := r.db.WithContext(ctx).
		Where("id IN ?", ids).
		Where("status = ?", StatusReady).
		Find(&videos).Error
	if err != nil {
		return nil, fmt.Errorf("list ready videos by ids: %w", err)
	}
	if videos == nil {
		return []Video{}, nil
	}
	return videos, nil
}

// AddViewCount 把浏览量增量累加到 videos.view_count。
//
// 注意它不在 Repository 接口里：只有浏览量回写 Worker 需要这个能力，
// 而 hot 包按"接口定义在使用方"的原则自己声明了 ViewCountWriter。
//
// TODO(阶段 9.5)：这里由你实现。提示：
//
//  1. 必须一条 UPDATE 完成：SET view_count = view_count + ?。
//     不要先 SELECT 再 UPDATE，那会在并发下丢更新。
//     GORM 里可以用 gorm.Expr("view_count + ?", delta)。
//  2. 必须带 WHERE id = ? 条件。视频已被删除时 RowsAffected 会是 0，
//     这不算错误：增量直接丢弃，但要返回 nil，让上层正常 ack 掉。
//  3. 出错时用 fmt.Errorf("add view count: %w", err) 包装。
func (r *GORMRepository) AddViewCount(
	ctx context.Context,
	videoID uint64,
	delta int64,
) error {
	if videoID == 0 || delta <= 0 {
		return fmt.Errorf("invalid view count delta: %w", ErrInvalidInput)
	}
	return errors.New("AddViewCount 尚未实现：见 TODO(阶段 9.5)")
}
