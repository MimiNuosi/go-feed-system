package video

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"go-feed-system/internal/user"
	"go-feed-system/pkg/storage"

	"github.com/google/uuid"
)

const MaxUploadSizeBytes = 100 << 20

type UserReader interface {
	GetByID(ctx context.Context, id uint64) (*user.User, error)
}

type UploadInput struct {
	AuthorID         uint64
	Title            string
	Description      string
	OriginalFilename string
	ContentType      string
	SizeBytes        int64
	Content          io.Reader
}

type Service struct {
	videos     Repository
	storage    ObjectStorage
	userReader UserReader
	logger     *slog.Logger
}

func NewService(videos Repository, storage ObjectStorage, userReader UserReader, logger *slog.Logger) *Service {
	return &Service{
		videos:     videos,
		storage:    storage,
		userReader: userReader,
		logger:     logger,
	}
}

func (s *Service) Upload(ctx context.Context, input UploadInput) (*Video, error) {
	// 1. 校验输入
	// 1.1 规范化文件名（防止路径穿越、超长）
	if input.AuthorID == 0 {
		return nil, ErrInvalidInput
	}

	filename := filepath.Base(input.OriginalFilename)
	if len(filename) > 255 {
		return nil, fmt.Errorf("filename is too long: %w", ErrInvalidInput)
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, ErrInvalidInput
	}
	// 使用 RuneCountInString 而不是 len()，因为 len() 对中文计算字节数，会误伤
	if utf8.RuneCountInString(title) > 100 {
		return nil, ErrInvalidInput
	}
	if utf8.RuneCountInString(input.Description) > 1000 {
		return nil, ErrInvalidInput
	}

	// 1.2 后缀名白名单（第一道防线）
	ext := strings.ToLower(filepath.Ext(filename))
	allowedExts := map[string]bool{".mp4": true, ".mov": true, ".webm": true}
	if !allowedExts[ext] {
		return nil, ErrUnsupportedMediaType
	}

	// 1.3 客户端 Content-Type 校验（第二道防线）
	allowedTypes := map[string]bool{"video/mp4": true, "video/quicktime": true, "video/webm": true}
	mediaType, _, err := mime.ParseMediaType(input.ContentType)
	if err != nil || !allowedTypes[mediaType] {
		return nil, ErrUnsupportedMediaType
	}

	title = strings.TrimSpace(input.Title)
	if title == "" {
		return nil, ErrInvalidInput
	}

	if input.SizeBytes <= 0 {
		return nil, ErrInvalidInput
	}

	if input.SizeBytes > MaxUploadSizeBytes {
		return nil, ErrFileTooLarge
	}
	if input.Content == nil {
		return nil, ErrInvalidInput
	}

	// 2. 生成不可被客户端控制的 StorageKey
	// 使用 uuid 确保唯一性，并且加上日期做分层，防止单目录文件过多
	// 注意：为了防止后缀名包含路径穿越符，这里可以加个安全校验，但 uuid 足够安全
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generate storage key: %w", err)
	}
	storageKey := fmt.Sprintf("videos/%s/%s%s", time.Now().Format("2006/01"), id.String(), ext)

	// 3. 先保存文件
	err = s.storage.Save(ctx, storageKey, input.Content, input.SizeBytes, input.ContentType)
	if err != nil {
		return nil, fmt.Errorf("upload video: save file: %w", err)
	}

	// 4. 写入数据库元数据
	video := &Video{
		AuthorID:         input.AuthorID,
		Title:            title,
		Description:      input.Description,
		StorageKey:       storageKey,
		OriginalFilename: filename,
		ContentType:      input.ContentType,
		SizeBytes:        input.SizeBytes,
		Status:           StatusReady, // 如果是异步转码，这里可能就是 "pending"
	}

	err = s.videos.Create(ctx, video)
	if err != nil {
		// 5. 数据库写入失败，进行补偿：删除已经保存的文件
		// 创建独立的、带超时的 context，专门用于补偿清理
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if delErr := s.storage.Delete(cleanupCtx, storageKey); delErr != nil {
			// 这里必须记录日志，不能静默忽略
			s.logger.Error("failed to delete orphan file", "storage_key", storageKey, "error", delErr)
		}

		if errors.Is(err, ErrConflict) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("upload video: create metadata: %w", err)
	}

	// 6. 成功返回
	return video, nil
}

func (s *Service) GetByID(ctx context.Context, id uint64) (*Video, error) {
	item, err := s.videos.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get video by id: %w", err)
	}

	return item, nil
}

// 组合视频和作者信息
func (s *Service) GetDetail(ctx context.Context, id uint64) (*VideoDetail, error) {
	item, err := s.videos.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get video detail: %w", err)
	}

	author, err := s.userReader.GetByID(ctx, item.AuthorID)
	if err != nil {
		return nil, fmt.Errorf("get author detail: %w", err)
	}

	return &VideoDetail{
		ID:          item.ID,
		Title:       item.Title,
		Description: item.Description,
		ContentType: item.ContentType,
		SizeBytes:   item.SizeBytes,
		Status:      item.Status,
		Author: AuthorInfo{
			ID:       author.ID,
			Username: author.Username,
		},
	}, nil
}

// 提供文件流和元数据，供 Handler 使用
func (s *Service) OpenFile(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error) {
	item, err := s.videos.FindByID(ctx, id)
	if err != nil {
		return nil, nil, fmt.Errorf("find video: %w", err)
	}

	file, err := s.storage.Open(ctx, item.StorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			return nil, nil, ErrObjectNotFound
		}
		return nil, nil, fmt.Errorf("open storage file: %w", err)
	}
	return file, item, nil
}
