package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

const maxRequestIDLength = 128

// RequestID 为每个请求准备稳定的 Request ID。
//
// 如果可信上游已经传入了格式合法的请求头，就复用该值；否则由当前服务生成。
// 请求 ID 会同时写入：
// 1. request context，供业务逻辑和日志读取。
// 2. 响应头，方便客户端反馈问题。
func RequestID(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := strings.TrimSpace(c.GetHeader(requestid.Header))
		if !isValidRequestID(id) {
			var err error
			id, err = newRequestID()
			if err != nil {
				logger.Error("generate request id", "error", err)
				httpx.Abort(
					c,
					httpx.StatusCode(httpx.ErrorCodeInternal),
					httpx.ErrorCodeInternal,
					"internal server error",
				)
				return
			}
		}

		c.Request = c.Request.WithContext(requestid.WithContext(c.Request.Context(), id))
		c.Header(requestid.Header, id)
		c.Next()
	}
}

func isValidRequestID(id string) bool {
	if len(id) == 0 || len(id) > maxRequestIDLength {
		return false
	}

	for i := 0; i < len(id); i++ {
		ch := id[i]
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch >= 'A' && ch <= 'Z' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '-' || ch == '_' || ch == '.' || ch == ':' {
			continue
		}

		return false
	}

	return true
}

func newRequestID() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}

	return hex.EncodeToString(randomBytes), nil
}
