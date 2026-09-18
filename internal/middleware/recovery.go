package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

// Recovery 捕获当前 HTTP 请求处理链中未预期的 panic。
//
// 它只负责兜底，不能替代正常错误处理。数据库失败、参数错误等可预期问题
// 仍然必须通过 error 返回。
//
// 注意：它不能捕获其他 goroutine 中未处理的 panic。后台任务必须自行
// 处理错误，或者使用明确的 recover 边界。
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			// http.ErrAbortHandler 是 net/http 用来主动中断连接的约定。
			// 收到它时应继续向上传递，不要伪装成普通业务错误。
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}

			logger.Error(
				"panic recovered",
				"request_id", requestid.FromContext(c.Request.Context()),
				"panic", fmt.Sprintf("%v", recovered),
				"stack", string(debug.Stack()),
			)

			if c.Writer.Written() {
				c.Abort()
				return
			}

			httpx.Abort(
				c,
				httpx.StatusCode(httpx.ErrorCodeInternal),
				httpx.ErrorCodeInternal,
				"internal server error",
			)
		}()

		c.Next()
	}
}
