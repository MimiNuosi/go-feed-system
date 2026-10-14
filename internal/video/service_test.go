package video

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"go-feed-system/internal/outbox"
	"go-feed-system/internal/user"
	"go-feed-system/pkg/storage"
)

// ftypHeader 构造一个真实结构的 ISO-BMFF ftyp box。
//
// 上传校验现在依赖内容嗅探，所以测试数据必须是真头，
// 不能像以前那样随手拼一段字符串。
func ftypHeader(major string, compatible ...string) []byte {
	var brands bytes.Buffer
	brands.WriteString(major)
	brands.Write([]byte{0x00, 0x00, 0x02, 0x00}) // minor_version
	for _, brand := range compatible {
		brands.WriteString(brand)
	}

	size := 8 + brands.Len()
	header := make([]byte, 0, size)
	header = binary.BigEndian.AppendUint32(header, uint32(size))
	header = append(header, "ftyp"...)
	header = append(header, brands.Bytes()...)
	return header
}

func TestService_ListByAuthor(t *testing.T) {
	errRepo := errors.New("repository failed")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name         string
		cursor       string
		pageSize     int
		repoVideos   []Video
		repoErr      error
		wantErr      error
		wantLen      int
		wantHasMore  bool
		wantNext     string
		wantBeforeID uint64
		wantLimit    int
	}{
		{
			name:       "第一页不带游标时从最新开始",
			pageSize:   2,
			repoVideos: []Video{{ID: 9}, {ID: 8}},
			wantLen:    2,
			wantLimit:  3, // pageSize + 1，多取一条判断还有没有下一页
		},
		{
			name:         "第二页把游标转成 beforeID",
			cursor:       "42",
			pageSize:     2,
			repoVideos:   []Video{{ID: 41}},
			wantLen:      1,
			wantBeforeID: 42,
			wantLimit:    3,
		},
		{
			name:        "多返回一条说明还有下一页",
			pageSize:    2,
			repoVideos:  []Video{{ID: 9}, {ID: 8}, {ID: 7}},
			wantLen:     2,
			wantHasMore: true,
			wantNext:    "8",
			wantLimit:   3,
		},
		{
			name:       "刚好取满说明是最后一页",
			pageSize:   2,
			repoVideos: []Video{{ID: 9}, {ID: 8}},
			wantLen:    2,
			wantLimit:  3,
		},
		{
			name:     "游标不是数字",
			cursor:   "abc",
			pageSize: 2,
			wantErr:  ErrInvalidInput,
		},
		{
			name:     "游标为 0 视为非法",
			cursor:   "0",
			pageSize: 2,
			wantErr:  ErrInvalidInput,
		},
		{
			name:      "页大小为空用默认值",
			pageSize:  0,
			wantLimit: DefaultAuthorVideoPageSize + 1,
		},
		{
			name:      "页大小超过上限被裁剪",
			pageSize:  1000,
			wantLimit: MaxAuthorVideoPageSize + 1,
		},
		{
			name:     "仓储错误原样透传",
			pageSize: 2,
			repoErr:  errRepo,
			wantErr:  errRepo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				gotBeforeID uint64
				gotLimit    int
				called      bool
			)
			repo := &fakeVideoRepository{
				ListByAuthorFunc: func(
					ctx context.Context,
					authorID uint64,
					beforeID uint64,
					limit int,
				) ([]Video, error) {
					called = true
					gotBeforeID = beforeID
					gotLimit = limit
					return tt.repoVideos, tt.repoErr
				},
			}
			svc := NewService(repo, &fakeObjectStorage{}, &fakeUserReader{}, logger)

			page, err := svc.ListByAuthor(context.Background(), 7, tt.cursor, tt.pageSize)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				if tt.cursor != "" && tt.cursor != "0" && called {
					t.Fatal("游标非法时不该查仓储")
				}
				return
			}
			if err != nil {
				t.Fatalf("list by author: %v", err)
			}

			if len(page.Videos) != tt.wantLen {
				t.Errorf("expected %d videos, got %d", tt.wantLen, len(page.Videos))
			}
			if page.HasMore != tt.wantHasMore {
				t.Errorf("expected HasMore %v, got %v", tt.wantHasMore, page.HasMore)
			}
			if page.NextCursor != tt.wantNext {
				t.Errorf("expected NextCursor %q, got %q", tt.wantNext, page.NextCursor)
			}
			if gotBeforeID != tt.wantBeforeID {
				t.Errorf("expected beforeID %d, got %d", tt.wantBeforeID, gotBeforeID)
			}
			if gotLimit != tt.wantLimit {
				t.Errorf("expected limit %d, got %d", tt.wantLimit, gotLimit)
			}
		})
	}
}

