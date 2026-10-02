package feed

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
)

// 1. Fake ServiceAPI
type fakeFeedService struct {
	ListFollowingFunc func(ctx context.Context, userID uint64, cursor *Cursor, pageSize int) (*Page, error)

	CallCount    int
	LastUserID   uint64
	LastCursor   *Cursor
	LastPageSize int
}

func (f *fakeFeedService) ListFollowing(ctx context.Context, userID uint64, cursor *Cursor, pageSize int) (*Page, error) {
	f.CallCount++
	f.LastUserID = userID
	f.LastCursor = cursor
	f.LastPageSize = pageSize
	if f.ListFollowingFunc != nil {
		return f.ListFollowingFunc(ctx, userID, cursor, pageSize)
	}
	return &Page{Items: []Item{}, HasMore: false}, nil
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHandler_ListFollowing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 生成一个合法的游标，用于测试“有游标”的情况
	validCursorStr, err := encodeCursor(Cursor{
		CreatedAt: time.Now(),
		ID:        100,
	})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}

	tests := []struct {
		name           string
		hasAuth        bool
		queryString    string // URL Query 参数，比如 "cursor=...&page_size=20"
		mockServiceErr error
		wantStatus     int
		wantCallCount  int
		wantUserID     uint64
		wantCursorNil  bool
		wantCursorID   uint64
		wantPageSize   int
	}{
		{
			name:          "成功获取 Feed 200（第一页，无游标）",
			hasAuth:       true,
			queryString:   "page_size=5",
			wantStatus:    http.StatusOK,
			wantCallCount: 1,
			wantUserID:    1,
			wantCursorNil: true, // 核心：空游标必须解析成 nil
			wantPageSize:  5,
		},
		{
			name:          "未传 page_size 时传 0 交给 Service 兜底",
			hasAuth:       true,
			queryString:   "",
			wantStatus:    http.StatusOK,
			wantCallCount: 1,
			wantUserID:    1,
			wantCursorNil: true,
			wantPageSize:  0,
		},
		{
			name:          "成功获取 Feed 200（第二页，带游标）",
			hasAuth:       true,
			queryString:   "cursor=" + validCursorStr + "&page_size=10",
			wantStatus:    http.StatusOK,
			wantCallCount: 1,
			wantUserID:    1,
			wantCursorNil: false, // 核心：有效游标必须解析出内容
			wantCursorID:  100,
			wantPageSize:  10,
		},
		{
			name:          "未授权 401",
			hasAuth:       false,
			queryString:   "page_size=5",
			wantStatus:    http.StatusUnauthorized,
			wantCallCount: 0,
		},
		{
			name:          "非法游标 400",
			hasAuth:       true,
			queryString:   "cursor=invalid-base64-string!",
			wantStatus:    http.StatusBadRequest,
			wantCallCount: 0,
		},
		{
			name:          "非法 page_size 400（非数字）",
			hasAuth:       true,
			queryString:   "page_size=abc",
			wantStatus:    http.StatusBadRequest,
			wantCallCount: 0,
		},
		{
			name:          "非法 page_size 400（负数）",
			hasAuth:       true,
			queryString:   "page_size=-5",
			wantStatus:    http.StatusBadRequest,
			wantCallCount: 0,
		},
		{
			name:           "Service 返回业务错误 400",
			hasAuth:        true,
			queryString:    "page_size=5",
			mockServiceErr: ErrInvalidInput,
			wantStatus:     http.StatusBadRequest,
			wantCallCount:  1,
			wantUserID:     1,
			wantCursorNil:  true,
			wantPageSize:   5,
		},
		{
			name:           "Service 返回内部错误 500",
			hasAuth:        true,
			queryString:    "page_size=5",
			mockServiceErr: errors.New("db connection lost"),
			wantStatus:     http.StatusInternalServerError,
			wantCallCount:  1,
			wantUserID:     1,
			wantCursorNil:  true,
			wantPageSize:   5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeFeedService{
				ListFollowingFunc: func(ctx context.Context, userID uint64, cursor *Cursor, pageSize int) (*Page, error) {
					if tt.mockServiceErr != nil {
						return nil, tt.mockServiceErr
					}
					return &Page{Items: []Item{}}, nil
				},
			}
			handler := NewHandler(service, newDiscardLogger())

			engine := gin.New()
			engine.GET("/api/v1/feed/following", func(c *gin.Context) {
				if tt.hasAuth {
					// 模拟 Auth 中间件注入 UserID = 1
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.ListFollowing(c)
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/feed/following?"+tt.queryString, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			// 1. 断言状态码
			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d. body=%s", tt.wantStatus, w.Code, w.Body.String())
			}

			// 2. 断言 Service 调用次数
			if service.CallCount != tt.wantCallCount {
				t.Fatalf("expected Service called %d times, got %d", tt.wantCallCount, service.CallCount)
			}

			// 3. 断言透传参数
			if tt.wantCallCount > 0 {
				if service.LastUserID != tt.wantUserID {
					t.Errorf("expected userID %d, got %d", tt.wantUserID, service.LastUserID)
				}
				if (service.LastCursor == nil) != tt.wantCursorNil {
					t.Errorf("expected cursor nil %v, got %v", tt.wantCursorNil, service.LastCursor)
				}
				if !tt.wantCursorNil && service.LastCursor.ID != tt.wantCursorID {
					t.Errorf("expected cursor ID %d, got %d", tt.wantCursorID, service.LastCursor.ID)
				}
				if service.LastPageSize != tt.wantPageSize {
					t.Errorf("expected pageSize %d, got %d", tt.wantPageSize, service.LastPageSize)
				}
			}
		})
	}
}
