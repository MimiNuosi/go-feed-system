package notification

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
)

type fakeNotificationService struct {
	listFunc        func(ctx context.Context, userID, cursor uint64, limit int) (*Page, error)
	unreadCountFunc func(ctx context.Context, userID uint64) (int64, error)
	markAllReadFunc func(ctx context.Context, userID uint64) error
	subscribeFunc   func(userID uint64) (<-chan Item, func())

	lastUserID uint64
	lastCursor uint64
	lastLimit  int
}

func (f *fakeNotificationService) List(
	ctx context.Context,
	userID, cursor uint64,
	limit int,
) (*Page, error) {
	f.lastUserID = userID
	f.lastCursor = cursor
	f.lastLimit = limit
	if f.listFunc != nil {
		return f.listFunc(ctx, userID, cursor, limit)
	}
	return &Page{Items: []Item{}}, nil
}

func (f *fakeNotificationService) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	f.lastUserID = userID
	if f.unreadCountFunc != nil {
		return f.unreadCountFunc(ctx, userID)
	}
	return 0, nil
}

func (f *fakeNotificationService) MarkAllRead(ctx context.Context, userID uint64) error {
	f.lastUserID = userID
	if f.markAllReadFunc != nil {
		return f.markAllReadFunc(ctx, userID)
	}
	return nil
}

func (f *fakeNotificationService) Subscribe(userID uint64) (<-chan Item, func()) {
	f.lastUserID = userID
	if f.subscribeFunc != nil {
		return f.subscribeFunc(userID)
	}
	ch := make(chan Item)
	return ch, func() { close(ch) }
}

func TestNotificationHandlerList(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		query          string
		hasAuth        bool
		serviceErr     error
		wantStatus     int
		wantUserID     uint64
		wantCursor     uint64
		wantLimit      int
		wantCallCount  int
		wantItemCount  int
		wantNextCursor string
	}{
		{
			name:           "成功获取通知",
			query:          "?cursor=30&page_size=10",
			hasAuth:        true,
			wantStatus:     http.StatusOK,
			wantUserID:     7,
			wantCursor:     30,
			wantLimit:      10,
			wantCallCount:  1,
			wantItemCount:  1,
			wantNextCursor: "20",
		},
		{
			name:          "未授权",
			query:         "",
			hasAuth:       false,
			wantStatus:    http.StatusUnauthorized,
			wantCallCount: 0,
		},
		{
			name:          "非法 cursor",
			query:         "?cursor=abc",
			hasAuth:       true,
			wantStatus:    http.StatusBadRequest,
			wantCallCount: 0,
		},
		{
			name:          "负数 page_size",
			query:         "?page_size=-1",
			hasAuth:       true,
			wantStatus:    http.StatusBadRequest,
			wantCallCount: 0,
		},
		{
			name:          "内部错误",
			query:         "",
			hasAuth:       true,
			serviceErr:    errors.New("db down"),
			wantStatus:    http.StatusInternalServerError,
			wantUserID:    7,
			wantCallCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			callCount := 0
			service := &fakeNotificationService{
				listFunc: func(ctx context.Context, userID, cursor uint64, limit int) (*Page, error) {
					callCount++
					if tt.serviceErr != nil {
						return nil, tt.serviceErr
					}
					return &Page{
						Items: []Item{{
							ID:    20,
							Actor: Actor{ID: 2, Username: "alice"},
							Kind:  "like",
						}},
						NextCursor: tt.wantNextCursor,
					}, nil
				},
			}
			handler := NewHandler(service, newNotificationDiscardLogger())
			engine := notificationTestEngine(handler, tt.hasAuth)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications"+tt.query, nil)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if callCount != tt.wantCallCount {
				t.Fatalf("List call count = %d, want %d", callCount, tt.wantCallCount)
			}
			if tt.wantCallCount > 0 {
				if service.lastUserID != tt.wantUserID ||
					service.lastCursor != tt.wantCursor ||
					service.lastLimit != tt.wantLimit {
					t.Fatalf(
						"unexpected service args: user=%d cursor=%d limit=%d",
						service.lastUserID,
						service.lastCursor,
						service.lastLimit,
					)
				}
			}
			if tt.wantStatus == http.StatusOK {
				var page Page
				if err := json.Unmarshal(recorder.Body.Bytes(), &page); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if len(page.Items) != tt.wantItemCount {
					t.Errorf("item count = %d, want %d", len(page.Items), tt.wantItemCount)
				}
				if page.NextCursor != tt.wantNextCursor {
					t.Errorf("next cursor = %q, want %q", page.NextCursor, tt.wantNextCursor)
				}
			}
		})
	}
}

