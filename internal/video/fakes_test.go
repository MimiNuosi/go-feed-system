package video

import (
	"bytes"
	"context"
	"io"

	"go-feed-system/internal/outbox"
	"go-feed-system/internal/user"
)

type fakeVideoService struct {
	UploadFunc    func(ctx context.Context, input UploadInput) (*Video, error)
	GetDetailFunc func(ctx context.Context, id uint64) (*VideoDetail, error)
	OpenFileFunc  func(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error)
}

func (f *fakeVideoService) Upload(ctx context.Context, input UploadInput) (*Video, error) {
	if f.UploadFunc != nil {
		return f.UploadFunc(ctx, input)
	}
	return &Video{ID: 1}, nil
}

func (f *fakeVideoService) GetByID(ctx context.Context, id uint64) (*Video, error) {
	return nil, ErrNotFound
}

func (f *fakeVideoService) GetDetail(ctx context.Context, id uint64) (*VideoDetail, error) {
	if f.GetDetailFunc != nil {
		return f.GetDetailFunc(ctx, id)
	}
	return nil, ErrNotFound
}

func (f *fakeVideoService) OpenFile(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error) {
	if f.OpenFileFunc != nil {
		return f.OpenFileFunc(ctx, id)
	}
	return nil, nil, ErrNotFound
}

type fakeVideoRepository struct {
	CreateFunc           func(ctx context.Context, video *Video) error
	CreateWithOutboxFunc func(ctx context.Context, video *Video, event *outbox.Event) error
	FindByIDFunc         func(ctx context.Context, id uint64) (*Video, error)
}

func (f *fakeVideoRepository) Create(ctx context.Context, video *Video) error {
	if f.CreateFunc != nil {
		return f.CreateFunc(ctx, video)
	}
	return nil
}

func (f *fakeVideoRepository) CreateWithOutbox(ctx context.Context, video *Video, event *outbox.Event) error {
	if f.CreateWithOutboxFunc != nil {
		return f.CreateWithOutboxFunc(ctx, video, event)
	}
	if f.CreateFunc != nil {
		return f.CreateFunc(ctx, video)
	}
	return nil
}

func (f *fakeVideoRepository) FindByID(ctx context.Context, id uint64) (*Video, error) {
	if f.FindByIDFunc != nil {
		return f.FindByIDFunc(ctx, id)
	}
	return nil, ErrNotFound
}

type fakeObjectStorage struct {
	SaveFunc    func(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error
	OpenFunc    func(ctx context.Context, key string) (io.ReadSeekCloser, error)
	DeleteFunc  func(ctx context.Context, key string) error
	SaveCount   int
	DeleteCount int
	LastKey     string
}

func (f *fakeObjectStorage) Save(ctx context.Context, key string, reader io.Reader, size int64, contentType string) error {
	f.SaveCount++
	f.LastKey = key
	if f.SaveFunc != nil {
		return f.SaveFunc(ctx, key, reader, size, contentType)
	}
	return nil
}

func (f *fakeObjectStorage) Open(ctx context.Context, key string) (io.ReadSeekCloser, error) {
	if f.OpenFunc != nil {
		return f.OpenFunc(ctx, key)
	}
	return nil, nil
}

func (f *fakeObjectStorage) Delete(ctx context.Context, key string) error {
	f.DeleteCount++
	if f.DeleteFunc != nil {
		return f.DeleteFunc(ctx, key)
	}
	return nil
}

type fakeUserReader struct {
	GetByIDFunc func(ctx context.Context, id uint64) (*user.User, error)
}

func (f *fakeUserReader) GetByID(ctx context.Context, id uint64) (*user.User, error) {
	if f.GetByIDFunc != nil {
		return f.GetByIDFunc(ctx, id)
	}
	return &user.User{ID: id, Username: "test-user"}, nil
}

type fakeReadSeekCloser struct {
	*bytes.Reader
}

func (f *fakeReadSeekCloser) Close() error {
	return nil
}
