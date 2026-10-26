// Package avatar 处理头像上传与读取。
//
// 独立于 user 包：user 负责账号，avatar 只依赖"按 ID 查用户 + 更新 avatar_key"
// 这两个窄接口，以及对象存储的 Save/Open/Delete。
package avatar

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"go-feed-system/internal/user"
)

const MaxAvatarSize = 2 << 20 // 2MB

var (
	ErrInvalidImage = errors.New("invalid avatar image")
	ErrNotFound     = errors.New("avatar not found")
)

var allowedTypes = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

type UserRepository interface {
	FindByID(ctx context.Context, id uint64) (*user.User, error)
	UpdateAvatarKey(ctx context.Context, id uint64, avatarKey string) error
}

type ObjectStorage interface {
	Save(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	Open(ctx context.Context, key string) (io.ReadSeekCloser, error)
	Delete(ctx context.Context, key string) error
}

type Service struct {
	users   UserRepository
	storage ObjectStorage
}

func NewService(users UserRepository, storage ObjectStorage) *Service {
	return &Service{users: users, storage: storage}
}

// Update 校验图片类型与大小，保存新头像并更新 users.avatar_key。
func (s *Service) Update(ctx context.Context, userID uint64, content io.Reader, size int64) (string, error) {
	if userID == 0 || content == nil || size <= 0 || size > MaxAvatarSize {
		return "", ErrInvalidImage
	}

	data, err := io.ReadAll(io.LimitReader(content, MaxAvatarSize+1))
	if err != nil {
		return "", fmt.Errorf("read avatar: %w", err)
	}
	if len(data) == 0 || len(data) > MaxAvatarSize {
		return "", ErrInvalidImage
	}

	contentType := http.DetectContentType(data)
	ext, ok := allowedTypes[contentType]
	if !ok {
		return "", ErrInvalidImage
	}

	existing, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return "", fmt.Errorf("find user: %w", err)
	}

	key := filepath.ToSlash(filepath.Join("avatars", uuid.NewString()+ext))
	if err := s.storage.Save(ctx, key, bytes.NewReader(data), int64(len(data)), contentType); err != nil {
		return "", fmt.Errorf("save avatar: %w", err)
	}
	if err := s.users.UpdateAvatarKey(ctx, userID, key); err != nil {
		_ = s.storage.Delete(ctx, key)
		return "", fmt.Errorf("update avatar key: %w", err)
	}
	// 旧头像删除失败不影响本次上传。
	if existing.AvatarKey != "" && existing.AvatarKey != key {
		_ = s.storage.Delete(ctx, existing.AvatarKey)
	}
	return key, nil
}

// Open 返回头像文件与 MIME 类型。
func (s *Service) Open(ctx context.Context, userID uint64) (io.ReadSeekCloser, string, error) {
	account, err := s.users.FindByID(ctx, userID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("find user: %w", err)
	}
	if account.AvatarKey == "" {
		return nil, "", ErrNotFound
	}

	file, err := s.storage.Open(ctx, account.AvatarKey)
	if err != nil {
		return nil, "", fmt.Errorf("open avatar: %w", err)
	}
	contentType := "image/jpeg"
	switch strings.ToLower(filepath.Ext(account.AvatarKey)) {
	case ".png":
		contentType = "image/png"
	case ".webp":
		contentType = "image/webp"
	}
	return file, contentType, nil
}
