package router

import (
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/avatar"
	"go-feed-system/internal/feed"
	"go-feed-system/internal/health"
	"go-feed-system/internal/hot"
	"go-feed-system/internal/interaction"
	"go-feed-system/internal/message"
	"go-feed-system/internal/middleware"
	"go-feed-system/internal/notification"
	"go-feed-system/internal/profile"
	"go-feed-system/internal/upload"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/internal/web"
)

type Dependencies struct {
	Logger                 *slog.Logger
	Metrics                middleware.HTTPMetrics
	MetricsHandler         http.Handler
	GlobalRateLimit        gin.HandlerFunc
	IdentityRateLimit      gin.HandlerFunc
	HealthHandler          *health.Handler
	AvatarHandler          *avatar.Handler
	UserHandler            *user.Handler
	AuthMiddleware         gin.HandlerFunc
	OptionalAuthMiddleware gin.HandlerFunc
	VideoHandler           *video.Handler
	FollowHandler          *interaction.FollowHandler
	LikeHandler            *interaction.LikeHandler
	CommentHandler         *interaction.CommentHandler
	NotificationHandler    *notification.Handler
	MessageHandler         *message.Handler
	ProfileHandler         *profile.Handler
	UploadHandler          *upload.Handler
	FeedHandler            *feed.Handler
	HotHandler             *hot.Handler
}

