package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/requestid"
)

// AccessLog 在请求结束后记录一次结构化访问日志。
//
// 它必须放在 Recovery 外层。这样即使后面的 handler 发生 panic，
// Recovery 写入 500 响应后控制权返回这里，访问日志仍能记录最终状态码。
func AccessLog(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		logger.Info(
			"http request",
			"request_id", requestid.FromContext(c.Request.Context()),
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"latency_ms", time.Since(start).Milliseconds(),
			"client_ip", c.ClientIP(),
		)
	}
}
