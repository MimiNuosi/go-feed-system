package router

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/health"
	"go-feed-system/internal/middleware"
	"go-feed-system/internal/user"
)

type Dependencies struct {
	Logger         *slog.Logger
	HealthHandler  *health.Handler
	UserHandler    *user.Handler
	AuthMiddleware gin.HandlerFunc
}

// New 组装 HTTP 路由和中间件。
//
// 当前使用手动依赖注入：logger 和 handler 都由 main 显式传入。
// 这个函数相当于 C++ 项目中的 LogicSystem 路由表，但不再使用全局单例。
func New(deps Dependencies) *gin.Engine {
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	engine.Use(middleware.RequestID(deps.Logger))
	engine.Use(middleware.AccessLog(deps.Logger))
	engine.Use(middleware.Recovery(deps.Logger))

	// 基础设施探针不放进 /api/v1，避免业务版本升级影响运维配置。
	engine.GET("/livez", deps.HealthHandler.Live)
	engine.GET("/readyz", deps.HealthHandler.Ready)

	v1 := engine.Group("/api/v1")
	v1.POST("/auth/register", deps.UserHandler.Register)
	v1.POST("/auth/login", deps.UserHandler.Login)
	v1.GET("/users/me", deps.AuthMiddleware, deps.UserHandler.Me)

	return engine
}
