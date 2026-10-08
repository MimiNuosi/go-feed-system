package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
)

// HTTPMetrics 是 HTTP 中间件需要的窄指标接口。
type HTTPMetrics interface {
	IncHTTPInFlight(method string)
	DecHTTPInFlight(method string)
	ObserveHTTPRequest(method, route string, status int, duration time.Duration)
}

// Metrics 记录请求数量、状态码、处理耗时和当前并发请求数。
//
// route 使用 Gin 匹配出的路由模板，例如 /api/v1/videos/:id，
// 避免把具体视频 ID 当成标签，造成指标基数失控。
func Metrics(metrics HTTPMetrics) gin.HandlerFunc {
	if metrics == nil {
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return func(c *gin.Context) {
		start := time.Now()
		method := c.Request.Method
		metrics.IncHTTPInFlight(method)

		defer func() {
			metrics.DecHTTPInFlight(method)

			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			metrics.ObserveHTTPRequest(
				method,
				route,
				c.Writer.Status(),
				time.Since(start),
			)
		}()

		c.Next()
	}
}
