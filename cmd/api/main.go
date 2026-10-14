package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-feed-system/internal/feed"
	"go-feed-system/internal/health"
	"go-feed-system/internal/hot"
	"go-feed-system/internal/interaction"
	"go-feed-system/internal/middleware"
	"go-feed-system/internal/outbox"
	"go-feed-system/internal/router"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/config"
	"go-feed-system/pkg/database"
	"go-feed-system/pkg/metrics"
	"go-feed-system/pkg/password"
	"go-feed-system/pkg/redis"
	"go-feed-system/pkg/storage/local"
	"go-feed-system/pkg/token"

	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
)

const version = "dev"

func main() {
	_ = godotenv.Load() // 自动加载同目录下的 .env
	if err := run(); err != nil {
		slog.Error("server stopped with error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	appMetrics := metrics.New()

	startupCtx, cancelStartup := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelStartup()

	db, err := database.Open(startupCtx, database.MySQLConfig{
		DSN:             cfg.MySQL.DSN,
		MaxOpenConns:    cfg.MySQL.MaxOpenConns,
		MaxIdleConns:    cfg.MySQL.MaxIdleConns,
		ConnMaxLifetime: cfg.MySQL.ConnMaxLifetime,
	})
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() {
		if err := database.Close(db); err != nil {
			logger.Error("close database", "error", err)
		}
	}()

	redisClient, err := redis.Open(startupCtx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("open redis: %w", err)
	}
	defer func() {
		if err := redis.Close(redisClient); err != nil {
			logger.Error("close redis", "error", err)
		}
	}()
	redisInbox := feed.NewRedisInbox(redisClient, feed.DefaultInboxMaxLen)
	bigAuthorCache := feed.NewRedisBigAuthorCache(
		redisClient,
		cfg.Feed.BigAuthorCacheTTL,
	)

	hasher, err := password.NewBcryptHasher(bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("create password hasher: %w", err)
	}

	tokenManager, err := token.NewManager(
		cfg.Auth.JWTSecret,
		cfg.Auth.JWTIssuer,
		cfg.Auth.JWTTTL,
	)
	if err != nil {
		return fmt.Errorf("create token manager: %w", err)
	}

	userRepository := user.NewGORMRepository(db)
	userService := user.NewService(userRepository, hasher, tokenManager)
	userHandler := user.NewHandler(userService)

	videoStorage, err := local.NewStorage(cfg.Storage.LocalDir)
	if err != nil {
		return fmt.Errorf("create video storage: %w", err)
	}

	outboxRepository := outbox.NewGORMRepository(db)
	videoRepository := video.NewGORMRepository(db, outboxRepository)
	videoService := video.NewService(videoRepository, videoStorage, userService, logger)

	followRepository := interaction.NewGORMFollowRepository(db)
	followService := interaction.NewFollowService(
		followRepository,
		userService,
		bigAuthorCache,
		logger,
	)
	followHandler := interaction.NewFollowHandler(followService, logger)

	messaging, err := newFeedMessaging(
		cfg,
		outboxRepository,
		redisInbox,
		followRepository,
		appMetrics,
		logger,
	)
	if err != nil {
		return fmt.Errorf("setup feed messaging: %w", err)
	}
	defer func() {
		if err := messaging.Close(); err != nil {
			logger.Error("close feed messaging", "error", err)
		}
	}()

	hotRepository := hot.NewRedisRepository(redisClient, logger)
	hotService := hot.NewService(hotRepository, logger)
	hotScoreRecorder := hot.NewScoreRecorder(hotRepository, logger)

	// 浏览量回写：把 Redis 里累计的浏览增量刷进 MySQL。
	//
	// 这里把 videoRepository 当"浏览量写入"能力传进去，用的是 hot 包自己
	// 声明的窄接口 ViewCountWriter，而不是 video.Repository。
	viewWritebackWorker := hot.NewViewWritebackWorker(
		hot.NewViewWriteback(
			hotRepository,
			videoRepository,
			hot.DefaultViewWritebackBatchSize,
			logger,
			appMetrics,
		),
		hot.DefaultViewWritebackInterval,
		logger,
	)

	likeRepository := interaction.NewGORMLikeRepository(db)
	likeService := interaction.NewLikeService(
		likeRepository,
		videoService,
		hotScoreRecorder,
	)
	likeHandler := interaction.NewLikeHandler(likeService, logger)

	commentRepository := interaction.NewGORMCommentRepository(db)
	commentService := interaction.NewCommentService(
		commentRepository,
		videoService,
		userService,
		hotScoreRecorder,
	)
	commentHandler := interaction.NewCommentHandler(commentService, logger)

	videoHandler := video.NewHandler(videoService, likeService)

	feedRepository := feed.NewGORMRepository(db)
	feedService := feed.NewService(
		feedRepository,
		userService,
		likeService,
		redisInbox,
		bigAuthorCache,
		cfg.Feed.FanoutFollowerThreshold,
		logger,
	)
	feedHandler := feed.NewHandler(feedService, logger)

	hotHandler := hot.NewHandler(
		hotService,
		hotService,
		videoRepository,
		userService,
		likeService,
		logger,
		// 热榜读模型的 TTL：把"每页 3 条 SQL 补作者和点赞"从每请求降到每秒一次，
		// 代价是点赞数最多陈旧 1 秒（详情接口不走这个缓存）。
		hot.DefaultItemCacheTTL,
	)

	// 限流：额度放在进程内存里（理由见 pkg/ratelimit 的包注释），
	// 额度本身来自配置，压测时可以临时放开。
	globalRateLimit, identityRateLimit, err := newRateLimitHandlers(
		cfg.RateLimit,
		appMetrics,
		logger,
	)
	if err != nil {
		return fmt.Errorf("setup rate limiting: %w", err)
	}

	engine := router.New(router.Dependencies{
		Logger:            logger,
		Metrics:           appMetrics,
		MetricsHandler:    appMetrics.Handler(),
		GlobalRateLimit:   globalRateLimit,
		IdentityRateLimit: identityRateLimit,
		HealthHandler: health.NewHandler(
			version,
			logger,
			health.Dependency{
				Name:     "mysql",
				Critical: true,
				Check: func(ctx context.Context) error {
					sqlDB, err := db.DB()
					if err != nil {
						return err
					}
					return sqlDB.PingContext(ctx)
				},
			},
			health.Dependency{
				Name:     "redis",
				Critical: false,
				Check: func(ctx context.Context) error {
					return redisClient.Ping(ctx).Err()
				},
			},
			health.Dependency{
				Name:     "rabbitmq",
				Critical: false,
				Check:    messaging.Ready,
			},
		),
		UserHandler:            userHandler,
		AuthMiddleware:         middleware.Auth(tokenManager, logger),
		OptionalAuthMiddleware: middleware.OptionalAuth(tokenManager, logger),
		VideoHandler:           videoHandler,
		FollowHandler:          followHandler,
		LikeHandler:            likeHandler,
		CommentHandler:         commentHandler,
		FeedHandler:            feedHandler,
		HotHandler:             hotHandler,
	})

	server := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           engine,
		ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
	}

	logger.Info("http server starting", "address", cfg.HTTP.Addr, "version", version)

	// 1. 使用 signal.NotifyContext 监听 Ctrl+C 和 SIGTERM。
	// 2. 在一个 goroutine 中调用 server.ListenAndServe，并把结果写入带缓冲的 error channel。
	// 3. 在主 goroutine 中使用 select，等待“服务启动失败”或“退出信号”。
	// 4. 收到退出信号后，调用 server.Shutdown，并给它一个有截止时间的 context。
	// 5. 把 http.ErrServerClosed 当作正常关闭，不要包装成启动错误。
	//
	// - error channel 为什么建议带缓冲？
	//	因为不带缓冲的channel 在写入时会阻塞，主goroutine可能会在收到退出信号时提前返回，
	//  跑ListenAndServe的goroutine就会阻塞在往channel里发数据这一步，导致 goroutine 泄漏
	// - Shutdown 和 Close 的差别是什么？
	//  Shutdown 会优雅地关闭服务器，等待正在处理的请求完成后再关闭，而 Close 会立即关闭服务器，不管是否有请求正在处理。
	// - 如果收到第二次 Ctrl+C，程序应如何表现？
	//  如果收到第二次 Ctrl+C，程序应立即退出，不再等待正在处理的请求完成。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	serverErrCh := make(chan error, 1)
	go func() {
		serverErrCh <- server.ListenAndServe()
	}()

	messagingErrCh := make(chan error, 1)
	go func() {
		messagingErrCh <- messaging.Run(runCtx)
	}()

	viewWritebackErrCh := make(chan error, 1)
	go func() {
		viewWritebackErrCh <- viewWritebackWorker.Run(runCtx)
	}()

	var runErr error
	messagingDone := false
	viewWritebackDone := false

	select {
	case err := <-serverErrCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("server failed to start: %w", err)
		}
	case err := <-messagingErrCh:
		messagingDone = true
		if err != nil {
			runErr = fmt.Errorf("feed messaging stopped: %w", err)
		} else {
			runErr = errors.New("feed messaging stopped unexpectedly")
		}
	case err := <-viewWritebackErrCh:
		viewWritebackDone = true
		if err != nil {
			runErr = fmt.Errorf("view writeback worker stopped: %w", err)
		} else {
			runErr = errors.New("view writeback worker stopped unexpectedly")
		}
	case <-ctx.Done():
		// 恢复第二次 Ctrl+C 的默认强制退出行为。
		stop()
		logger.Info("signal received, shutting down gracefully")
	}

	cancelRun()

	shutdownCtx, cancelShutdown := context.WithTimeout(
		context.Background(),
		cfg.HTTP.ShutdownTimeout,
	)
	defer cancelShutdown()

	if err := server.Shutdown(shutdownCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown server: %w", err)
	}

	if !messagingDone {
		select {
		case err := <-messagingErrCh:
			if err != nil && runErr == nil {
				runErr = fmt.Errorf("stop feed messaging: %w", err)
			}
		case <-shutdownCtx.Done():
			if runErr == nil {
				runErr = fmt.Errorf("stop feed messaging: %w", shutdownCtx.Err())
			}
		}
	}

	if !viewWritebackDone {
		// 单独给后台 worker 一份等待预算，避免和上面的等待共用同一个 deadline，
		// 否则前一个等超时后，这里会立刻失败。
		workerStopCtx, cancelWorkerStop := context.WithTimeout(
			context.Background(),
			cfg.HTTP.ShutdownTimeout,
		)
		defer cancelWorkerStop()

		select {
		case err := <-viewWritebackErrCh:
			if err != nil && runErr == nil {
				runErr = fmt.Errorf("stop view writeback worker: %w", err)
			}
		case <-workerStopCtx.Done():
			if runErr == nil {
				runErr = fmt.Errorf("stop view writeback worker: %w", workerStopCtx.Err())
			}
		}
	}

	return runErr
}
