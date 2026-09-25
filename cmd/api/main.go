package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-feed-system/internal/health"
	"go-feed-system/internal/middleware"
	"go-feed-system/internal/router"
	"go-feed-system/internal/user"
	"go-feed-system/pkg/config"
	"go-feed-system/pkg/database"
	"go-feed-system/pkg/password"
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

	engine := router.New(router.Dependencies{
		Logger:         logger,
		HealthHandler:  health.NewHandler(version),
		UserHandler:    userHandler,
		AuthMiddleware: middleware.Auth(tokenManager, logger),
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

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.ListenAndServe()
	}()

	select {
	case err := <-errChan:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server failed to start: %w", err)
		}
	case <-ctx.Done():
		// 1. 立即调用 stop()，注销 NotifyContext 对信号的接管。
		// 此时，如果用户再按 Ctrl+C，系统会直接 kill 掉进程（默认行为），实现强制退出。
		// 注意：这里也解释了为什么外层的 defer stop() 不够，
		// 因为 defer 要等到 run() 函数返回才执行，而我们要在进入 Shutdown 循环前就恢复默认行为。
		stop()

		logger.Info("signal received, shutting down gracefully")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.HTTP.ShutdownTimeout)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}

	return nil
}
