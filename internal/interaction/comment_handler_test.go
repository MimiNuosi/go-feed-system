package interaction

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
)

// 1. 定义 Fake CommentService
type fakeCommentService struct {
	CreateFunc func(ctx context.Context, userID, videoID uint64, content string) (*Comment, error)
	ListFunc   func(ctx context.Context, videoID, cursor uint64, pageSize int) (*CommentPage, error)
	DeleteFunc func(ctx context.Context, userID, commentID uint64) error

	CreateCallCount int
	ListCallCount   int
	DeleteCallCount int

	LastUserID    uint64
	LastVideoID   uint64
	LastCommentID uint64
	LastContent   string
	LastCursor    uint64
	LastPageSize  int
}

func (f *fakeCommentService) Create(ctx context.Context, userID, videoID uint64, content string) (*Comment, error) {
	f.CreateCallCount++
	f.LastUserID = userID
	f.LastVideoID = videoID
	f.LastContent = content
	if f.CreateFunc != nil {
		return f.CreateFunc(ctx, userID, videoID, content)
	}
	return &Comment{ID: 1, VideoID: videoID, UserID: userID, Content: content, CreatedAt: time.Now()}, nil
}

func (f *fakeCommentService) List(ctx context.Context, videoID, cursor uint64, pageSize int) (*CommentPage, error) {
	f.ListCallCount++
	f.LastVideoID = videoID
	f.LastCursor = cursor
	f.LastPageSize = pageSize
	if f.ListFunc != nil {
		return f.ListFunc(ctx, videoID, cursor, pageSize)
	}
	return &CommentPage{Items: []CommentItem{}, HasMore: false}, nil
}

func (f *fakeCommentService) Delete(ctx context.Context, userID, commentID uint64) error {
	f.DeleteCallCount++
	f.LastUserID = userID
	f.LastCommentID = commentID
	if f.DeleteFunc != nil {
		return f.DeleteFunc(ctx, userID, commentID)
	}
	return nil
}

// 2. 测试 Create
func TestCommentHandler_Create(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		videoID       string
		hasAuth       bool
		body          string // 请求体 JSON 字符串
		serviceErr    error
		wantStatus    int
		wantCallCount int
		wantUserID    uint64
		wantVideoID   uint64
		wantContent   string
	}{
		{
			name:          "成功创建评论 201",
			videoID:       "1",
			hasAuth:       true,
			body:          `{"content":"这是一条测试评论"}`,
			wantStatus:    http.StatusCreated,
			wantCallCount: 1,
			wantUserID:    1,
			wantVideoID:   1,
			wantContent:   "这是一条测试评论",
		},
		{
			name:       "未授权 401",
			videoID:    "1",
			hasAuth:    false,
			body:       `{"content":"test"}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "非法视频 ID 400",
			videoID:    "abc",
			hasAuth:    true,
			body:       `{"content":"test"}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "非法 JSON 请求体 400",
			videoID:    "1",
			hasAuth:    true,
			body:       `{"content":123}`, // 类型错误
			wantStatus: http.StatusBadRequest,
		},
		{
			name:          "视频不存在 404",
			videoID:       "999",
			hasAuth:       true,
			body:          `{"content":"test"}`,
			serviceErr:    ErrInvalidTarget,
			wantStatus:    http.StatusNotFound,
			wantCallCount: 1,
			wantUserID:    1,
			wantVideoID:   999,
			wantContent:   "test",
		},
		{
			name:          "内部错误 500",
			videoID:       "1",
			hasAuth:       true,
			body:          `{"content":"test"}`,
			serviceErr:    errors.New("db down"),
			wantStatus:    http.StatusInternalServerError,
			wantCallCount: 1,
			wantUserID:    1,
			wantVideoID:   1,
			wantContent:   "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeCommentService{
				CreateFunc: func(ctx context.Context, userID, videoID uint64, content string) (*Comment, error) {
					if tt.serviceErr != nil {
						return nil, tt.serviceErr // 失败时返回 nil 和错误
					}
					// 成功时返回一个非空对象，让 Handler 能正常读取字段
					return &Comment{
						ID:        1,
						VideoID:   videoID,
						UserID:    userID,
						Content:   content,
						CreatedAt: time.Now(),
					}, nil
				},
			}
			handler := NewCommentHandler(service, newDiscardLogger())

			engine := gin.New()
			engine.POST("/api/v1/videos/:id/comments", func(c *gin.Context) {
				if tt.hasAuth {
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.Create(c)
			})

			req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+tt.videoID+"/comments", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d. body=%s", tt.wantStatus, w.Code, w.Body.String())
			}
			if service.CreateCallCount != tt.wantCallCount {
				t.Fatalf("expected Create called %d times, got %d", tt.wantCallCount, service.CreateCallCount)
			}
			if tt.wantCallCount > 0 {
				if service.LastUserID != tt.wantUserID {
					t.Errorf("expected userID %d, got %d", tt.wantUserID, service.LastUserID)
				}
				if service.LastVideoID != tt.wantVideoID {
					t.Errorf("expected videoID %d, got %d", tt.wantVideoID, service.LastVideoID)
				}
				if service.LastContent != tt.wantContent {
					t.Errorf("expected content %q, got %q", tt.wantContent, service.LastContent)
				}
			}
		})
	}
}

