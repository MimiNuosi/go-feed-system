package upload

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrSessionNotFound = errors.New("upload session not found")

type Repository interface {
	CreateSession(ctx context.Context, session *Session) error
	FindSession(ctx context.Context, id string) (*Session, error)
	// FindActiveSessionByMD5 查找同一作者、同一 MD5、仍在上传中的会话（断点续传）。
	FindActiveSessionByMD5(ctx context.Context, authorID uint64, fileMD5 string) (*Session, error)
	// AddChunk 记录一个已上传的分片；重复上传同一片保持幂等。
	AddChunk(ctx context.Context, uploadID string, index int, sizeBytes int64) error
	// ListChunkIndexes 返回已收到的分片下标（升序），用于断点续传。
	ListChunkIndexes(ctx context.Context, uploadID string) ([]int, error)
	// MarkCompleted 原子地把会话标记为完成并写入最终 video_id。
	MarkCompleted(ctx context.Context, uploadID string, videoID uint64) error
}

type GORMRepository struct {
	db *gorm.DB
}

func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{db: db}
}

func (r *GORMRepository) CreateSession(ctx context.Context, session *Session) error {
	if err := r.db.WithContext(ctx).Create(session).Error; err != nil {
		return fmt.Errorf("create upload session: %w", err)
	}
	return nil
}

func (r *GORMRepository) FindSession(ctx context.Context, id string) (*Session, error) {
	var session Session
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("find upload session: %w", err)
	}
	return &session, nil
}

func (r *GORMRepository) FindActiveSessionByMD5(
	ctx context.Context,
	authorID uint64,
	fileMD5 string,
) (*Session, error) {
	var session Session
	err := r.db.WithContext(ctx).
		Where("author_id = ? AND file_md5 = ? AND status = ?", authorID, fileMD5, StatusUploading).
		Order("created_at DESC").
		First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("find active upload session: %w", err)
	}
	return &session, nil
}

// AddChunk 重复上传同一片时更新大小，不报唯一键冲突。
func (r *GORMRepository) AddChunk(ctx context.Context, uploadID string, index int, sizeBytes int64) error {
	chunk := &Chunk{UploadID: uploadID, Index: index, SizeBytes: sizeBytes}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "upload_id"}, {Name: "chunk_index"}},
		DoUpdates: clause.AssignmentColumns([]string{"size_bytes"}),
	}).Create(chunk).Error
	if err != nil {
		return fmt.Errorf("add upload chunk: %w", err)
	}
	return nil
}

func (r *GORMRepository) ListChunkIndexes(ctx context.Context, uploadID string) ([]int, error) {
	var indexes []int
	err := r.db.WithContext(ctx).
		Model(&Chunk{}).
		Where("upload_id = ?", uploadID).
		Order("chunk_index ASC").
		Pluck("chunk_index", &indexes).Error
	if err != nil {
		return nil, fmt.Errorf("list upload chunk indexes: %w", err)
	}
	return indexes, nil
}

func (r *GORMRepository) MarkCompleted(ctx context.Context, uploadID string, videoID uint64) error {
	result := r.db.WithContext(ctx).
		Model(&Session{}).
		Where("id = ? AND status = ?", uploadID, StatusUploading).
		Updates(map[string]any{"status": StatusCompleted, "video_id": videoID})
	if result.Error != nil {
		return fmt.Errorf("mark upload session completed: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		// 已经完成过：幂等返回成功，由调用方读取 video_id。
		return nil
	}
	return nil
}