func TestService_Upload(t *testing.T) {
	mp4Content := append(ftypHeader("isom", "iso2", "avc1", "mp41"), []byte("payload")...)
	movContent := append(ftypHeader("qt  ", "qt  "), []byte("payload")...)
	m4vContent := append(ftypHeader("M4V ", "M4V ", "M4A ", "isom"), []byte("payload")...)
	webmContent := append([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte("payload")...)
	textContent := []byte("this is not a video at all")

	tests := []struct {
		name              string
		input             UploadInput
		content           []byte
		mockRepoCreate    func(ctx context.Context, video *Video) error
		mockStorageSave   func(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
		mockStorageDelete func(ctx context.Context, key string) error
		wantErr           error
		wantAnyErr        bool
		wantSaveCount     int
		wantDeleteCount   int
		wantContentType   string
		wantKeySuffix     string
	}{
		{
			name: "成功上传 mp4",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
			},
			content:         mp4Content,
			wantSaveCount:   1,
			wantDeleteCount: 0,
			wantContentType: "video/mp4",
			wantKeySuffix:   ".mp4",
		},
		{
			name: "客户端声明 application/octet-stream 也能上传",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "application/octet-stream",
				SizeBytes:        1024,
			},
			content:         mp4Content,
			wantSaveCount:   1,
			wantDeleteCount: 0,
			wantContentType: "video/mp4",
			wantKeySuffix:   ".mp4",
		},
		{
			name: "扩展名与真实容器不一致时按内容放行",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mov",
				ContentType:      "video/quicktime",
				SizeBytes:        1024,
			},
			content:         mp4Content,
			wantSaveCount:   1,
			wantDeleteCount: 0,
			wantContentType: "video/mp4",
			wantKeySuffix:   ".mp4",
		},
		{
			name: "真实 mov 内容",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mov",
				ContentType:      "video/quicktime",
				SizeBytes:        1024,
			},
			content:         movContent,
			wantSaveCount:   1,
			wantDeleteCount: 0,
			wantContentType: "video/quicktime",
			wantKeySuffix:   ".mov",
		},
		{
			name: "m4v 归并为 video/mp4",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.m4v",
				ContentType:      "video/x-m4v",
				SizeBytes:        1024,
			},
			content:         m4vContent,
			wantSaveCount:   1,
			wantDeleteCount: 0,
			wantContentType: "video/mp4",
			wantKeySuffix:   ".mp4",
		},
		{
			name: "真实 webm 内容",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.webm",
				ContentType:      "video/webm",
				SizeBytes:        1024,
			},
			content:         webmContent,
			wantSaveCount:   1,
			wantDeleteCount: 0,
			wantContentType: "video/webm",
			wantKeySuffix:   ".webm",
		},
		{
			name: "非法文件后缀",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.exe",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
			},
			content:         mp4Content,
			wantErr:         ErrUnsupportedMediaType,
			wantSaveCount:   0,
			wantDeleteCount: 0,
		},
		{
			name: "客户端声明 image/png 被粗筛拒绝",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "image/png",
				SizeBytes:        1024,
			},
			content:         mp4Content,
			wantErr:         ErrUnsupportedMediaType,
			wantSaveCount:   0,
			wantDeleteCount: 0,
		},
		{
			name: "扩展名对但内容不是视频",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
			},
			content:         textContent,
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
			},
			content:         mp4Content,
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
			},
			content: mp4Content,
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
			},
			content: mp4Content,
			mockRepoCreate: func(ctx context.Context, video *Video) error {
				return errors.New("mysql connection lost")
			},
			wantErr:         nil, // 只要 err != nil 即可
			wantAnyErr:      true,
			wantSaveCount:   1,
			wantDeleteCount: 1, // 核心断言：必须调用一次 Delete
		},
		{
			name: "数据库冲突，必须触发文件补偿删除",
			input: UploadInput{
				AuthorID:         1,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
			},
			content: mp4Content,
			mockRepoCreate: func(ctx context.Context, video *Video) error {
				return ErrConflict
			},
			wantErr:         ErrConflict,
			wantSaveCount:   1,
			wantDeleteCount: 1, // 核心断言
		},
		{
			name: "作者 ID 为 0",
			input: UploadInput{
				AuthorID:         0,
				Title:            "测试视频",
				OriginalFilename: "test.mp4",
				ContentType:      "video/mp4",
				SizeBytes:        1024,
			},
			content:         mp4Content,
			wantErr:         ErrInvalidInput,
			wantSaveCount:   0,
			wantDeleteCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var createdVideo *Video

			// 每个用例都必须有独立的 reader。
			// reader 是一次性游标，如果在表定义里共用一个，
			// 第一个用例就会把它读空，后面的用例只会看到 EOF。
			input := tt.input
			input.Content = bytes.NewReader(tt.content)

			// 构造 Fake 依赖
			repo := &fakeVideoRepository{
				CreateFunc: func(ctx context.Context, video *Video) error {
					createdVideo = video
					if tt.mockRepoCreate != nil {
						return tt.mockRepoCreate(ctx, video)
					}
					return nil
				},
			}
			storageMock := &fakeObjectStorage{
				SaveFunc:   tt.mockStorageSave,
				DeleteFunc: tt.mockStorageDelete,
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			svc := NewService(repo, storageMock, &fakeUserReader{}, logger)

			// 执行
			_, err := svc.Upload(context.Background(), input)

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

			// 入库的类型和存储键的后缀必须来自嗅探结果，而不是客户端声明
			if tt.wantContentType != "" {
				if createdVideo == nil {
					t.Fatal("expected a created video")
				}
				if createdVideo.ContentType != tt.wantContentType {
					t.Errorf(
						"expected stored content type %q, got %q",
						tt.wantContentType,
						createdVideo.ContentType,
					)
				}
			}
			if tt.wantKeySuffix != "" &&
				!strings.HasSuffix(storageMock.LastKey, tt.wantKeySuffix) {
				t.Errorf(
					"expected storage key suffix %q, got %q",
					tt.wantKeySuffix,
					storageMock.LastKey,
				)
			}
		})
	}
}

