package middleware

import (
	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/requestid"
	"log/slog"

	"github.com/gin-gonic/gin"
)

// OptionalAuth 尝试解析请求中的 Bearer Token，但不强制要求登录。
//
// 规则：
// 1. 完全没有 Authorization Header：按匿名请求继续，context 中不写入 UserID。
// 2. Header 存在但格式错误、Token 无效或过期：返回 401，不降级为匿名。
// 3. Header 合法：把 UserID 写入 context，再继续请求。
//
// 这个中间件用于 GET /api/v1/videos/:id：
// 匿名用户也要能查看视频详情，但登录用户需要额外获得 is_liked_by 状态。
func OptionalAuth(tokens TokenParser, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 读取 Authorization Header
		authHeader := c.GetHeader("Authorization")

		// 2. Header 为空：纯匿名用户，直接放行
		if authHeader == "" {
			c.Next()
			return
		}

		// 3. Header 存在但格式错误（比如缺少 Bearer 前缀）
		rawToken, ok := parseBearerToken(authHeader)
		if !ok {
			abortUnauthorized(c)
			return
		}

		// 4. 调用 tokens.Parse 校验 Token
		userID, err := tokens.Parse(rawToken)
		if err != nil {
			// 5. Token 校验失败（过期/签名错误）：记录日志，返回 401
			logger.Warn(
				"optional auth: token parse failed",
				"request_id", requestid.FromContext(c.Request.Context()),
				"error", err,
			)
			abortUnauthorized(c)
			return
		}

		// 6. Token 合法：把 UserID 写入标准 context
		ctx := authctx.WithUserID(c.Request.Context(), userID)
		// 7. 更新 c.Request，把新 context 向后传递
		c.Request = c.Request.WithContext(ctx)

		// 8. 继续后续的中间件或 Handler
		c.Next()
	}
}