// 3. 测试 List
func TestCommentHandler_List(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		videoID       string
		queryString   string // URL Query 参数
		serviceErr    error
		wantStatus    int
		wantCallCount int
		wantVideoID   uint64
		wantCursor    uint64
		wantPageSize  int
	}{
		{
			name:          "成功获取列表 200",
			videoID:       "1",
			queryString:   "cursor=10&page_size=5",
			wantStatus:    http.StatusOK,
			wantCallCount: 1,
			wantVideoID:   1,
			wantCursor:    10,
			wantPageSize:  5,
		},
		{
			name:          "默认参数 200",
			videoID:       "1",
			queryString:   "", // 不传参数
			wantStatus:    http.StatusOK,
			wantCallCount: 1,
			wantVideoID:   1,
			wantCursor:    0,
			wantPageSize:  0, // Handler 传给 Service 是 0，由 Service 兜底
		},
		{
			name:        "非法视频 ID 400",
			videoID:     "abc",
			queryString: "",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "非法 cursor 400",
			videoID:     "1",
			queryString: "cursor=abc",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "非法 page_size 400",
			videoID:     "1",
			queryString: "page_size=abc",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "负数 page_size 400",
			videoID:     "1",
			queryString: "page_size=-1",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:          "视频不存在 404",
			videoID:       "999",
			queryString:   "",
			serviceErr:    ErrInvalidTarget,
			wantStatus:    http.StatusNotFound,
			wantCallCount: 1,
			wantVideoID:   999,
		},
		{
			name:          "内部错误 500",
			videoID:       "1",
			queryString:   "",
			serviceErr:    errors.New("db down"),
			wantStatus:    http.StatusInternalServerError,
			wantCallCount: 1,
			wantVideoID:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeCommentService{
				ListFunc: func(ctx context.Context, videoID, cursor uint64, pageSize int) (*CommentPage, error) {
					return nil, tt.serviceErr
				},
			}
			handler := NewCommentHandler(service, newDiscardLogger())

			engine := gin.New()
			engine.GET("/api/v1/videos/:id/comments", handler.List)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+tt.videoID+"/comments?"+tt.queryString, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d. body=%s", tt.wantStatus, w.Code, w.Body.String())
			}
			if service.ListCallCount != tt.wantCallCount {
				t.Fatalf("expected List called %d times, got %d", tt.wantCallCount, service.ListCallCount)
			}
			if tt.wantCallCount > 0 {
				if service.LastVideoID != tt.wantVideoID {
					t.Errorf("expected videoID %d, got %d", tt.wantVideoID, service.LastVideoID)
				}
				if service.LastCursor != tt.wantCursor {
					t.Errorf("expected cursor %d, got %d", tt.wantCursor, service.LastCursor)
				}
				if service.LastPageSize != tt.wantPageSize {
					t.Errorf("expected pageSize %d, got %d", tt.wantPageSize, service.LastPageSize)
				}
			}
		})
	}
}

// 4. 测试 Delete
func TestCommentHandler_Delete(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		commentID     string
		hasAuth       bool
		serviceErr    error
		wantStatus    int
		wantCallCount int
		wantUserID    uint64
		wantCommentID uint64
	}{
		{
			name:          "成功删除评论 204",
			commentID:     "100",
			hasAuth:       true,
			wantStatus:    http.StatusNoContent,
			wantCallCount: 1,
			wantUserID:    1,
			wantCommentID: 100,
		},
		{
			name:       "未授权 401",
			commentID:  "100",
			hasAuth:    false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "非法评论 ID 400",
			commentID:  "abc",
			hasAuth:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:          "非作者删除 403",
			commentID:     "100",
			hasAuth:       true,
			serviceErr:    ErrForbidden,
			wantStatus:    http.StatusForbidden,
			wantCallCount: 1,
			wantUserID:    1,
			wantCommentID: 100,
		},
		{
			name:          "内部错误 500",
			commentID:     "100",
			hasAuth:       true,
			serviceErr:    errors.New("db down"),
			wantStatus:    http.StatusInternalServerError,
			wantCallCount: 1,
			wantUserID:    1,
			wantCommentID: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeCommentService{
				DeleteFunc: func(ctx context.Context, userID, commentID uint64) error {
					return tt.serviceErr
				},
			}
			handler := NewCommentHandler(service, newDiscardLogger())

			engine := gin.New()
			engine.DELETE("/api/v1/comments/:id", func(c *gin.Context) {
				if tt.hasAuth {
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.Delete(c)
			})

			req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/"+tt.commentID, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d. body=%s", tt.wantStatus, w.Code, w.Body.String())
			}
			if service.DeleteCallCount != tt.wantCallCount {
				t.Fatalf("expected Delete called %d times, got %d", tt.wantCallCount, service.DeleteCallCount)
			}
			if tt.wantCallCount > 0 {
				if service.LastUserID != tt.wantUserID {
					t.Errorf("expected userID %d, got %d", tt.wantUserID, service.LastUserID)
				}
				if service.LastCommentID != tt.wantCommentID {
					t.Errorf("expected commentID %d, got %d", tt.wantCommentID, service.LastCommentID)
				}
			}
		})
	}
}
