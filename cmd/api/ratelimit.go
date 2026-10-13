package main

import (
	"fmt"
	"log/slog"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/middleware"
	"go-feed-system/pkg/config"
	"go-feed-system/pkg/ratelimit"
)

// rateLimitExemptPaths 是不参与限流的路径。
//
// 探针必须豁免：额度打满导致健康检查失败，容器会被判不健康反复重启，
// 反而制造雪崩。/metrics 豁免是为了 Prometheus 抓取不受业务流量影响。
var rateLimitExemptPaths = []string{"/livez", "/readyz", "/metrics"}

// newRateLimitHandlers 按配置构造全局限流和身份限流两层中间件。
//
// 关闭限流时返回两个 nil，路由层会跳过挂载。
// 压测基线时要的就是这个状态：否则测到的是限流器的天花板。
func newRateLimitHandlers(
	cfg config.RateLimitConfig,
	metrics middleware.RateLimitMetrics,
	logger *slog.Logger,
) (gin.HandlerFunc, gin.HandlerFunc, error) {
	if logger == nil {
		logger = slog.Default()
	}

	if !cfg.Enabled {
		logger.Warn("rate limiting is disabled by configuration")
		return nil, nil, nil
	}

	globalLimiter, err := ratelimit.NewKeyedLimiter(ratelimit.Config{
		Burst:    cfg.Global.Burst,
		Interval: cfg.Global.Interval,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create global rate limiter: %w", err)
	}

	identityLimiter, err := ratelimit.NewKeyedLimiter(ratelimit.Config{
		Burst:    cfg.Identity.Burst,
		Interval: cfg.Identity.Interval,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create identity rate limiter: %w", err)
	}

	globalRateLimit := middleware.RateLimit(middleware.RateLimitOptions{
		Scope:       "global",
		Limiter:     globalLimiter,
		KeyFunc:     middleware.GlobalKey,
		ExemptPaths: rateLimitExemptPaths,
		Metrics:     metrics,
		Logger:      logger,
	})
	identityRateLimit := middleware.RateLimit(middleware.RateLimitOptions{
		Scope:       "identity",
		Limiter:     identityLimiter,
		KeyFunc:     middleware.IdentityKey,
		ExemptPaths: rateLimitExemptPaths,
		Metrics:     metrics,
		Logger:      logger,
	})

	// 启动时打一行额度，出问题时不用翻代码猜配置。
	logger.Info(
		"rate limiting enabled",
		"global_burst", cfg.Global.Burst,
		"global_interval", cfg.Global.Interval,
		"identity_burst", cfg.Identity.Burst,
		"identity_interval", cfg.Identity.Interval,
	)

	return globalRateLimit, identityRateLimit, nil
}