func TestService_Upload_BuildsOutboxEvent(t *testing.T) {
	var capturedVideo *Video
	var capturedEvent *outbox.Event

	repo := &fakeVideoRepository{
		CreateWithOutboxFunc: func(ctx context.Context, video *Video, event *outbox.Event) error {
			capturedVideo = video
			capturedEvent = event
			video.ID = 1001
			return nil
		},
	}
	storageMock := &fakeObjectStorage{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := NewService(repo, storageMock, &fakeUserReader{}, logger)

	_, err := svc.Upload(context.Background(), UploadInput{
		AuthorID:         1,
		Title:            "测试视频",
		OriginalFilename: "test.mp4",
		ContentType:      "video/mp4",
		SizeBytes:        1024,
		Content: bytes.NewReader(
			append(ftypHeader("isom", "iso2", "avc1", "mp41"), []byte("fake video")...),
		),
	})
	if err != nil {
		t.Fatalf("upload video: %v", err)
	}

	if capturedVideo == nil || capturedEvent == nil {
		t.Fatal("expected repository to receive video and outbox event")
	}
	if _, err := uuid.Parse(capturedEvent.EventID); err != nil {
		t.Errorf("expected valid event ID, got %q: %v", capturedEvent.EventID, err)
	}
	if capturedEvent.EventType != outbox.EventTypeVideoPublished {
		t.Errorf("expected event type %q, got %q", outbox.EventTypeVideoPublished, capturedEvent.EventType)
	}
	if capturedEvent.Status != outbox.StatusPending {
		t.Errorf("expected status %q, got %q", outbox.StatusPending, capturedEvent.Status)
	}
	if capturedVideo.CreatedAt.IsZero() {
		t.Error("expected video CreatedAt to be set before entering repository")
	}
	if capturedVideo.CreatedAt.Nanosecond()%int(time.Millisecond) != 0 {
		t.Errorf("expected millisecond precision CreatedAt, got %v", capturedVideo.CreatedAt)
	}
}

func TestService_GetDetail(t *testing.T) {
	// 1. 初始化 Fake 依赖
	fakeRepo := &fakeVideoRepository{
		FindByIDFunc: func(ctx context.Context, id uint64) (*Video, error) {
			return &Video{
				ID:        1,
				AuthorID:  1,
				Title:     "test video",
				ViewCount: 42,
			}, nil
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
		// 浏览量必须从 Video 透传到 VideoDetail
		if detail.ViewCount != 42 {
			t.Errorf("expected view count 42, got %d", detail.ViewCount)
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
