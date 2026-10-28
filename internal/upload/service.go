package upload

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"go-feed-system/internal/video"
	"go-feed-system/pkg/distlock"
)

var (
	ErrInvalidInput = errors.New("invalid upload input")
	ErrForbidden    = errors.New("upload session belongs to another user")
	ErrIncomplete   = errors.New("upload is not complete")
	ErrUploadBusy   = errors.New("upload merge is busy")
)

var md5Pattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

const (
	uploadMergeLockTTL  = 5 * time.Minute
	uploadMergeLockWait = 30 * time.Second
)

// VideoUploader 是完成分片后复用的整文件上传能力（video.Service 满足它）。
type VideoUploader interface {
	Upload(ctx context.Context, input video.UploadInput) (*video.Video, error)
}

// ExistingVideoReader 用于秒传：同一作者 + 同一 MD5 直接复用已有视频。
type ExistingVideoReader interface {
	FindByAuthorAndMD5(ctx context.Context, authorID uint64, fileMD5 string) (*video.Video, error)
}

type Service struct {
	repo     Repository
	videos   VideoUploader
	existing ExistingVideoReader
	locker   distlock.Locker
	baseDir  string
	logger   *slog.Logger

	mergeLockWait time.Duration
}

func NewService(
	repo Repository,
	videos VideoUploader,
	existing ExistingVideoReader,
	locker distlock.Locker,
	baseDir string,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:          repo,
		videos:        videos,
		existing:      existing,
		locker:        locker,
		baseDir:       baseDir,
		logger:        logger,
		mergeLockWait: uploadMergeLockWait,
	}
}

type InitInput struct {
	AuthorID    uint64
	FileName    string
	FileSize    int64
	FileMD5     string
	ContentType string
	Title       string
	Description string
	ChunkSize   int
}

type InitResult struct {
	UploadID    string  `json:"upload_id,omitempty"`
	ChunkSize   int     `json:"chunk_size"`
	TotalChunks int     `json:"total_chunks"`
	Uploaded    []int   `json:"uploaded"`
	VideoID     *uint64 `json:"video_id,omitempty"`
}

