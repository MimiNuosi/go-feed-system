package router

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-feed-system/internal/health"
	"go-feed-system/internal/interaction"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/authctx"

	"github.com/gin-gonic/gin"
)

type fakeLikeRouteService struct {
	likeCalls   int
	unlikeCalls int
}

func (f *fakeLikeRouteService) Like(ctx context.Context, userID, videoID uint64) error {
	f.likeCalls++
	return nil
}

func (f *fakeLikeRouteService) Unlike(ctx context.Context, userID, videoID uint64) error {
	f.unlikeCalls++
	return nil
}

func newTestEngine(logger *slog.Logger, authMiddleware gin.HandlerFunc, likeService *fakeLikeRouteService) *gin.Engine {
	return New(Dependencies{
		Logger:         logger,
		HealthHandler:  health.NewHandler("test-version"),
		UserHandler:    user.NewHandler(nil),
		AuthMiddleware: authMiddleware,
		VideoHandler:   video.NewHandler(nil),
		FollowHandler:  interaction.NewFollowHandler(nil, logger),
		LikeHandler:    interaction.NewLikeHandler(likeService, logger),
	})
}

func TestMethodNotAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := newTestEngine(logger, func(c *gin.Context) { c.Next() }, &fakeLikeRouteService{})

	t.Run("POST /livez should return 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/livez", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, w.Code)
		}
		if w.Header().Get("Allow") != "GET" {
			t.Errorf("expected 'Allow' header 'GET', got '%s'", w.Header().Get("Allow"))
		}
	})
}

func TestLikeRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	likeService := &fakeLikeRouteService{}
	authMiddleware := func(c *gin.Context) {
		ctx := authctx.WithUserID(c.Request.Context(), 1)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	engine := newTestEngine(logger, authMiddleware, likeService)

	tests := []struct {
		name      string
		method    string
		wantCalls int
	}{
		{
			name:      "POST like route",
			method:    http.MethodPost,
			wantCalls: 1,
		},
		{
			name:      "DELETE like route",
			method:    http.MethodDelete,
			wantCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			likeService.likeCalls = 0
			likeService.unlikeCalls = 0

			req := httptest.NewRequest(tt.method, "/api/v1/videos/2/like", nil)
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
			}
			if tt.method == http.MethodPost && likeService.likeCalls != tt.wantCalls {
				t.Fatalf("expected Like called %d times, got %d", tt.wantCalls, likeService.likeCalls)
			}
			if tt.method == http.MethodDelete && likeService.unlikeCalls != tt.wantCalls {
				t.Fatalf("expected Unlike called %d times, got %d", tt.wantCalls, likeService.unlikeCalls)
			}
		})
	}
}
