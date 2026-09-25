package middleware

import (
	"log/slog"
	"strings"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

type TokenParser interface {
	Parse(raw string) (uint64, error)
}

// Auth 校验 Bearer Token，并把用户 ID 放入标准 context。
//
// 对外所有认证失败都返回相同的 401，具体失败原因只写入内部日志。
// 业务层只依赖后来的 request context，不依赖 gin.Context。
func Auth(tokens TokenParser, logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawToken, ok := parseBearerToken(c.GetHeader("Authorization"))
		if !ok {
			abortUnauthorized(c)
			return
		}

		userID, err := tokens.Parse(rawToken)
		if err != nil {
			logger.Warn(
				"authenticate request",
				"request_id", requestid.FromContext(c.Request.Context()),
				"error", err,
			)
			abortUnauthorized(c)
			return
		}

		ctx := authctx.WithUserID(c.Request.Context(), userID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

func parseBearerToken(header string) (string, bool) {
	// 1. 使用 strings.Fields 按任意空白字符分割（处理多个空格）
	parts := strings.Fields(header)

	// 2. 必须恰好两部分：["Bearer", "<token>"]
	if len(parts) != 2 {
		return "", false
	}

	// 3. 比较前缀，忽略大小写（strings.EqualFold）
	if !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}

	// 4. Token 不能为空
	if parts[1] == "" {
		return "", false
	}

	return parts[1], true
}

func abortUnauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", "Bearer")
	httpx.Abort(
		c,
		httpx.StatusCode(httpx.ErrorCodeUnauthorized),
		httpx.ErrorCodeUnauthorized,
		"unauthorized",
	)
}
