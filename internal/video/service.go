package video

import (
	"bufio"
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

	"go-feed-system/internal/outbox"
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
	// ContentType 是客户端声明的类型，只用于粗筛，绝不会写进数据库。
	ContentType string
	SizeBytes   int64
	Content     io.Reader
}

// allowedExtensions 是上传文件的扩展名粗筛表。
//
// 扩展名来自客户端文件名，随时可以被改名绕过，所以它只是廉价的第一道门。
// 真正的裁决是 SniffContainer 对文件内容的嗅探。
var allowedExtensions = map[string]bool{
	".mp4":  true,
	".m4v":  true, // 与 mp4 同容器，嗅探后统一归并为 video/mp4
	".mov":  true,
	".webm": true,
	".3gp":  true,
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

	// 1.2 后缀名粗筛（第一道防线）
	ext := strings.ToLower(filepath.Ext(filename))
	if !allowedExtensions[ext] {
		return nil, ErrUnsupportedMediaType
	}

	// 1.3 客户端 Content-Type 粗筛（第二道防线）
	if !isAcceptableClientContentType(input.ContentType) {
		return nil, ErrUnsupportedMediaType
	}

	// 1.4 先做完廉价的校验，再做需要读流的嗅探
	if input.SizeBytes <= 0 {
		return nil, ErrInvalidInput
	}

	if input.SizeBytes > MaxUploadSizeBytes {
		return nil, ErrFileTooLarge
	}
	if input.Content == nil {
		return nil, ErrInvalidInput
	}

	// 1.5 服务端容器嗅探（最终裁决）
	//
	// Peek 只查看文件头，不会消费数据：reader 之后仍能被完整写入存储。
	// 这是 Go 里替代 C++ seekg(0) 的惯用法，因为 io.Reader 没有回退能力。
	reader := bufio.NewReader(input.Content)
	header, err := reader.Peek(MaxSniffBytes)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("upload video: peek header: %w", err)
	}

	container, ok := SniffContainer(header)
	if !ok {
		return nil, ErrUnsupportedMediaType
	}

	if ext != container.Extension {
		// 扩展名和真实容器不一致不是错误：客户端可能只是改名或换了后缀。
		// 我们以内容为准放行，但必须留下可观测的痕迹。
		s.logger.Warn(
			"upload video: extension does not match sniffed container",
			"original_filename", filename,
			"extension", ext,
			"sniffed_mime", container.MIME,
			"sniffed_extension", container.Extension,
		)
	}

	// 2. 生成不可被客户端控制的 StorageKey
	// 使用 uuid 确保唯一性，并且加上日期做分层，防止单目录文件过多
	// 注意：为了防止后缀名包含路径穿越符，这里可以加个安全校验，但 uuid 足够安全
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generate storage key: %w", err)
	}
	// 后缀取嗅探结果，而不是客户端文件名，避免把可执行文件存成 .mp4
	storageKey := fmt.Sprintf(
		"videos/%s/%s%s",
		time.Now().Format("2006/01"),
		id.String(),
		container.Extension,
	)

	eventID, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generate outbox event id: %w", err)
	}

	// 3. 先保存文件
	err = s.storage.Save(ctx, storageKey, reader, input.SizeBytes, container.MIME)
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
		ContentType:      container.MIME,
		SizeBytes:        input.SizeBytes,
		Status:           StatusReady, // 如果是异步转码，这里可能就是 "pending"
		CreatedAt:        time.Now().UTC().Truncate(time.Millisecond),
	}

	event := &outbox.Event{
		EventID:   eventID.String(),
		EventType: outbox.EventTypeVideoPublished,
		Status:    outbox.StatusPending,
	}

	err = s.videos.CreateWithOutbox(ctx, video, event)
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
		return nil, fmt.Errorf("upload video: create video with outbox: %w", err)
	}

	// 6. 成功返回
	return video, nil
}

// isAcceptableClientContentType 做客户端声明的粗筛。
//
// 它只回答"这个声明有没有可能是一个视频"，不代表我们接受它：
// 真正的裁决在 SniffContainer。放行 application/octet-stream 是因为
// 很多 HTTP 客户端（Go 的 multipart 默认行为、PowerShell 的 -Form）
// 都会用它，拒绝它们只会误伤老实客户端，却挡不住想绕过的人。
func isAcceptableClientContentType(raw string) bool {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		// 没写或写得无法解析时，交给内容嗅探决定。
		return true
	}
	if strings.HasPrefix(mediaType, "video/") {
		return true
	}
	return mediaType == "application/octet-stream"
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
