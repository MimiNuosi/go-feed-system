package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
)

// 1. 定义 Fake Service
type fakeFollowService struct {
	FollowFunc      func(ctx context.Context, followerID, followeeID uint64) error
	UnfollowFunc    func(ctx context.Context, followerID, followeeID uint64) error
	IsFollowingFunc func(ctx context.Context, followerID, followeeID uint64) (bool, error)
}

func (f *fakeFollowService) Follow(ctx context.Context, followerID, followeeID uint64) error {
	if f.FollowFunc != nil {
		return f.FollowFunc(ctx, followerID, followeeID)
	}
	return nil
}
func (f *fakeFollowService) Unfollow(ctx context.Context, followerID, followeeID uint64) error {
	if f.UnfollowFunc != nil {
		return f.UnfollowFunc(ctx, followerID, followeeID)
	}
	return nil
}
func (f *fakeFollowService) IsFollowing(ctx context.Context, followerID, followeeID uint64) (bool, error) {
	if f.IsFollowingFunc != nil {
		return f.IsFollowingFunc(ctx, followerID, followeeID)
	}
	return false, nil
}

// 2. 测试 Follow / Unfollow 接口
func TestFollowHandler_Follow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		urlID          string // URL 里的 :id 参数
		hasAuth        bool   // 是否在 Context 中注入 UserID
		mockSvcFunc    func(ctx context.Context, followerID, followeeID uint64) error
		wantStatusCode int
	}{
		{
			name:           "成功关注 204",
			urlID:          "2",
			hasAuth:        true,
			mockSvcFunc:    func(ctx context.Context, followerID, followeeID uint64) error { return nil },
			wantStatusCode: http.StatusNoContent,
		},
		{
			name:           "未授权 401",
			urlID:          "2",
			hasAuth:        false, // 缺失 authctx.UserID
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name:           "非法用户 ID 400",
			urlID:          "abc",
			hasAuth:        true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "ID 为 0 400",
			urlID:          "0",
			hasAuth:        true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:    "关注自己 400",
			urlID:   "1",
			hasAuth: true,
			mockSvcFunc: func(ctx context.Context, followerID, followeeID uint64) error {
				return ErrInvalidInput
			},
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:    "目标用户不存在 404",
			urlID:   "999",
			hasAuth: true,
			mockSvcFunc: func(ctx context.Context, followerID, followeeID uint64) error {
				return ErrInvalidTarget
			},
			wantStatusCode: http.StatusNotFound,
		},
		{
			name:    "内部错误 500",
			urlID:   "2",
			hasAuth: true,
			mockSvcFunc: func(ctx context.Context, followerID, followeeID uint64) error {
				return errors.New("db down")
			},
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeSvc := &fakeFollowService{FollowFunc: tt.mockSvcFunc}
			handler := NewFollowHandler(fakeSvc, slog.New(slog.NewTextHandler(io.Discard, nil)))

			r := gin.New()
			r.POST("/api/v1/users/:id/follow", func(c *gin.Context) {
				if tt.hasAuth {
					// 模拟 Auth 中间件注入 UserID (followerID = 1)
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.Follow(c)
			})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+tt.urlID+"/follow", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d. Body: %s", tt.wantStatusCode, w.Code, w.Body.String())
			}
		})
	}
}

func TestFollowHandler_Unfollow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		urlID          string
		hasAuth        bool
		mockSvcFunc    func(ctx context.Context, followerID, followeeID uint64) error
		wantStatusCode int
	}{
		{
			name:           "成功取消关注 204",
			urlID:          "2",
			hasAuth:        true,
			mockSvcFunc:    func(ctx context.Context, followerID, followeeID uint64) error { return nil },
			wantStatusCode: http.StatusNoContent,
		},
		{
			name:           "未授权 401",
			urlID:          "2",
			hasAuth:        false,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name:           "非法 ID 400",
			urlID:          "xyz",
			hasAuth:        true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:    "内部错误 500",
			urlID:   "2",
			hasAuth: true,
			mockSvcFunc: func(ctx context.Context, followerID, followeeID uint64) error {
				return errors.New("db down")
			},
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeSvc := &fakeFollowService{UnfollowFunc: tt.mockSvcFunc}
			handler := NewFollowHandler(fakeSvc, slog.New(slog.NewTextHandler(io.Discard, nil)))

			r := gin.New()
			r.DELETE("/api/v1/users/:id/follow", func(c *gin.Context) {
				if tt.hasAuth {
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.Unfollow(c)
			})

			req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+tt.urlID+"/follow", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d. Body: %s", tt.wantStatusCode, w.Code, w.Body.String())
			}
		})
	}
}

// 3. 测试 Status 接口（重点验证 JSON 响应体）
func TestFollowHandler_Status(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		urlID          string
		hasAuth        bool
		mockFollow     bool
		mockErr        error
		wantStatusCode int
		wantFollowing  bool
	}{
		{
			name:           "已关注 200",
			urlID:          "2",
			hasAuth:        true,
			mockFollow:     true,
			wantStatusCode: http.StatusOK,
			wantFollowing:  true,
		},
		{
			name:           "未关注 200",
			urlID:          "2",
			hasAuth:        true,
			mockFollow:     false,
			wantStatusCode: http.StatusOK,
			wantFollowing:  false,
		},
		{
			name:           "未授权 401",
			urlID:          "2",
			hasAuth:        false,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name:           "非法 ID 400",
			urlID:          "abc",
			hasAuth:        true,
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:           "内部错误 500",
			urlID:          "2",
			hasAuth:        true,
			mockErr:        errors.New("db down"),
			wantStatusCode: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeSvc := &fakeFollowService{
				IsFollowingFunc: func(ctx context.Context, followerID, followeeID uint64) (bool, error) {
					return tt.mockFollow, tt.mockErr
				},
			}
			handler := NewFollowHandler(fakeSvc, slog.New(slog.NewTextHandler(io.Discard, nil)))

			r := gin.New()
			r.GET("/api/v1/users/:id/follow/status", func(c *gin.Context) {
				if tt.hasAuth {
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.Status(c)
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+tt.urlID+"/follow/status", nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d. Body: %s", tt.wantStatusCode, w.Code, w.Body.String())
			}

			// 如果是 200，验证 JSON 内容
			if tt.wantStatusCode == http.StatusOK {
				var resp map[string]bool
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to decode JSON: %v", err)
				}
				if resp["following"] != tt.wantFollowing {
					t.Errorf("expected following %v, got %v", tt.wantFollowing, resp["following"])
				}
			}
		})
	}
}
