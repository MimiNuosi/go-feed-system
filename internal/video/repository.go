package video

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

type Repository interface {
	Create(ctx context.Context, video *Video) error
	FindByID(ctx context.Context, id uint64) (*Video, error)
}

type GORMRepository struct {
	db *gorm.DB
}

func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{
		db: db,
	}
}

func (r *GORMRepository) Create(ctx context.Context, video *Video) error {
	// TODO(阶段 3.1)：使用 GORM 插入视频元数据。
	//
	// 要求：
	// 1. 使用 r.db.WithContext(ctx)。
	// 2. storage_key 唯一冲突时返回业务可识别的错误。
	// 3. 其他错误使用 fmt.Errorf("create video: %w", err) 包装。
	if err := r.db.WithContext(ctx).Create(video).Error; err != nil {
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
