package user

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

var ErrRefreshTokenNotFound = errors.New("refresh token not found")

type RefreshTokenRepository interface {
	Create(ctx context.Context, token *RefreshToken) error
	FindByHash(ctx context.Context, tokenHash string) (*RefreshToken, error)
	Revoke(ctx context.Context, id uint64) error
	RevokeAllForUser(ctx context.Context, userID uint64) error
}

type GORMRefreshTokenRepository struct {
	db *gorm.DB
}

func NewGORMRefreshTokenRepository(db *gorm.DB) *GORMRefreshTokenRepository {
	return &GORMRefreshTokenRepository{db: db}
}

func (r *GORMRefreshTokenRepository) Create(ctx context.Context, token *RefreshToken) error {
	if err := r.db.WithContext(ctx).Create(token).Error; err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

func (r *GORMRefreshTokenRepository) FindByHash(ctx context.Context, tokenHash string) (*RefreshToken, error) {
	var token RefreshToken
	err := r.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&token).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrRefreshTokenNotFound
		}
		return nil, fmt.Errorf("find refresh token: %w", err)
	}
	return &token, nil
}

func (r *GORMRefreshTokenRepository) Revoke(ctx context.Context, id uint64) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := r.db.WithContext(ctx).
		Model(&RefreshToken{}).
		Where("id = ? AND revoked_at IS NULL", id).
		Update("revoked_at", now).Error
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

func (r *GORMRefreshTokenRepository) RevokeAllForUser(ctx context.Context, userID uint64) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := r.db.WithContext(ctx).
		Model(&RefreshToken{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Update("revoked_at", now).Error
	if err != nil {
		return fmt.Errorf("revoke all refresh tokens: %w", err)
	}
	return nil
}
