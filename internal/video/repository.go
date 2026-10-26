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
	ListByAuthor(
		ctx context.Context,
		authorID uint64,
		beforeID uint64,
		limit int,
	) ([]Video, error)
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
		// 注意：这里转成 string 是有意的。
		// DSN 开了 interpolateParams 时，[]byte 会被驱动转义成 _binary'...'，
		// MySQL 无法用它写入 JSON 列（见 outbox.Event.Payload 的注释）。
		event.Payload = string(payloadBytes)

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

// CountByAuthor 统计某作者已发布（ready）的作品数。
func (r *GORMRepository) CountByAuthor(ctx context.Context, authorID uint64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&Video{}).
		Where("author_id = ? AND status = ?", authorID, StatusReady).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("count videos by author: %w", err)
	}
	return count, nil
}

// ListByAuthor 按作者列出视频，id 倒序（即"最新发布在前"）。
//
// beforeID 是游标：传 0 表示第一页；否则只取 id 小于它的记录。
//
// 这里不需要复合游标（对比关注 Feed 的 (created_at, id)）：
// 这条列表的排序键就是主键本身，id 单调递增，不存在"同一时间戳"的并列问题。
// 什么时候该用复合游标？排序键不唯一的时候 —— 比如按发布时间排序，
// 同一毫秒可能有多条记录。
func (r *GORMRepository) ListByAuthor(
	ctx context.Context,
	authorID uint64,
	beforeID uint64,
	limit int,
) ([]Video, error) {
	if authorID == 0 {
		return nil, fmt.Errorf("list videos by author: author ID is zero: %w", ErrInvalidInput)
	}
	if limit <= 0 {
		return nil, fmt.Errorf("list videos by author: limit is not positive: %w", ErrInvalidInput)
	}

	query := r.db.WithContext(ctx).
		Where("author_id = ?", authorID).
		Where("status = ?", StatusReady)
	if beforeID > 0 {
		query = query.Where("id < ?", beforeID)
	}

	var videos []Video
	if err := query.Order("id DESC").Limit(limit).Find(&videos).Error; err != nil {
		return nil, fmt.Errorf("list videos by author: %w", err)
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
// 必须一条 UPDATE 完成自增，不能先 SELECT 再 UPDATE：后者在并发下会丢更新。
// 视频已被删除时 RowsAffected == 0，这不算是错误，增量直接丢弃，
// 返回 nil 让上层正常 ack 即可。
func (r *GORMRepository) AddViewCount(
	ctx context.Context,
	videoID uint64,
	delta int64,
) error {
	if videoID == 0 || delta <= 0 {
		return fmt.Errorf("invalid view count delta: %w", ErrInvalidInput)
	}

	result := r.db.WithContext(ctx).
		Model(&Video{}).
		Where("id = ?", videoID).
		Update("view_count", gorm.Expr("view_count + ?", delta))

	if result.Error != nil {
		return fmt.Errorf("add view count: %w", result.Error)
	}

	return nil
}
