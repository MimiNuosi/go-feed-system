package message

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

type fakeService struct {
	sendFunc              func(ctx context.Context, senderID, receiverID uint64, content string) (*Item, error)
	listFunc              func(ctx context.Context, userID, peerID, beforeID uint64, pageSize int) (*Page, error)
	listConversationsFunc func(ctx context.Context, userID uint64) (*ConversationList, error)
	unreadCountFunc       func(ctx context.Context, userID uint64) (int64, error)
	markReadFunc          func(ctx context.Context, userID, peerID uint64) error
	subscribeFunc         func(userID uint64) (<-chan Item, func())

	lastUserID   uint64
	lastPeerID   uint64
	lastBeforeID uint64
	lastPageSize int
	lastContent  string
}

func (f *fakeService) Send(
	ctx context.Context,
	senderID, receiverID uint64,
	content string,
) (*Item, error) {
	f.lastUserID = senderID
	f.lastPeerID = receiverID
	f.lastContent = content
	if f.sendFunc != nil {
		return f.sendFunc(ctx, senderID, receiverID, content)
	}
	return &Item{ID: 1, SenderID: senderID, ReceiverID: receiverID, Content: content}, nil
}

func (f *fakeService) List(
	ctx context.Context,
	userID, peerID, beforeID uint64,
	pageSize int,
) (*Page, error) {
	f.lastUserID = userID
	f.lastPeerID = peerID
	f.lastBeforeID = beforeID
	f.lastPageSize = pageSize
	if f.listFunc != nil {
		return f.listFunc(ctx, userID, peerID, beforeID, pageSize)
	}
	return &Page{Items: []Item{}}, nil
}

func (f *fakeService) ListConversations(
	ctx context.Context,
	userID uint64,
) (*ConversationList, error) {
	f.lastUserID = userID
	if f.listConversationsFunc != nil {
		return f.listConversationsFunc(ctx, userID)
	}
	return &ConversationList{Items: []Conversation{}}, nil
}

func (f *fakeService) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	f.lastUserID = userID
	if f.unreadCountFunc != nil {
		return f.unreadCountFunc(ctx, userID)
	}
	return 0, nil
}

func (f *fakeService) MarkConversationRead(ctx context.Context, userID, peerID uint64) error {
	f.lastUserID = userID
	f.lastPeerID = peerID
	if f.markReadFunc != nil {
		return f.markReadFunc(ctx, userID, peerID)
	}
	return nil
}

func (f *fakeService) Subscribe(userID uint64) (<-chan Item, func()) {
	f.lastUserID = userID
	if f.subscribeFunc != nil {
		return f.subscribeFunc(userID)
	}
	ch := make(chan Item)
	return ch, func() { close(ch) }
}

func TestHandlerSend(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name         string
		body         string
		hasAuth      bool
		serviceErr   error
		wantStatus   int
		wantCalls    int
		wantReceiver uint64
		wantContent  string
	}{
		{
			name:         "成功发送",
			body:         `{"receiver_id":2,"content":"hello"}`,
			hasAuth:      true,
			wantStatus:   http.StatusCreated,
			wantCalls:    1,
			wantReceiver: 2,
			wantContent:  "hello",
		},
		{
			name:       "未授权",
			body:       `{"receiver_id":2,"content":"hello"}`,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "非法 JSON",
			body:       `{"receiver_id":"bad"}`,
			hasAuth:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:         "收件人不存在",
			body:         `{"receiver_id":9,"content":"hello"}`,
			hasAuth:      true,
			serviceErr:   ErrRecipientNotFound,
			wantStatus:   http.StatusNotFound,
			wantCalls:    1,
			wantReceiver: 9,
			wantContent:  "hello",
		},
		{
			name:         "参数错误",
			body:         `{"receiver_id":1,"content":"hello"}`,
			hasAuth:      true,
			serviceErr:   ErrInvalidInput,
			wantStatus:   http.StatusBadRequest,
			wantCalls:    1,
			wantReceiver: 1,
			wantContent:  "hello",
		},
		{
			name:         "内部错误",
			body:         `{"receiver_id":2,"content":"hello"}`,
			hasAuth:      true,
			serviceErr:   errors.New("db down"),
			wantStatus:   http.StatusInternalServerError,
			wantCalls:    1,
			wantReceiver: 2,
			wantContent:  "hello",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			service := &fakeService{
				sendFunc: func(
					ctx context.Context,
					senderID, receiverID uint64,
					content string,
				) (*Item, error) {
					calls++
					if tt.serviceErr != nil {
						return nil, tt.serviceErr
					}
					return &Item{
						ID:         1,
						SenderID:   senderID,
						ReceiverID: receiverID,
						Content:    content,
					}, nil
				},
			}
			handler := NewHandler(service, newMessageDiscardLogger())
			engine := messageTestEngine(handler, tt.hasAuth)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if calls != tt.wantCalls {
				t.Fatalf("service calls = %d, want %d", calls, tt.wantCalls)
			}
			if tt.wantCalls > 0 {
				if service.lastUserID != 7 ||
					service.lastPeerID != tt.wantReceiver ||
					service.lastContent != tt.wantContent {
					t.Fatalf("unexpected service args: %+v", service)
				}
			}
		})
	}
}

