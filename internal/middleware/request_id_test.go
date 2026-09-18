package middleware

import (
	"go-feed-system/pkg/requestid"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDMiddlewareGeneratesUniqueIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	engine := gin.New()
	engine.Use(RequestID(logger))

	engine.GET("/test", func(c *gin.Context) {
		id := requestid.FromContext(c.Request.Context())
		c.String(http.StatusOK, id)
	})

	t.Run("without request ID", func(t *testing.T) {
		// 第一次请求
		req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
		w1 := httptest.NewRecorder()
		engine.ServeHTTP(w1, req1)

		id1 := w1.Header().Get("X-Request-ID")
		if id1 == "" {
			t.Error("expected X-Request-ID header to be set")
		}

		// 第二次请求
		req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
		w2 := httptest.NewRecorder()
		engine.ServeHTTP(w2, req2)

		id2 := w2.Header().Get("X-Request-ID")
		if id2 == "" {
			t.Error("expected X-Request-ID header to be set")
		}

		// 核心断言：两个随机生成的 ID 不能相同
		if id1 == id2 {
			t.Errorf("expected different request IDs, but both are '%s'", id1)
		}
	})
}

func TestRequestIDMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	engine := gin.New()
	engine.Use(RequestID(logger))

	engine.GET("/test", func(c *gin.Context) {
		id := requestid.FromContext(c.Request.Context())
		c.String(http.StatusOK, id)
	})

	tests := []struct {
		name          string
		requestID     string
		wantGenerated bool
	}{
		{
			name:      "valid request ID",
			requestID: "valid-request-id",
		},
		{
			name:          "empty request ID",
			requestID:     "",
			wantGenerated: true,
		},
		{
			name:          "invalid request ID",
			requestID:     "bad id!",
			wantGenerated: true,
		},
		{
			name:          "request ID too long",
			requestID:     strings.Repeat("a", maxRequestIDLength+1),
			wantGenerated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.requestID != "" {
				req.Header.Set(requestid.Header, tt.requestID)
			}
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, w.Code)
			}

			headerID := w.Header().Get(requestid.Header)
			bodyID := w.Body.String()

			if tt.wantGenerated {
				// 期望服务器重新生成的情况（空、非法、超长）
				// 1. 新生成的 ID 不能为空
				if headerID == "" || bodyID == "" {
					t.Fatalf("expected generated ID, got empty")
				}
				// 2. 新生成的 ID 不能等于传入的非法 ID（如果有）
				if tt.requestID != "" && headerID == tt.requestID {
					t.Errorf("expected new generated ID, got original invalid ID")
				}
			} else {
				// 期望复用上游传入 ID 的情况
				if headerID != tt.requestID {
					t.Errorf("expected header ID %s, got %s", tt.requestID, headerID)
				}
				if bodyID != tt.requestID {
					t.Errorf("expected body ID %s, got %s", tt.requestID, bodyID)
				}
			}
		})
	}
}
