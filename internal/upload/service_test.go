package upload

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"go-feed-system/internal/video"
)

type memRepo struct {
	sessions map[string]*Session
	chunks   map[string]map[int]int64
}

func newMemRepo() *memRepo {
	return &memRepo{sessions: map[string]*Session{}, chunks: map[string]map[int]int64{}}
}

func (r *memRepo) CreateSession(ctx context.Context, session *Session) error {
	copied := *session
	r.sessions[session.ID] = &copied
	r.chunks[session.ID] = map[int]int64{}
	return nil
}

func (r *memRepo) FindSession(ctx context.Context, id string) (*Session, error) {
	session, ok := r.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	copied := *session
	return &copied, nil
}

func (r *memRepo) FindActiveSessionByMD5(ctx context.Context, authorID uint64, fileMD5 string) (*Session, error) {
	for _, session := range r.sessions {
		if session.AuthorID == authorID && session.FileMD5 == fileMD5 && session.Status == StatusUploading {
			copied := *session
			return &copied, nil
		}
	}
	return nil, nil
}

func (r *memRepo) AddChunk(ctx context.Context, uploadID string, index int, sizeBytes int64) error {
	r.chunks[uploadID][index] = sizeBytes
	return nil
}

func (r *memRepo) ListChunkIndexes(ctx context.Context, uploadID string) ([]int, error) {
	indexes := make([]int, 0, len(r.chunks[uploadID]))
	for i := 0; i < len(r.chunks[uploadID]); i++ {
		if _, ok := r.chunks[uploadID][i]; ok {
			indexes = append(indexes, i)
		}
	}
	return indexes, nil
}

func (r *memRepo) MarkCompleted(ctx context.Context, uploadID string, videoID uint64) error {
	session := r.sessions[uploadID]
	session.Status = StatusCompleted
	session.VideoID = &videoID
	return nil
}

type fakeUploader struct {
	content []byte
	input   video.UploadInput
	err     error
}

func (f *fakeUploader) Upload(ctx context.Context, input video.UploadInput) (*video.Video, error) {
	f.input = input
	if f.err != nil {
		return nil, f.err
	}
	data, err := io.ReadAll(input.Content)
	if err != nil {
		return nil, err
	}
	f.content = data
	return &video.Video{ID: 99, Status: video.StatusReady}, nil
}

type fakeExisting struct {
	item *video.Video
}

func (f *fakeExisting) FindByAuthorAndMD5(ctx context.Context, authorID uint64, fileMD5 string) (*video.Video, error) {
	return f.item, nil
}

func testMD5(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}

func TestService_ChunkedUpload(t *testing.T) {
	baseDir := t.TempDir()
	repo := newMemRepo()
	uploader := &fakeUploader{}
	service := NewService(repo, uploader, &fakeExisting{}, baseDir, slog.Default())

	data := bytes.Repeat([]byte("a"), MinChunkSize+3)
	init, err := service.Init(context.Background(), InitInput{
		AuthorID:    1,
		FileName:    "demo.mp4",
		FileSize:    int64(len(data)),
		FileMD5:     testMD5(data),
		ContentType: "video/mp4",
		Title:       "demo",
		ChunkSize:   MinChunkSize,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if init.TotalChunks != 2 {
		t.Fatalf("expected 2 chunks, got %d", init.TotalChunks)
	}
	if _, _, err := service.UploadChunk(context.Background(), 1, init.UploadID, 0, int64(MinChunkSize), bytes.NewReader(data[:MinChunkSize])); err != nil {
		t.Fatalf("upload chunk 0: %v", err)
	}
	if _, _, err := service.UploadChunk(context.Background(), 1, init.UploadID, 1, 3, bytes.NewReader(data[MinChunkSize:])); err != nil {
		t.Fatalf("upload chunk 1: %v", err)
	}

	videoID, err := service.Complete(context.Background(), 1, init.UploadID)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if videoID != 99 {
		t.Fatalf("expected video 99, got %d", videoID)
	}
	if !bytes.Equal(uploader.content, data) {
		t.Fatalf("merged content mismatch: got %d bytes want %d", len(uploader.content), len(data))
	}
	if uploader.input.FileMD5 != testMD5(data) {
		t.Fatalf("file md5 not passed to video service")
	}
	if repo.sessions[init.UploadID].Status != StatusCompleted {
		t.Fatalf("session not marked completed")
	}
	if _, err := os.Stat(service.sessionDir(init.UploadID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected upload dir cleaned, err=%v", err)
	}
}

func TestService_Init_InstantUpload(t *testing.T) {
	repo := newMemRepo()
	service := NewService(repo, &fakeUploader{}, &fakeExisting{item: &video.Video{ID: 7}}, t.TempDir(), slog.Default())

	result, err := service.Init(context.Background(), InitInput{
		AuthorID:  1,
		FileName:  "demo.mp4",
		FileSize:  int64(MinChunkSize),
		FileMD5:   testMD5([]byte("x")),
		Title:     "demo",
		ChunkSize: MinChunkSize,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if result.VideoID == nil || *result.VideoID != 7 {
		t.Fatalf("expected instant upload video 7, got %+v", result)
	}
	if len(repo.sessions) != 0 {
		t.Fatalf("instant upload should not create a session")
	}
}

func TestService_Complete_Incomplete(t *testing.T) {
	repo := newMemRepo()
	service := NewService(repo, &fakeUploader{}, &fakeExisting{}, t.TempDir(), slog.Default())

	data := bytes.Repeat([]byte("b"), MinChunkSize+1)
	init, err := service.Init(context.Background(), InitInput{
		AuthorID:  1,
		FileName:  "demo.mp4",
		FileSize:  int64(len(data)),
		FileMD5:   testMD5(data),
		Title:     "demo",
		ChunkSize: MinChunkSize,
	})
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	if _, _, err := service.UploadChunk(context.Background(), 1, init.UploadID, 0, int64(MinChunkSize), bytes.NewReader(data[:MinChunkSize])); err != nil {
		t.Fatalf("upload chunk 0: %v", err)
	}

	if _, err := service.Complete(context.Background(), 1, init.UploadID); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("expected ErrIncomplete, got %v", err)
	}
}
