package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/health"
	"go-feed-system/internal/interaction"
	"go-feed-system/internal/router"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
)

func TestHandlerLive(t *testing.T) {
	t.Parallel()

	engine := newTestEngine()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/livez", nil)

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp["status"])
	}
	if resp["check"] != "live" {
		t.Errorf("expected check 'live', got '%s'", resp["check"])
	}
	if resp["version"] != "test-version" {
		t.Errorf("expected version 'test-version', got '%s'", resp["version"])
	}
}

func TestHandlerReady(t *testing.T) {
	t.Parallel()

	engine := newTestEngine()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	engine.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}

	var resp map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got '%s'", resp["status"])
	}
	if resp["check"] != "ready" {
		t.Errorf("expected check 'ready', got '%s'", resp["check"])
	}
	if resp["version"] != "test-version" {
		t.Errorf("expected version 'test-version', got '%s'", resp["version"])
	}
}

func TestHandlerReadyWithDependencies(t *testing.T) {
	tests := []struct {
		name       string
		mysqlErr   error
		redisErr   error
		rabbitErr  error
		wantStatus int
		wantHealth string
		wantRedis  string
		wantRabbit string
	}{
		{
			name:       "所有依赖正常",
			wantStatus: http.StatusOK,
			wantHealth: "ok",
			wantRedis:  "up",
			wantRabbit: "up",
		},
		{
			name:       "可选依赖失败时降级",
			redisErr:   errors.New("redis down"),
			wantStatus: http.StatusOK,
			wantHealth: "degraded",
			wantRedis:  "down",
			wantRabbit: "up",
		},
		{
			name:       "核心依赖失败时不可用",
			mysqlErr:   errors.New("mysql down"),
			wantStatus: http.StatusServiceUnavailable,
			wantHealth: "unavailable",
			wantRedis:  "up",
			wantRabbit: "up",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler := health.NewHandler(
				"test-version",
				logger,
				health.Dependency{
					Name:     "mysql",
					Critical: true,
					Check: func(ctx context.Context) error {
						return tt.mysqlErr
					},
				},
				health.Dependency{
					Name:     "redis",
					Critical: false,
					Check: func(ctx context.Context) error {
						return tt.redisErr
					},
				},
				health.Dependency{
					Name:     "rabbitmq",
					Critical: false,
					Check: func(ctx context.Context) error {
						return tt.rabbitErr
					},
				},
			)

			engine := gin.New()
			engine.GET("/readyz", handler.Ready)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			engine.ServeHTTP(recorder, request)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, recorder.Code)
			}

			var response struct {
				Status       string `json:"status"`
				Dependencies []struct {
					Name   string `json:"name"`
					Status string `json:"status"`
				} `json:"dependencies"`
			}
			if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Status != tt.wantHealth {
				t.Errorf("expected health %q, got %q", tt.wantHealth, response.Status)
			}

			statusByName := make(map[string]string, len(response.Dependencies))
			for _, dependency := range response.Dependencies {
				statusByName[dependency.Name] = dependency.Status
			}
			if statusByName["redis"] != tt.wantRedis {
				t.Errorf("expected redis %q, got %q", tt.wantRedis, statusByName["redis"])
			}
			if statusByName["rabbitmq"] != tt.wantRabbit {
				t.Errorf("expected rabbitmq %q, got %q", tt.wantRabbit, statusByName["rabbitmq"])
			}
		})
	}
}

func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return router.New(router.Dependencies{
		Logger:         logger,
		HealthHandler:  health.NewHandler("test-version", logger),
		UserHandler:    user.NewHandler(nil),
		AuthMiddleware: func(c *gin.Context) { c.Next() },
		VideoHandler:   video.NewHandler(nil, nil),
		CommentHandler: interaction.NewCommentHandler(nil, logger),
	})
}
