package middleware

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
)

type fakeLimiter struct {
	allow      bool
	retryAfter time.Duration
	keys       []string
}

func (f *fakeLimiter) Allow(key string) bool {
	f.keys = append(f.keys, key)
	return f.allow
}

func (f *fakeLimiter) RetryAfter() time.Duration {
	if f.retryAfter <= 0 {
		return time.Second
	}
	return f.retryAfter
}

type fakeRateLimitMetrics struct {
	scopes []string
}

func (m *fakeRateLimitMetrics) IncRateLimited(scope string) {
	m.scopes = append(m.scopes, scope)
}

func TestRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		allow          bool
		retryAfter     time.Duration
		keyFunc        func(*gin.Context) string
		path           string
		exemptPaths    []string
		wantStatus     int
		wantCode       httpx.ErrorCode
		wantRetryAfter string
		wantKeys       []string
		wantLimited    int
		wantHandler    bool
	}{
		{
			name:        "放行时交给下游 Handler",
			allow:       true,
			keyFunc:     func(*gin.Context) string { return "user:1" },
			path:        "/probe",
			wantStatus:  http.StatusOK,
			wantKeys:    []string{"user:1"},
			wantHandler: true,
		},
		{
			name:           "拒绝时返回 429 与统一错误码",
			allow:          false,
			retryAfter:     2 * time.Second,
			keyFunc:        func(*gin.Context) string { return "user:1" },
			path:           "/probe",
			wantStatus:     http.StatusTooManyRequests,
			wantCode:       httpx.ErrorCodeRateLimited,
			wantRetryAfter: "2",
			wantKeys:       []string{"user:1"},
			wantLimited:    1,
		},
		{
			name:        "没有身份维度时跳过这一层",
			allow:       false,
			keyFunc:     func(*gin.Context) string { return "" },
			path:        "/probe",
			wantStatus:  http.StatusOK,
			wantHandler: true,
		},
		{
			name:        "豁免路径不消耗额度",
			allow:       false,
			keyFunc:     GlobalKey,
			path:        "/livez",
			exemptPaths: []string{"/livez"},
			wantStatus:  http.StatusOK,
			wantHandler: true,
		},
		{
			name:           "Retry-After 向上取整且至少 1 秒",
			allow:          false,
			retryAfter:     200 * time.Millisecond,
			keyFunc:        GlobalKey,
			path:           "/probe",
			wantStatus:     http.StatusTooManyRequests,
			wantCode:       httpx.ErrorCodeRateLimited,
			wantRetryAfter: "1",
			wantKeys:       []string{"global"},
			wantLimited:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := &fakeLimiter{allow: tt.allow, retryAfter: tt.retryAfter}
			metrics := &fakeRateLimitMetrics{}
			handlerCalled := false

			engine := gin.New()
			engine.Use(RateLimit(RateLimitOptions{
				Scope:       "test",
				Limiter:     limiter,
				KeyFunc:     tt.keyFunc,
				ExemptPaths: tt.exemptPaths,
				Metrics:     metrics,
				Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
			}))
			engine.GET(tt.path, func(c *gin.Context) {
				handlerCalled = true
				c.Status(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d body=%s",
					tt.wantStatus,
					w.Code,
					w.Body.String(),
				)
			}
			if handlerCalled != tt.wantHandler {
				t.Errorf("handler called = %v, want %v", handlerCalled, tt.wantHandler)
			}
			if !reflect.DeepEqual(limiter.keys, tt.wantKeys) {
				t.Errorf("limiter keys = %v, want %v", limiter.keys, tt.wantKeys)
			}
			if len(metrics.scopes) != tt.wantLimited {
				t.Errorf(
					"rate limited metric = %d, want %d",
					len(metrics.scopes),
					tt.wantLimited,
				)
			}
			if tt.wantRetryAfter != "" {
				if got := w.Header().Get("Retry-After"); got != tt.wantRetryAfter {
					t.Errorf("Retry-After = %q, want %q", got, tt.wantRetryAfter)
				}
			}
			if tt.wantCode != "" {
				var resp httpx.ErrorResponse
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if resp.Error.Code != tt.wantCode {
					t.Errorf("error code = %q, want %q", resp.Error.Code, tt.wantCode)
				}
			}
		})
	}
}

func TestIdentityKey(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		userID uint64
		want   string
	}{
		{name: "登录用户按 userID 维度", userID: 42, want: "user:42"},
		{name: "匿名用户没有身份维度", userID: 0, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			if tt.userID > 0 {
				ctx := authctx.WithUserID(req.Context(), tt.userID)
				req = req.WithContext(ctx)
			}

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			if got := IdentityKey(c); got != tt.want {
				t.Fatalf("IdentityKey = %q, want %q", got, tt.want)
			}
		})
	}
}
