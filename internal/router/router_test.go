package router

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-feed-system/internal/health"
	"go-feed-system/internal/interaction"
	"go-feed-system/internal/middleware"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/authctx"

	"github.com/gin-gonic/gin"
)

type fakeLikeRouteService struct {
	likeCalls         int
	unlikeCalls       int
	getLikeStateCalls int
	lastViewerID      uint64
	lastVideoID       uint64
}

func (f *fakeLikeRouteService) Like(ctx context.Context, userID, videoID uint64) error {
	f.likeCalls++
	return nil
}

func (f *fakeLikeRouteService) Unlike(ctx context.Context, userID, videoID uint64) error {
	f.unlikeCalls++
	return nil
}

func (f *fakeLikeRouteService) GetLikeState(ctx context.Context, viewerID, videoID uint64) (int64, bool, error) {
	f.getLikeStateCalls++
	f.lastViewerID = viewerID
	f.lastVideoID = videoID
	return 7, false, nil
}

type fakeVideoRouteService struct{}

func (f *fakeVideoRouteService) Upload(ctx context.Context, input video.UploadInput) (*video.Video, error) {
	return &video.Video{ID: 1}, nil
}

func (f *fakeVideoRouteService) GetByID(ctx context.Context, id uint64) (*video.Video, error) {
	return &video.Video{ID: id}, nil
}

func (f *fakeVideoRouteService) GetDetail(ctx context.Context, id uint64) (*video.VideoDetail, error) {
	return &video.VideoDetail{ID: id, Title: "test video"}, nil
}

func (f *fakeVideoRouteService) OpenFile(ctx context.Context, id uint64) (io.ReadSeekCloser, *video.Video, error) {
	return nil, nil, errors.New("not implemented")
}

type fakeRouteTokenParser struct {
	userID    uint64
	err       error
	callCount int
}

func (f *fakeRouteTokenParser) Parse(raw string) (uint64, error) {
	f.callCount++
	return f.userID, f.err
}

func newTestEngine(
	logger *slog.Logger,
	authMiddleware gin.HandlerFunc,
	optionalAuthMiddleware gin.HandlerFunc,
	likeService *fakeLikeRouteService,
) *gin.Engine {
	return New(Dependencies{
		Logger:                 logger,
		HealthHandler:          health.NewHandler("test-version"),
		UserHandler:            user.NewHandler(nil),
		AuthMiddleware:         authMiddleware,
		OptionalAuthMiddleware: optionalAuthMiddleware,
		VideoHandler:           video.NewHandler(&fakeVideoRouteService{}, likeService),
		FollowHandler:          interaction.NewFollowHandler(nil, logger),
		LikeHandler:            interaction.NewLikeHandler(likeService, logger),
	})
}

func TestMethodNotAllowed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := newTestEngine(
		logger,
		func(c *gin.Context) { c.Next() },
		func(c *gin.Context) { c.Next() },
		&fakeLikeRouteService{},
	)

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
	engine := newTestEngine(
		logger,
		authMiddleware,
		func(c *gin.Context) { c.Next() },
		likeService,
	)

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

func TestVideoDetailOptionalAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	tests := []struct {
		name            string
		authHeader      string
		parserUserID    uint64
		parserErr       error
		wantStatus      int
		wantParserCalls int
		wantLikeCalls   int
		wantViewerID    uint64
	}{
		{
			name:            "匿名访问不调用 TokenParser",
			wantStatus:      http.StatusOK,
			wantParserCalls: 0,
			wantLikeCalls:   1,
			wantViewerID:    0,
		},
		{
			name:            "合法 Token 注入用户 ID",
			authHeader:      "Bearer valid-token",
			parserUserID:    123,
			wantStatus:      http.StatusOK,
			wantParserCalls: 1,
			wantLikeCalls:   1,
			wantViewerID:    123,
		},
		{
			name:            "非法 Token 返回 401",
			authHeader:      "Bearer invalid-token",
			parserErr:       errors.New("invalid token"),
			wantStatus:      http.StatusUnauthorized,
			wantParserCalls: 1,
			wantLikeCalls:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := &fakeRouteTokenParser{
				userID: tt.parserUserID,
				err:    tt.parserErr,
			}
			likeService := &fakeLikeRouteService{}
			engine := newTestEngine(
				logger,
				func(c *gin.Context) { c.Next() },
				middleware.OptionalAuth(parser, logger),
				likeService,
			)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/2", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, recorder.Code)
			}
			if parser.callCount != tt.wantParserCalls {
				t.Errorf("expected parser called %d times, got %d", tt.wantParserCalls, parser.callCount)
			}
			if likeService.getLikeStateCalls != tt.wantLikeCalls {
				t.Errorf("expected GetLikeState called %d times, got %d", tt.wantLikeCalls, likeService.getLikeStateCalls)
			}
			if tt.wantLikeCalls > 0 && likeService.lastViewerID != tt.wantViewerID {
				t.Errorf("expected viewerID %d, got %d", tt.wantViewerID, likeService.lastViewerID)
			}
		})
	}
}
