package middleware

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/token"
)

func TestOptionalAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name            string
		authHeader      string
		mockUserID      uint64
		mockErr         error
		wantStatusCode  int
		wantUserID      uint64
		wantParserCalls int
		wantHeader      string // 检查 WWW-Authenticate
	}{
		// 1. 匿名访问：没有任何 Authorization 头
		{
			name:            "无 Authorization，匿名放行 200",
			authHeader:      "",
			wantStatusCode:  200,
			wantUserID:      0, // context 里不应该有 userID
			wantParserCalls: 0, // 核心断言：不能调用 Parser
		},
		// 2. 合法 Token
		{
			name:            "合法 Token，放行并注入 UserID 200",
			authHeader:      "Bearer valid-token",
			mockUserID:      123,
			mockErr:         nil,
			wantStatusCode:  200,
			wantUserID:      123,
			wantParserCalls: 1,
		},
		// 3. Header 格式错误
		{
			name:            "格式错误（缺少 Bearer），返回 401",
			authHeader:      "Basic abc",
			wantStatusCode:  401,
			wantUserID:      0,
			wantParserCalls: 0, // 格式错误，不应该走到 Parser
			wantHeader:      "Bearer",
		},
		{
			name:            "只有 Bearer 没有 Token，返回 401",
			authHeader:      "Bearer",
			wantStatusCode:  401,
			wantUserID:      0,
			wantParserCalls: 0,
			wantHeader:      "Bearer",
		},
		// 4. Token 校验失败（过期/签名错误）
		{
			name:            "Token 过期，返回 401",
			authHeader:      "Bearer expired-token",
			mockErr:         token.ErrTokenExpired,
			wantStatusCode:  401,
			wantUserID:      0,
			wantParserCalls: 1,
			wantHeader:      "Bearer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 Fake
			fakeParser := &fakeTokenParser{
				userID: tt.mockUserID,
				err:    tt.mockErr,
			}

			engine := gin.New()
			engine.Use(RequestID(logger))
			engine.Use(OptionalAuth(fakeParser, logger))

			// 核心：测试 Handler 必须兼容匿名访问，不能强行返回 401！
			engine.GET("/test", func(c *gin.Context) {
				id, ok := authctx.UserID(c.Request.Context())
				if !ok {
					// 匿名用户，返回 200 并标记为 anonymous
					c.JSON(http.StatusOK, gin.H{"user_id": uint64(0), "is_anonymous": true})
					return
				}
				// 登录用户，返回真实的 userID
				c.JSON(http.StatusOK, gin.H{"user_id": id, "is_anonymous": false})
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			// 1. 断言状态码
			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d", tt.wantStatusCode, w.Code)
			}

			// 2. 断言 WWW-Authenticate 头（401 时才有）
			if tt.wantHeader != "" {
				if got := w.Header().Get("WWW-Authenticate"); got != tt.wantHeader {
					t.Errorf("expected header %s, got %s", tt.wantHeader, got)
				}
			}

			// 3. 断言 Parser 被调用的次数
			if fakeParser.CallCount != tt.wantParserCalls {
				t.Errorf("expected Parser called %d times, got %d", tt.wantParserCalls, fakeParser.CallCount)
			}

			// 4. 断言响应体
			if tt.wantStatusCode == 200 {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to decode body: %v", err)
				}
				// 判断 user_id
				gotUserID := uint64(resp["user_id"].(float64))
				if gotUserID != tt.wantUserID {
					t.Errorf("expected userID %d, got %d", tt.wantUserID, gotUserID)
				}
				// 判断是否为匿名
				gotAnon := resp["is_anonymous"].(bool)
				wantAnon := tt.wantUserID == 0
				if gotAnon != wantAnon {
					t.Errorf("expected is_anonymous %v, got %v", wantAnon, gotAnon)
				}
			}
		})
	}
}
