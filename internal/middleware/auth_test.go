package middleware

import (
	"encoding/json"
	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/token"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 定义 Fake TokenParser
type fakeTokenParser struct {
	userID   uint64
	err      error
	gotToken string
}

func (f *fakeTokenParser) Parse(raw string) (uint64, error) {
	f.gotToken = raw
	return f.userID, f.err
}

func TestAuth(t *testing.T) {
	// 初始化 Engine 和 Fake
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name           string
		authHeader     string
		mockUserID     uint64
		wantUserID     uint64
		mockErr        error
		wantStatusCode int
		wantHeader     string // 检查 WWW-Authenticate
		wantToken      string
	}{
		// 失败用例必须填 wantHeader
		{name: "缺少 Authorization", authHeader: "", wantStatusCode: 401, wantHeader: "Bearer"},
		{name: "只有 Bearer", authHeader: "Bearer", wantStatusCode: 401, wantHeader: "Bearer"},
		{name: "Basic 认证", authHeader: "Basic abc", wantStatusCode: 401, wantHeader: "Bearer"},
		{name: "Bearer 后为空", authHeader: "Bearer ", wantStatusCode: 401, wantHeader: "Bearer"},
		{name: "Token 含有多个空格", authHeader: "Bearer valid token", wantStatusCode: 401, wantHeader: "Bearer"},
		{name: "Token 为空但包含 Tab", authHeader: "Bearer\t", wantStatusCode: 401, wantHeader: "Bearer"},
		{name: "Token 已过期", authHeader: "Bearer expired-token", mockErr: token.ErrTokenExpired, wantStatusCode: http.StatusUnauthorized, wantHeader: "Bearer"},

		// 成功用例，不需要 Header 检查，但需要 UserID
		{name: "合法 Header 且验证成功", authHeader: "Bearer valid-token", mockUserID: 123, wantUserID: 123, mockErr: nil, wantStatusCode: 200},
		{name: "大小写不敏感的 Bearer", authHeader: "bearer valid-token", mockUserID: 123, wantUserID: 123, wantStatusCode: 200, wantToken: "valid-token"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeParser := &fakeTokenParser{
				userID: tt.mockUserID,
				err:    tt.mockErr,
			}

			engine := gin.New()
			engine.Use(RequestID(logger))
			engine.Use(Auth(fakeParser, logger))

			engine.GET("/test", func(c *gin.Context) {
				id, ok := authctx.UserID(c.Request.Context())
				if !ok {
					c.String(http.StatusUnauthorized, "no user id in context")
					return
				}
				c.JSON(http.StatusOK, gin.H{"user_id": id})
			})

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d", tt.wantStatusCode, w.Code)
			}

			// 验证传递给 Parser 的 Token 是否正确
			if tt.wantToken != "" && fakeParser.gotToken != tt.wantToken {
				t.Errorf("expected parser to receive token %q, got %q", tt.wantToken, fakeParser.gotToken)
			}

			if tt.wantHeader != "" {
				if got := w.Header().Get("WWW-Authenticate"); got != tt.wantHeader {
					t.Errorf("expected header %s, got %s", tt.wantHeader, got)
				}
			}

			// 验证统一错误响应体(401)
			if tt.wantStatusCode == http.StatusUnauthorized {
				var errResp map[string]map[string]string // 嵌套 map 解析 JSON
				if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
					t.Fatalf("failed to decode error response: %v", err)
				}
				if errResp["error"]["code"] != "UNAUTHORIZED" {
					t.Errorf("expected error code UNAUTHORIZED, got %s", errResp["error"]["code"])
				}
			}

			//正常状态码
			if tt.wantStatusCode == 200 {
				var resp map[string]uint64
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to decode body: %v", err)
				}
				if resp["user_id"] != tt.wantUserID {
					t.Errorf("expected userID %d, got %d", tt.wantUserID, resp["user_id"])
				}
			}
		})
	}
}