func TestHandlerListAndRead(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		method     string
		path       string
		hasAuth    bool
		serviceErr error
		wantStatus int
		wantPeerID uint64
		wantCursor uint64
		wantSize   int
	}{
		{
			name:       "历史消息",
			method:     http.MethodGet,
			path:       "/api/v1/messages/2?cursor=30&page_size=10",
			hasAuth:    true,
			wantStatus: http.StatusOK,
			wantPeerID: 2,
			wantCursor: 30,
			wantSize:   10,
		},
		{
			name:       "标记已读",
			method:     http.MethodPost,
			path:       "/api/v1/messages/2/read",
			hasAuth:    true,
			wantStatus: http.StatusNoContent,
			wantPeerID: 2,
		},
		{
			name:       "非法 peer id",
			method:     http.MethodGet,
			path:       "/api/v1/messages/abc",
			hasAuth:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "未授权",
			method:     http.MethodGet,
			path:       "/api/v1/messages/2",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "内部错误",
			method:     http.MethodGet,
			path:       "/api/v1/messages/2",
			hasAuth:    true,
			serviceErr: errors.New("db down"),
			wantStatus: http.StatusInternalServerError,
			wantPeerID: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeService{
				listFunc: func(
					ctx context.Context,
					userID, peerID, beforeID uint64,
					pageSize int,
				) (*Page, error) {
					return nil, tt.serviceErr
				},
				markReadFunc: func(ctx context.Context, userID, peerID uint64) error {
					return tt.serviceErr
				},
			}
			handler := NewHandler(service, newMessageDiscardLogger())
			engine := messageTestEngine(handler, tt.hasAuth)
			req := httptest.NewRequest(tt.method, tt.path, nil)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d body=%s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.wantPeerID > 0 && service.lastPeerID != tt.wantPeerID {
				t.Fatalf("peer id = %d, want %d", service.lastPeerID, tt.wantPeerID)
			}
			if tt.method == http.MethodGet && tt.hasAuth && tt.wantStatus == http.StatusOK {
				if service.lastBeforeID != tt.wantCursor || service.lastPageSize != tt.wantSize {
					t.Fatalf("cursor=%d size=%d", service.lastBeforeID, service.lastPageSize)
				}
			}
		})
	}
}

func TestHandlerConversationsAndUnreadCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &fakeService{
		listConversationsFunc: func(ctx context.Context, userID uint64) (*ConversationList, error) {
			return &ConversationList{Items: []Conversation{{
				Peer: Peer{ID: 2, Username: "bob"},
			}}}, nil
		},
		unreadCountFunc: func(ctx context.Context, userID uint64) (int64, error) {
			return 3, nil
		},
	}
	handler := NewHandler(service, newMessageDiscardLogger())
	engine := messageTestEngine(handler, true)

	t.Run("会话列表", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/conversations", nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"username":"bob"`) {
			t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("未读数", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/unread-count", nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d", recorder.Code)
		}
		var body map[string]int64
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if body["count"] != 3 {
			t.Fatalf("count = %d, want 3", body["count"])
		}
	})
}

func TestHandlerStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	events := make(chan Item, 1)
	events <- Item{
		ID:         5,
		SenderID:   2,
		ReceiverID: 7,
		Content:    "hello",
		CreatedAt:  time.Now().UTC(),
	}
	unsubscribed := make(chan struct{}, 1)
	service := &fakeService{
		subscribeFunc: func(userID uint64) (<-chan Item, func()) {
			return events, func() { unsubscribed <- struct{}{} }
		},
	}
	handler := NewHandler(service, newMessageDiscardLogger())
	engine := messageTestEngine(handler, true)
	server := httptest.NewServer(engine)
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		server.URL+"/api/v1/messages/stream",
		nil,
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
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
		t.Fatalf("content type = %q", got)
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
	if eventName != "message" || !strings.Contains(data, `"content":"hello"`) {
		t.Fatalf("event=%q data=%q", eventName, data)
	}

	cancel()
	select {
	case <-unsubscribed:
	case <-time.After(time.Second):
		t.Fatal("expected stream subscription to be cancelled")
	}
}

func messageTestEngine(handler *Handler, hasAuth bool) *gin.Engine {
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		if hasAuth {
			ctx := authctx.WithUserID(c.Request.Context(), 7)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	})
	engine.POST("/api/v1/messages", handler.Send)
	engine.GET("/api/v1/messages/conversations", handler.Conversations)
	engine.GET("/api/v1/messages/unread-count", handler.UnreadCount)
	engine.GET("/api/v1/messages/stream", handler.Stream)
	engine.GET("/api/v1/messages/:peer_id", handler.List)
	engine.POST("/api/v1/messages/:peer_id/read", handler.MarkRead)
	return engine
}