func TestNotificationHandlerUnreadCountAndReadAll(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		method     string
		path       string
		hasAuth    bool
		serviceErr error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "未读数成功",
			method:     http.MethodGet,
			path:       "/api/v1/notifications/unread-count",
			hasAuth:    true,
			wantStatus: http.StatusOK,
			wantBody:   `{"count":3}`,
		},
		{
			name:       "未读数未授权",
			method:     http.MethodGet,
			path:       "/api/v1/notifications/unread-count",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "全部已读成功",
			method:     http.MethodPost,
			path:       "/api/v1/notifications/read-all",
			hasAuth:    true,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "全部已读内部错误",
			method:     http.MethodPost,
			path:       "/api/v1/notifications/read-all",
			hasAuth:    true,
			serviceErr: errors.New("db down"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeNotificationService{
				unreadCountFunc: func(ctx context.Context, userID uint64) (int64, error) {
					return 3, tt.serviceErr
				},
				markAllReadFunc: func(ctx context.Context, userID uint64) error {
					return tt.serviceErr
				},
			}
			handler := NewHandler(service, newNotificationDiscardLogger())
			engine := notificationTestEngine(handler, tt.hasAuth)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d, body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.wantBody != "" && strings.TrimSpace(recorder.Body.String()) != tt.wantBody {
				t.Errorf("body = %q, want %q", recorder.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestNotificationHandlerStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	events := make(chan Item, 1)
	events <- Item{
		ID:        9,
		Actor:     Actor{ID: 2, Username: "alice"},
		Kind:      "comment",
		Content:   "hello",
		CreatedAt: time.Now().UTC(),
	}
	unsubscribed := make(chan struct{}, 1)
	service := &fakeNotificationService{
		subscribeFunc: func(userID uint64) (<-chan Item, func()) {
			return events, func() {
				unsubscribed <- struct{}{}
			}
		},
	}
	handler := NewHandler(service, newNotificationDiscardLogger())
	engine := notificationTestEngine(handler, true)
	server := httptest.NewServer(engine)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		server.URL+"/api/v1/notifications/stream",
		nil,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content type = %q, want text/event-stream", got)
	}

	scanner := bufio.NewScanner(resp.Body)
	var eventName, data string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "event: ") {
			eventName = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = strings.TrimPrefix(line, "data: ")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	if eventName != "notification" {
		t.Errorf("event name = %q, want notification", eventName)
	}
	if !strings.Contains(data, `"username":"alice"`) {
		t.Errorf("event data = %q, want actor username", data)
	}

	cancel()
	select {
	case <-unsubscribed:
	case <-time.After(time.Second):
		t.Fatal("expected stream subscription to be cancelled")
	}
}

func notificationTestEngine(handler *Handler, hasAuth bool) *gin.Engine {
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if hasAuth {
			ctx := authctx.WithUserID(c.Request.Context(), 7)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	})
	engine.GET("/api/v1/notifications", handler.List)
	engine.GET("/api/v1/notifications/unread-count", handler.UnreadCount)
	engine.POST("/api/v1/notifications/read-all", handler.MarkAllRead)
	engine.GET("/api/v1/notifications/stream", handler.Stream)
	return engine
}
