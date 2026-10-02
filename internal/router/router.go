package router

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/feed"
	"go-feed-system/internal/health"
	"go-feed-system/internal/interaction"
	"go-feed-system/internal/middleware"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
)

type Dependencies struct {
	Logger                 *slog.Logger
	HealthHandler          *health.Handler
	UserHandler            *user.Handler
	AuthMiddleware         gin.HandlerFunc
	OptionalAuthMiddleware gin.HandlerFunc
	VideoHandler           *video.Handler
	FollowHandler          *interaction.FollowHandler
	LikeHandler            *interaction.LikeHandler
	CommentHandler         *interaction.CommentHandler
	FeedHandler            *feed.Handler
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
	engine.GET(
		"/api/v1/videos/:id",
		deps.OptionalAuthMiddleware,
		deps.VideoHandler.GetDetail,
	)
	engine.GET("/api/v1/videos/:id/file", deps.VideoHandler.File)

	v1 := engine.Group("/api/v1")

	// 1. 公开接口（无需鉴权）
	v1.POST("/auth/register", deps.UserHandler.Register)
	v1.POST("/auth/login", deps.UserHandler.Login)
	v1.GET("/videos/:id/comments", deps.CommentHandler.List)

	// 2. 需要鉴权的接口组
	auth := v1.Group("")
	auth.Use(deps.AuthMiddleware) // 整个组统一挂载鉴权中间件
	{
		auth.GET("/users/me", deps.UserHandler.Me)
		auth.POST("/videos", deps.VideoHandler.Upload)

		// 关注模块
		auth.POST("/users/:id/follow", deps.FollowHandler.Follow)
		auth.DELETE("/users/:id/follow", deps.FollowHandler.Unfollow)
		auth.GET("/users/:id/follow/status", deps.FollowHandler.Status)

		// 互动模块
		auth.POST("/videos/:id/like", deps.LikeHandler.Like)
		auth.DELETE("/videos/:id/like", deps.LikeHandler.Unlike)
		auth.POST("/videos/:id/comments", deps.CommentHandler.Create)
		auth.DELETE("/comments/:id", deps.CommentHandler.Delete)

		// Feed 模块
		auth.GET("/feed/following", deps.FeedHandler.ListFollowing)
	}

	return engine
}