// Init 校验元信息、查秒传、创建上传会话。
func (s *Service) Init(ctx context.Context, input InitInput) (*InitResult, error) {
	if input.AuthorID == 0 {
		return nil, fmt.Errorf("author id is zero: %w", ErrInvalidInput)
	}

	name := filepath.Base(strings.TrimSpace(input.FileName))
	if name == "" || name == "." || len(name) > 255 {
		return nil, fmt.Errorf("invalid file name: %w", ErrInvalidInput)
	}
	title := strings.TrimSpace(input.Title)
	if title == "" || utf8.RuneCountInString(title) > 100 {
		return nil, fmt.Errorf("invalid title: %w", ErrInvalidInput)
	}
	if utf8.RuneCountInString(input.Description) > 1000 {
		return nil, fmt.Errorf("description too long: %w", ErrInvalidInput)
	}
	if input.FileSize <= 0 || input.FileSize > video.MaxUploadSizeBytes {
		return nil, fmt.Errorf("invalid file size: %w", ErrInvalidInput)
	}

	md5Value := strings.ToLower(strings.TrimSpace(input.FileMD5))
	if !md5Pattern.MatchString(md5Value) {
		return nil, fmt.Errorf("invalid md5: %w", ErrInvalidInput)
	}

	chunkSize := input.ChunkSize
	if chunkSize == 0 {
		chunkSize = DefaultChunkSize
	}
	if chunkSize < MinChunkSize || chunkSize > MaxChunkSize {
		return nil, fmt.Errorf("invalid chunk size: %w", ErrInvalidInput)
	}

	totalChunks := int((input.FileSize + int64(chunkSize) - 1) / int64(chunkSize))
	if totalChunks <= 0 || totalChunks > 10000 {
		return nil, fmt.Errorf("too many chunks: %w", ErrInvalidInput)
	}

	// 秒传：作者之前上传过完全相同的文件。
	if s.existing != nil {
		existing, err := s.existing.FindByAuthorAndMD5(ctx, input.AuthorID, md5Value)
		if err != nil {
			return nil, fmt.Errorf("check existing video: %w", err)
		}
		if existing != nil {
			id := existing.ID
			return &InitResult{ChunkSize: chunkSize, TotalChunks: 0, Uploaded: []int{}, VideoID: &id}, nil
		}
	}

	// 断点续传：同一作者、同一 MD5 还有未完成的会话，直接复用。
	if active, err := s.repo.FindActiveSessionByMD5(ctx, input.AuthorID, md5Value); err != nil {
		return nil, err
	} else if active != nil {
		indexes, err := s.repo.ListChunkIndexes(ctx, active.ID)
		if err != nil {
			return nil, err
		}
		return &InitResult{
			UploadID:    active.ID,
			ChunkSize:   active.ChunkSize,
			TotalChunks: active.TotalChunks,
			Uploaded:    indexes,
		}, nil
	}

	id, err := uuid.NewRandom()
	if err != nil {
		return nil, fmt.Errorf("generate upload id: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	session := &Session{
		ID:          id.String(),
		AuthorID:    input.AuthorID,
		FileName:    name,
		FileSize:    input.FileSize,
		FileMD5:     md5Value,
		ContentType: strings.TrimSpace(input.ContentType),
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
		Title:       title,
		Description: input.Description,
		Status:      StatusUploading,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, err
	}

	return &InitResult{
		UploadID:    session.ID,
		ChunkSize:   chunkSize,
		TotalChunks: totalChunks,
		Uploaded:    []int{},
	}, nil
}

// UploadChunk 把一片写到临时目录，并在 upload_chunks 里登记。
//
// 重复上传同一片会覆盖临时文件，数据库侧是 upsert，整体幂等。
func (s *Service) UploadChunk(
	ctx context.Context,
	authorID uint64,
	uploadID string,
	index int,
	size int64,
	content io.Reader,
) (int, int, error) {
	if authorID == 0 || uploadID == "" || index < 0 || size <= 0 || content == nil {
		return 0, 0, fmt.Errorf("invalid chunk input: %w", ErrInvalidInput)
	}

	session, err := s.repo.FindSession(ctx, uploadID)
	if err != nil {
		return 0, 0, err
	}
	if session.AuthorID != authorID {
		return 0, 0, ErrForbidden
	}
	if session.Status != StatusUploading {
		return 0, 0, fmt.Errorf("session already completed: %w", ErrInvalidInput)
	}
	if index >= session.TotalChunks {
		return 0, 0, fmt.Errorf("chunk index out of range: %w", ErrInvalidInput)
	}

	expected := int64(session.ChunkSize)
	if index == session.TotalChunks-1 {
		expected = session.FileSize - int64(session.ChunkSize)*(int64(session.TotalChunks)-1)
	}
	if size != expected {
		return 0, 0, fmt.Errorf("chunk size mismatch: got %d want %d: %w", size, expected, ErrInvalidInput)
	}

	dir := s.sessionDir(session.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, 0, fmt.Errorf("create upload dir: %w", err)
	}

	partPath := filepath.Join(dir, fmt.Sprintf("%d.part", index))
	if err := writeFileAtomic(partPath, content, expected); err != nil {
		return 0, 0, err
	}

	if err := s.repo.AddChunk(ctx, session.ID, index, size); err != nil {
		return 0, 0, err
	}

	indexes, err := s.repo.ListChunkIndexes(ctx, session.ID)
	if err != nil {
		return 0, 0, err
	}
	return len(indexes), session.TotalChunks, nil
}

// Status 返回会话状态与已上传分片下标，前端据此跳过已传分片。
func (s *Service) Status(ctx context.Context, authorID uint64, uploadID string) (*Session, []int, error) {
	session, err := s.repo.FindSession(ctx, uploadID)
	if err != nil {
		return nil, nil, err
	}
	if session.AuthorID != authorID {
		return nil, nil, ErrForbidden
	}
	indexes, err := s.repo.ListChunkIndexes(ctx, uploadID)
	if err != nil {
		return nil, nil, err
	}
	return session, indexes, nil
}

// Complete 校验分片齐全后按序合并，再复用整文件上传写库。
func (s *Service) Complete(ctx context.Context, authorID uint64, uploadID string) (uint64, error) {
	session, err := s.repo.FindSession(ctx, uploadID)
	if err != nil {
		return 0, err
	}
	if session.AuthorID != authorID {
		return 0, ErrForbidden
	}
	if session.Status == StatusCompleted && session.VideoID != nil {
		return *session.VideoID, nil
	}

	release, err := s.acquireMergeLock(ctx, uploadID)
	if err != nil {
		return 0, err
	}
	if release != nil {
		defer func() {
			if err := release(); err != nil {
				s.logger.Warn("release upload merge lock", "upload_id", uploadID, "error", err)
			}
		}()
	}

	// 第二个并发 complete 拿到锁后必须重新读会话：第一个请求可能已经完成。
	session, err = s.repo.FindSession(ctx, uploadID)
	if err != nil {
		return 0, err
	}
	if session.AuthorID != authorID {
		return 0, ErrForbidden
	}
	if session.Status == StatusCompleted && session.VideoID != nil {
		return *session.VideoID, nil
	}

	// 崩溃恢复：如果视频已经写库但会话没标记完成，按 MD5 找回并补标。
	if s.existing != nil {
		existing, err := s.existing.FindByAuthorAndMD5(ctx, authorID, session.FileMD5)
		if err != nil {
			return 0, fmt.Errorf("check existing video: %w", err)
		}
		if existing != nil {
			_ = s.repo.MarkCompleted(ctx, uploadID, existing.ID)
			return existing.ID, nil
		}
	}

	indexes, err := s.repo.ListChunkIndexes(ctx, uploadID)
	if err != nil {
		return 0, err
	}
	if len(indexes) != session.TotalChunks {
		return 0, fmt.Errorf("received %d/%d chunks: %w", len(indexes), session.TotalChunks, ErrIncomplete)
	}
	for i, index := range indexes {
		if index != i {
			return 0, fmt.Errorf("missing chunk %d: %w", i, ErrIncomplete)
		}
	}

	dir := s.sessionDir(session.ID)
	mergedPath := filepath.Join(dir, "merged.tmp")
	if err := s.mergeChunks(dir, mergedPath, session); err != nil {
		return 0, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	file, err := os.Open(mergedPath)
	if err != nil {
		return 0, fmt.Errorf("open merged file: %w", err)
	}
	defer file.Close()

	created, err := s.videos.Upload(ctx, video.UploadInput{
		AuthorID:         session.AuthorID,
		Title:            session.Title,
		Description:      session.Description,
		OriginalFilename: session.FileName,
		FileMD5:          session.FileMD5,
		ContentType:      session.ContentType,
		SizeBytes:        session.FileSize,
		Content:          file,
	})
	if err != nil {
		return 0, fmt.Errorf("upload merged video: %w", err)
	}

	if err := s.repo.MarkCompleted(ctx, uploadID, created.ID); err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (s *Service) acquireMergeLock(
	ctx context.Context,
	uploadID string,
) (distlock.UnlockFunc, error) {
	if s.locker == nil {
		return nil, nil
	}
	lockCtx, cancel := context.WithTimeout(ctx, s.mergeLockWait)
	defer cancel()
	release, err := s.locker.Acquire(
		lockCtx,
		"upload:merge:"+uploadID,
		uploadMergeLockTTL,
	)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("wait for upload merge lock: %w", ErrUploadBusy)
		}
		return nil, fmt.Errorf("acquire upload merge lock: %w", err)
	}
	return release, nil
}

// mergeChunks 按 index 升序拼接到目标文件，并校验每一片的大小。
//
// TODO(学习点)：这里有意留成独立函数，方便你对照 C++ 的
// offset = (seq-1)*MAX_FILE_LEN 写法，理解"顺序分片 + 合并"与
// "按偏移直接写最终文件"两种断点续传实现的差异。
func (s *Service) mergeChunks(dir, target string, session *Session) error {
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create merged file: %w", err)
	}
	defer out.Close()

	hasher := md5.New()
	writer := io.MultiWriter(out, hasher)

	for i := 0; i < session.TotalChunks; i++ {
		partPath := filepath.Join(dir, fmt.Sprintf("%d.part", i))
		info, err := os.Stat(partPath)
		if err != nil {
			return fmt.Errorf("stat chunk %d: %w", i, err)
		}
		expected := int64(session.ChunkSize)
		if i == session.TotalChunks-1 {
			expected = session.FileSize - int64(session.ChunkSize)*(int64(session.TotalChunks)-1)
		}
		if info.Size() != expected {
			return fmt.Errorf("chunk %d size %d want %d: %w", i, info.Size(), expected, ErrIncomplete)
		}

		part, err := os.Open(partPath)
		if err != nil {
			return fmt.Errorf("open chunk %d: %w", i, err)
		}
		if _, err := io.Copy(writer, part); err != nil {
			part.Close()
			return fmt.Errorf("copy chunk %d: %w", i, err)
		}
		if err := part.Close(); err != nil {
			return fmt.Errorf("close chunk %d: %w", i, err)
		}
	}

	if got := hex.EncodeToString(hasher.Sum(nil)); got != session.FileMD5 {
		return fmt.Errorf("merged file md5 %s want %s: %w", got, session.FileMD5, ErrIncomplete)
	}
	return out.Close()
}

func (s *Service) sessionDir(uploadID string) string {
	return filepath.Join(s.baseDir, "uploads", uploadID)
}

// writeFileAtomic 先写临时文件再 rename，避免半截分片被当成完整分片。
func writeFileAtomic(path string, content io.Reader, expected int64) error {
	tmp := fmt.Sprintf("%s.%s.tmp", path, uuid.NewString())
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create chunk temp file: %w", err)
	}

	written, copyErr := io.Copy(file, io.LimitReader(content, expected+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write chunk: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close chunk temp file: %w", closeErr)
	}
	if written != expected {
		_ = os.Remove(tmp)
		return fmt.Errorf("chunk size mismatch: wrote %d want %d: %w", written, expected, ErrInvalidInput)
	}
	if err := commitChunkFile(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit chunk file: %w", err)
	}
	return nil
}

// commitChunkFile 用 hard-link 原子地创建最终分片。
//
// 如果另一个并发请求已经提交同一片，hard-link 返回 EEXIST；此时保留先完成的
// 文件并删除自己的临时文件即可。这样两个 API 不会互相覆盖同名临时文件。
func commitChunkFile(tmp string, path string) error {
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return os.Remove(tmp)
		}
		return err
	}
	return os.Remove(tmp)
}
