package middleware

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

// Limiter 是限流中间件依赖的最小能力。
type Limiter interface {
	Allow(key string) bool
	RetryAfter() time.Duration
}

// RateLimitMetrics 是限流中间件需要的窄指标接口。
type RateLimitMetrics interface {
	IncRateLimited(scope string)
}

// RateLimitOptions 描述一层限流。
type RateLimitOptions struct {
	// Scope 只用于指标和日志标签，例如 "global" / "identity"。
	Scope string
	// Limiter 是具体的限流器，为 nil 时这一层不生效。
	Limiter Limiter
	// KeyFunc 决定用哪个维度限流。
	// 返回空字符串表示"这个请求没有可用的身份维度"，跳过这一层，
	// 由外层的全局桶兜底。
	KeyFunc func(*gin.Context) string
	// ExemptPaths 是不参与限流的路径（按路由模板匹配）。
	//
	// 探针必须豁免：否则额度被打满时健康检查会失败，容器被判不健康重启，
	// 反而制造出一次雪崩。
	ExemptPaths []string
	Metrics     RateLimitMetrics
	Logger      *slog.Logger
}

// RateLimit 返回一层令牌桶限流中间件。
//
// 位置建议放在 Recovery 之后、Auth 之前：这样 429 会被 AccessLog 和
// Metrics 记录到，同时它是挡在 JWT 解析之前的第一道 CPU 保护。
func RateLimit(options RateLimitOptions) gin.HandlerFunc {
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}

	exempt := make(map[string]struct{}, len(options.ExemptPaths))
	for _, path := range options.ExemptPaths {
		exempt[path] = struct{}{}
	}

	return func(c *gin.Context) {
		// FullPath 是路由模板（例如 /api/v1/videos/:id），
		// 在中间件里已经可用，并且天然不会被查询串影响。
		if _, ok := exempt[c.FullPath()]; ok {
			c.Next()
			return
		}
		if options.Limiter == nil {
			c.Next()
			return
		}

		key := ""
		if options.KeyFunc != nil {
			key = strings.TrimSpace(options.KeyFunc(c))
		}
		if key == "" {
			c.Next()
			return
		}

		if options.Limiter.Allow(key) {
			c.Next()
			return
		}

		if options.Metrics != nil {
			options.Metrics.IncRateLimited(options.Scope)
		}

		logger.Warn(
			"request rate limited",
			"request_id", requestid.FromContext(c.Request.Context()),
			"scope", options.Scope,
			"key", key,
			"method", c.Request.Method,
			"path", c.FullPath(),
		)

		c.Header("Retry-After", strconv.Itoa(retryAfterSeconds(options.Limiter)))
		httpx.Abort(
			c,
			http.StatusTooManyRequests,
			httpx.ErrorCodeRateLimited,
			"too many requests",
		)
	}
}

// retryAfterSeconds 把等待时间向上取整成秒，至少要 1 秒。
func retryAfterSeconds(limiter Limiter) int {
	seconds := int(math.Ceil(limiter.RetryAfter().Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}

// GlobalKey 让所有请求共用一个桶，用于保护整体吞吐。
func GlobalKey(*gin.Context) string {
	return "global"
}

// IdentityKey 返回按身份限流用的 key。
//
// 匿名请求返回空串，也就是跳过这一层。原因是当前部署下按 IP 拿不到可信来源：
// 客户端经 Docker 端口映射进来，容器看到的对端地址对所有人都是同一个
// （见路由层的 XFF 说明），按 IP 限流会退化成"全站共用一个桶"，
// 一个人就能把所有人的额度刷掉。匿名流量目前只靠全局桶兜底。
//
// 等客户端开始上报设备 ID 之后，这里可以补上
// device -> 兜底 IP 的降级链。
func IdentityKey(c *gin.Context) string {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok || userID == 0 {
		return ""
	}
	return "user:" + strconv.FormatUint(userID, 10)
}
