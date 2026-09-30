package interaction

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
)

type fakeLikeService struct {
	LikeFunc   func(ctx context.Context, userID, videoID uint64) error
	UnlikeFunc func(ctx context.Context, userID, videoID uint64) error

	LikeCallCount   int
	UnlikeCallCount int
	LastUserID      uint64
	LastVideoID     uint64
}

func (f *fakeLikeService) Like(ctx context.Context, userID, videoID uint64) error {
	f.LikeCallCount++
	f.LastUserID = userID
	f.LastVideoID = videoID
	if f.LikeFunc != nil {
		return f.LikeFunc(ctx, userID, videoID)
	}
	return nil
}

func (f *fakeLikeService) Unlike(ctx context.Context, userID, videoID uint64) error {
	f.UnlikeCallCount++
	f.LastUserID = userID
	f.LastVideoID = videoID
	if f.UnlikeFunc != nil {
		return f.UnlikeFunc(ctx, userID, videoID)
	}
	return nil
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestLikeHandler_Like(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		videoID       string
		hasAuth       bool
		serviceErr    error
		wantStatus    int
		wantCallCount int
		wantUserID    uint64
		wantVideoID   uint64
	}{
		{
			name:          "成功点赞 204",
			videoID:       "2",
			hasAuth:       true,
			wantStatus:    http.StatusNoContent,
			wantCallCount: 1,
			wantUserID:    1,
			wantVideoID:   2,
		},
		{
			name:       "未授权 401",
			videoID:    "2",
			hasAuth:    false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "非法视频 ID 400",
			videoID:    "abc",
			hasAuth:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "视频 ID 为 0 返回 400",
			videoID:    "0",
			hasAuth:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:          "视频不存在 404",
			videoID:       "999",
			hasAuth:       true,
			serviceErr:    ErrInvalidTarget,
			wantStatus:    http.StatusNotFound,
			wantCallCount: 1,
			wantUserID:    1,
			wantVideoID:   999,
		},
		{
			name:          "内部错误 500",
			videoID:       "2",
			hasAuth:       true,
			serviceErr:    errors.New("db down"),
			wantStatus:    http.StatusInternalServerError,
			wantCallCount: 1,
			wantUserID:    1,
			wantVideoID:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeLikeService{
				LikeFunc: func(ctx context.Context, userID, videoID uint64) error {
					return tt.serviceErr
				},
				UnlikeFunc: func(ctx context.Context, userID, videoID uint64) error {
					return tt.serviceErr
				},
			}
			handler := NewLikeHandler(service, newDiscardLogger())

			engine := gin.New()
			engine.POST("/api/v1/videos/:id/like", func(c *gin.Context) {
				if tt.hasAuth {
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.Like(c)
			})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+tt.videoID+"/like", nil)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d. body=%s", tt.wantStatus, recorder.Code, recorder.Body.String())
			}
			if service.LikeCallCount != tt.wantCallCount {
				t.Fatalf("expected Like called %d times, got %d", tt.wantCallCount, service.LikeCallCount)
			}
			if tt.wantCallCount > 0 {
				if service.LastUserID != tt.wantUserID {
					t.Errorf("expected userID %d, got %d", tt.wantUserID, service.LastUserID)
				}
				if service.LastVideoID != tt.wantVideoID {
					t.Errorf("expected videoID %d, got %d", tt.wantVideoID, service.LastVideoID)
				}
			}
		})
	}
}

func TestLikeHandler_Unlike(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		videoID       string
		hasAuth       bool
		serviceErr    error
		wantStatus    int
		wantCallCount int
		wantUserID    uint64
		wantVideoID   uint64
	}{
		{
			name:          "成功取消点赞 204",
			videoID:       "2",
			hasAuth:       true,
			wantStatus:    http.StatusNoContent,
			wantCallCount: 1,
			wantUserID:    1,
			wantVideoID:   2,
		},
		{
			name:       "未授权 401",
			videoID:    "2",
			hasAuth:    false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "非法视频 ID 400",
			videoID:    "abc",
			hasAuth:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:          "内部错误 500",
			videoID:       "2",
			hasAuth:       true,
			serviceErr:    errors.New("db down"),
			wantStatus:    http.StatusInternalServerError,
			wantCallCount: 1,
			wantUserID:    1,
			wantVideoID:   2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeLikeService{
				UnlikeFunc: func(ctx context.Context, userID, videoID uint64) error {
					return tt.serviceErr
				},
			}
			handler := NewLikeHandler(service, newDiscardLogger())

			engine := gin.New()
			engine.DELETE("/api/v1/videos/:id/like", func(c *gin.Context) {
				if tt.hasAuth {
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.Unlike(c)
			})

			req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/"+tt.videoID+"/like", nil)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d. body=%s", tt.wantStatus, recorder.Code, recorder.Body.String())
			}
			if service.UnlikeCallCount != tt.wantCallCount {
				t.Fatalf("expected Unlike called %d times, got %d", tt.wantCallCount, service.UnlikeCallCount)
			}
			if tt.wantCallCount > 0 {
				if service.LastUserID != tt.wantUserID {
					t.Errorf("expected userID %d, got %d", tt.wantUserID, service.LastUserID)
				}
				if service.LastVideoID != tt.wantVideoID {
					t.Errorf("expected videoID %d, got %d", tt.wantVideoID, service.LastVideoID)
				}
			}
		})
	}
}