// New 组装 HTTP 路由和中间件。
//
// 当前使用手动依赖注入：logger 和 handler 都由 main 显式传入。
// 这个函数相当于 C++ 项目中的 LogicSystem 路由表，但不再使用全局单例。
func New(deps Dependencies) *gin.Engine {
	engine := gin.New()
	engine.HandleMethodNotAllowed = true

	// 不信任任何代理转发的客户端 IP。
	//
	// X-Forwarded-For 是客户端可以随意伪造的头部，而 gin 默认信任全部代理，
	// 等于让客户端自己申报 IP。当前部署里客户端经 Docker 端口映射进来，
	// 容器看到的对端固定是网桥网关地址，一旦把私有网段填进可信列表，
	// 就等于把不可信的客户端当成了可信代理。
	//
	// 等以后真的在前面部署受控网关（并且 api 不再直接暴露端口）时，
	// 再把网关地址填进来，并确认网关是覆盖而不是追加 XFF。
	// nil 不会返回 error：只有传入非法 CIDR 才会失败。
	_ = engine.SetTrustedProxies(nil)

	engine.Use(middleware.RequestID(deps.Logger))
	engine.Use(middleware.AccessLog(deps.Logger))
	engine.Use(middleware.Metrics(deps.Metrics))
	engine.Use(middleware.Recovery(deps.Logger))

	// 全局限流放在 Recovery 之后、业务路由之前：
	// 429 仍然会被 AccessLog 和 Metrics 记录，同时还挡在鉴权之前，
	// 避免把 CPU 花在无效请求的 JWT 解析上。
	if deps.GlobalRateLimit != nil {
		engine.Use(deps.GlobalRateLimit)
	}

	// 身份限流的键是 userID，而 userID 是鉴权中间件写进 context 的，
	// 所以它不能像全局限流那样挂在最外层：
	// 挂在鉴权之前时 context 里还没有 userID，限流器只会看到空 key，
	// 那一层就等于没生效。正确做法是挂在鉴权之后。
	identityRateLimit := deps.IdentityRateLimit
	if identityRateLimit == nil {
		identityRateLimit = func(c *gin.Context) { c.Next() }
	}

	// 基础设施探针不放进 /api/v1，避免业务版本升级影响运维配置。
	engine.GET("/livez", deps.HealthHandler.Live)
	engine.GET("/readyz", deps.HealthHandler.Ready)

	// 前端与 API 同源，所以不需要配 CORS。
	// dist/ 是 Vue 3 + Vite 的构建产物，用 go:embed 打进二进制。
	dist := web.Dist()
	indexHTML, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		// 构建产物在编译期固定，这里失败说明发布流程坏了，尽早暴露。
		panic("router: embedded frontend is missing index.html: " + err.Error())
	}
	fileServer := http.FileServer(http.FS(dist))

	engine.GET("/", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	// 旧版单文件控制台保留为兜底入口。
	engine.GET("/legacy", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", web.LegacyHTML())
	})

	engine.GET("/assets/*filepath", gin.WrapH(fileServer))

	if deps.MetricsHandler != nil {
		engine.GET("/metrics", gin.WrapH(deps.MetricsHandler))
	}
	engine.GET(
		"/api/v1/videos/:id",
		deps.OptionalAuthMiddleware,
		identityRateLimit,
		deps.VideoHandler.GetDetail,
	)
	engine.GET("/api/v1/videos/:id/file", deps.VideoHandler.File)

	v1 := engine.Group("/api/v1")

	// 1. 公开接口（无需鉴权）
	v1.POST("/auth/register", deps.UserHandler.Register)
	v1.POST("/auth/login", deps.UserHandler.Login)
	v1.POST("/auth/refresh", deps.UserHandler.Refresh)
	v1.GET("/videos/:id/comments", deps.CommentHandler.List)
	v1.GET(
		"/users/:id/videos",
		deps.OptionalAuthMiddleware,
		identityRateLimit,
		deps.VideoHandler.ListByAuthor,
	)
	// 用户主页：聚合资料、粉丝/关注列表（公开可读）。
	v1.GET(
		"/users/:id/profile",
		deps.OptionalAuthMiddleware,
		identityRateLimit,
		deps.ProfileHandler.GetProfile,
	)
	v1.GET("/users/:id/avatar", deps.AvatarHandler.Get)
	v1.GET(
		"/users/:id/followers",
		deps.OptionalAuthMiddleware,
		identityRateLimit,
		deps.ProfileHandler.ListFollowers,
	)
	v1.GET(
		"/users/:id/following",
		deps.OptionalAuthMiddleware,
		identityRateLimit,
		deps.ProfileHandler.ListFollowing,
	)
	v1.POST(
		"/videos/:id/view",
		deps.OptionalAuthMiddleware,
		identityRateLimit,
		deps.HotHandler.RecordView,
	)
	v1.GET(
		"/feed/hot",
		deps.OptionalAuthMiddleware,
		identityRateLimit,
		deps.HotHandler.ListHot,
	)
	// 最新：不需要关注关系，是陌生人发现新内容的入口
	v1.GET(
		"/feed/latest",
		deps.OptionalAuthMiddleware,
		identityRateLimit,
		deps.FeedHandler.ListLatest,
	)

	// 2. 需要鉴权的接口组
	auth := v1.Group("")
	auth.Use(deps.AuthMiddleware) // 整个组统一挂载鉴权中间件
	auth.Use(identityRateLimit)   // 身份限流必须在鉴权之后
	{
		auth.GET("/users/me", deps.UserHandler.Me)
		auth.POST("/users/me/avatar", deps.AvatarHandler.Upload)
		auth.POST("/auth/logout", deps.UserHandler.Logout)
		auth.POST("/users/me/password", deps.UserHandler.ChangePassword)
		auth.GET("/users/me/liked-videos", deps.ProfileHandler.ListLikedVideos)
		auth.GET("/notifications", deps.NotificationHandler.List)
		auth.GET("/notifications/unread-count", deps.NotificationHandler.UnreadCount)
		auth.POST("/notifications/read-all", deps.NotificationHandler.MarkAllRead)
		auth.GET("/notifications/stream", deps.NotificationHandler.Stream)

		// 私信：静态路径放在 :peer_id 之前，避免被动态参数吞掉。
		auth.POST("/messages", deps.MessageHandler.Send)
		auth.GET("/messages/conversations", deps.MessageHandler.Conversations)
		auth.GET("/messages/unread-count", deps.MessageHandler.UnreadCount)
		auth.GET("/messages/stream", deps.MessageHandler.Stream)
		auth.GET("/messages/:peer_id", deps.MessageHandler.List)
		auth.POST("/messages/:peer_id/read", deps.MessageHandler.MarkRead)

		auth.POST("/videos", deps.VideoHandler.Upload)
		auth.POST("/videos/chunk/init", deps.UploadHandler.Init)
		auth.POST("/videos/chunk/upload", deps.UploadHandler.UploadChunk)
		auth.GET("/videos/chunk/status", deps.UploadHandler.Status)
		auth.POST("/videos/chunk/complete", deps.UploadHandler.Complete)

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

	// Vue 前端用 history 路由：刷新 /video/1、/login 这类地址要回退到 index.html。
	engine.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/api" || strings.HasPrefix(path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{"code": "NOT_FOUND", "message": "route not found"},
			})
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
	})

	return engine
}
