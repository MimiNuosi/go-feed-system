package video

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"go-feed-system/internal/user"
	"go-feed-system/pkg/storage"
)

func TestService_Upload(t *testing.T) {
	validContent := strings.NewReader("this is a fake mp4 video content")

	tests := []struct {
		name              string
		input             UploadInput
		mockRepoCreate    func(ctx context.Context, video *Video) error
		mockStorageSave   func(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
		mockStorageDelete func(ctx context.Context, key string) error
		wantErr           error
		wantAnyErr        bool
		wantSaveCount     int
		wantDeleteCount   int
	}{
		{
			name: "成功上传",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
				Content:          validContent,
			},
			wantErr:         nil,
			wantSaveCount:   1,
			wantDeleteCount: 0,
		},
		{
			name: "非法文件后缀",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.exe",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
				Content:          validContent,
			},
			wantErr:         ErrUnsupportedMediaType,
			wantSaveCount:   0,
			wantDeleteCount: 0,
		},
		{
			name: "非法 Content-Type",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "application/octet-stream",
				SizeBytes:        1024,
				Content:          validContent,
			},
			wantErr:         ErrUnsupportedMediaType,
			wantSaveCount:   0,
			wantDeleteCount: 0,
		},
		{
			name: "文件过大",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        MaxUploadSizeBytes + 1,
				Content:          validContent,
			},
			wantErr:         ErrFileTooLarge,
			wantSaveCount:   0,
			wantDeleteCount: 0,
		},
		{
			name: "文件保存失败",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
				Content:          validContent,
			},
			mockStorageSave: func(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
				return errors.New("disk full")
			},
			wantErr:         nil, // 只需要断言 errors.Is(err, ErrInvalidInput) 为 false 即可，这里我们看 error 是否包含特定字符串
			wantAnyErr:      true,
			wantSaveCount:   1,
			wantDeleteCount: 0,
		},
		{
			name: "数据库写入失败，必须触发文件补偿删除",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
				Content:          validContent,
			},
			mockRepoCreate: func(ctx context.Context, video *Video) error {
				return errors.New("mysql connection lost")
			},
			wantErr:         nil, // 只要 err != nil 即可
			wantAnyErr:      true,
			wantSaveCount:   1,
			wantDeleteCount: 1, // ✅ 核心断言：必须调用一次 Delete
		},
		{
			name: "数据库冲突，必须触发文件补偿删除",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
				Content:          validContent,
			},
			mockRepoCreate: func(ctx context.Context, video *Video) error {
				return ErrConflict
			},
			wantErr:         ErrConflict,
			wantSaveCount:   1,
			wantDeleteCount: 1, // ✅ 核心断言
		},
		{
			name: "作者 ID 为 0",
			input: UploadInput{
				AuthorID:         0,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
				Content:          validContent,
			},
			wantErr:         ErrInvalidInput,
			wantSaveCount:   0,
			wantDeleteCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 Fake 依赖
			repo := &fakeVideoRepository{
				CreateFunc: tt.mockRepoCreate,
			}
			storageMock := &fakeObjectStorage{
				SaveFunc:   tt.mockStorageSave,
				DeleteFunc: tt.mockStorageDelete,
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			svc := NewService(repo, storageMock, &fakeUserReader{}, logger)

			// 执行
			_, err := svc.Upload(context.Background(), tt.input)

			// 断言错误 (如果是特殊错误，需要精准断言)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else {
				// 对于 "文件保存失败" 这种没有具体哨兵错误的，只要 err != nil 就说明逻辑生效了
				if tt.wantAnyErr && err == nil {
					t.Fatal("expected an error")
				}
			}

			// 核心断言：调用次数
			if storageMock.SaveCount != tt.wantSaveCount {
				t.Errorf("expected Save to be called %d times, got %d", tt.wantSaveCount, storageMock.SaveCount)
			}
			if storageMock.DeleteCount != tt.wantDeleteCount {
				t.Errorf("expected Delete to be called %d times, got %d", tt.wantDeleteCount, storageMock.DeleteCount)
			}
		})
	}
}

func TestService_GetDetail(t *testing.T) {
	// 1. 初始化 Fake 依赖
	fakeRepo := &fakeVideoRepository{
		FindByIDFunc: func(ctx context.Context, id uint64) (*Video, error) {
			return &Video{ID: 1, AuthorID: 1, Title: "test video"}, nil
		},
	}
	fakeStorage := &fakeObjectStorage{}
	fakeUserReader := &fakeUserReader{
		GetByIDFunc: func(ctx context.Context, id uint64) (*user.User, error) {
			return &user.User{ID: 1, Username: "user001"}, nil
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := NewService(fakeRepo, fakeStorage, fakeUserReader, logger)

	// 2. 测试成功获取
	t.Run("成功获取详情", func(t *testing.T) {
		detail, err := svc.GetDetail(context.Background(), 1)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if detail.Title != "test video" {
			t.Errorf("expected title 'test video', got '%s'", detail.Title)
		}
		if detail.Author.Username != "user001" {
			t.Errorf("expected username 'user001', got '%s'", detail.Author.Username)
		}
	})

	// 3. 测试视频不存在
	t.Run("视频不存在", func(t *testing.T) {
		fakeRepo.FindByIDFunc = func(ctx context.Context, id uint64) (*Video, error) {
			return nil, ErrNotFound
		}
		_, err := svc.GetDetail(context.Background(), 999)
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestService_OpenFile(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// 1. 测试成功打开文件
	t.Run("成功打开文件", func(t *testing.T) {
		fakeRepo := &fakeVideoRepository{
			FindByIDFunc: func(ctx context.Context, id uint64) (*Video, error) {
				return &Video{ID: 1, StorageKey: "test-key"}, nil
			},
		}
		fakeStorage := &fakeObjectStorage{
			OpenFunc: func(ctx context.Context, key string) (io.ReadSeekCloser, error) {
				return &fakeReadSeekCloser{}, nil // 返回一个空的 ReadSeekCloser 即可
			},
		}
		svc := NewService(fakeRepo, fakeStorage, &fakeUserReader{}, logger)

		_, video, err := svc.OpenFile(context.Background(), 1)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if video.ID != 1 {
			t.Errorf("expected video ID 1, got %d", video.ID)
		}
	})

	// 2. 测试底层文件不存在（核心：验证错误转换是否正确）
	t.Run("底层文件不存在", func(t *testing.T) {
		fakeRepo := &fakeVideoRepository{
			FindByIDFunc: func(ctx context.Context, id uint64) (*Video, error) {
				return &Video{ID: 1, StorageKey: "missing-key"}, nil
			},
		}
		fakeStorage := &fakeObjectStorage{
			OpenFunc: func(ctx context.Context, key string) (io.ReadSeekCloser, error) {
				return nil, storage.ErrObjectNotFound // 模拟底层存储报错
			},
		}
		svc := NewService(fakeRepo, fakeStorage, &fakeUserReader{}, logger)

		_, _, err := svc.OpenFile(context.Background(), 1)
		// 核心断言：必须转换成 video 包的业务错误
		if !errors.Is(err, ErrObjectNotFound) {
			t.Errorf("expected ErrObjectNotFound, got %v", err)
		}
	})
}
