package router

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/health"
	"go-feed-system/internal/middleware"
)

// New 组装 HTTP 路由和中间件。
//
// 当前使用手动依赖注入：logger 和 handler 都由 main 显式传入。
// 这个函数相当于 C++ 项目中的 LogicSystem 路由表，但不再使用全局单例。
func New(logger *slog.Logger, healthHandler *health.Handler) *gin.Engine {
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	engine.Use(middleware.RequestID(logger))
	engine.Use(middleware.AccessLog(logger))
	engine.Use(middleware.Recovery(logger))

	// 基础设施探针不放进 /api/v1，避免业务版本升级影响运维配置。
	engine.GET("/livez", healthHandler.Live)
	engine.GET("/readyz", healthHandler.Ready)

	// TODO(阶段 1)：在这里创建 /api/v1 路由组，并注册后续业务路由。
	//
	// v1 := engine.Group("/api/v1")
	// v1.POST("/users/register", userHandler.Register)

	return engine
}
