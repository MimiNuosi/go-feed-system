package router

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go-feed-system/internal/feed"
	"go-feed-system/internal/health"
	"go-feed-system/internal/hot"
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

func (f *fakeLikeRouteService) CountByVideoIDs(
	ctx context.Context,
	videoIDs []uint64,
) (map[uint64]int64, error) {
	return map[uint64]int64{}, nil
}

func (f *fakeLikeRouteService) LikedVideoIDsByUser(
	ctx context.Context,
	userID uint64,
	videoIDs []uint64,
) (map[uint64]bool, error) {
	return map[uint64]bool{}, nil
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

func (f *fakeVideoRouteService) GetAuthorInfo(ctx context.Context, userID uint64) (video.AuthorInfo, error) {
	return video.AuthorInfo{ID: userID, Username: "author"}, nil
}

func (f *fakeVideoRouteService) ListByAuthor(
	ctx context.Context,
	authorID uint64,
	cursor string,
	pageSize int,
) (*video.AuthorVideoPage, error) {
	return &video.AuthorVideoPage{
		Videos: []video.Video{{ID: 7, AuthorID: authorID, Title: "my video"}},
	}, nil
}

func (f *fakeVideoRouteService) OpenFile(ctx context.Context, id uint64) (io.ReadSeekCloser, *video.Video, error) {
	return nil, nil, errors.New("not implemented")
}

type fakeRouteTokenParser struct {
	userID    uint64
	err       error
	callCount int
}

type fakeHotRouteService struct {
	calls         int
	videoID       uint64
	viewerKey     string
	listHotCalls  int
	listHotVideos []hot.RankedVideo
	readyVideos   []video.Video
	userMap       map[uint64]*user.User
	likeCounts    map[uint64]int64
}

func (f *fakeHotRouteService) RecordView(
	ctx context.Context,
	videoID uint64,
	viewerKey string,
	occurredAt time.Time,
) (bool, error) {
	f.calls++
	f.videoID = videoID
	f.viewerKey = viewerKey
	return true, nil
}

func (f *fakeHotRouteService) ListHot(
	ctx context.Context,
	window hot.Window,
	at time.Time,
	cursor *hot.Cursor,
	pageSize int,
) (*hot.Page, error) {
	f.listHotCalls++
	return &hot.Page{Videos: f.listHotVideos}, nil
}

func (f *fakeHotRouteService) ListReadyByIDs(
	ctx context.Context,
	ids []uint64,
) ([]video.Video, error) {
	return f.readyVideos, nil
}

func (f *fakeHotRouteService) GetByIDs(
	ctx context.Context,
	ids []uint64,
) (map[uint64]*user.User, error) {
	return f.userMap, nil
}

func (f *fakeHotRouteService) CountByVideoIDs(
	ctx context.Context,
	videoIDs []uint64,
) (map[uint64]int64, error) {
	return f.likeCounts, nil
}

func (f *fakeHotRouteService) LikedVideoIDsByUser(
	ctx context.Context,
	userID uint64,
	videoIDs []uint64,
) (map[uint64]bool, error) {
	return nil, nil
}

func (f *fakeRouteTokenParser) Parse(raw string) (uint64, error) {
	f.callCount++
	return f.userID, f.err
}

type fakeCommentRouteService struct {
	createCalls int
	listCalls   int
	deleteCalls int
}

func (f *fakeCommentRouteService) Create(
	ctx context.Context,
	userID, videoID uint64,
	content string,
) (*interaction.Comment, error) {
	f.createCalls++
	return &interaction.Comment{
		ID:      1,
		VideoID: videoID,
		UserID:  userID,
		Content: content,
	}, nil
}

func (f *fakeCommentRouteService) List(
	ctx context.Context,
	videoID, cursor uint64,
	pageSize int,
) (*interaction.CommentPage, error) {
	f.listCalls++
	return &interaction.CommentPage{Items: []interaction.CommentItem{}}, nil
}

func (f *fakeCommentRouteService) Delete(ctx context.Context, userID, commentID uint64) error {
	f.deleteCalls++
	return nil
}

type fakeFeedService struct {
	ListFollowingFunc func(ctx context.Context, userID uint64, cursor *feed.Cursor, pageSize int) (*feed.Page, error)
	ListLatestFunc    func(ctx context.Context, viewerID uint64, cursor *feed.Cursor, pageSize int) (*feed.Page, error)
	CallCount         int
	LastUserID        uint64
	LastCursor        *feed.Cursor
	LastPageSize      int
}

func (f *fakeFeedService) ListLatest(
	ctx context.Context,
	viewerID uint64,
	cursor *feed.Cursor,
	pageSize int,
) (*feed.Page, error) {
	f.CallCount++
	f.LastUserID = viewerID
	f.LastCursor = cursor
	f.LastPageSize = pageSize
	if f.ListLatestFunc != nil {
		return f.ListLatestFunc(ctx, viewerID, cursor, pageSize)
	}
	return &feed.Page{Items: []feed.Item{}, HasMore: false}, nil
}

func (f *fakeFeedService) ListFollowing(ctx context.Context, userID uint64, cursor *feed.Cursor, pageSize int) (*feed.Page, error) {
	f.CallCount++
	f.LastUserID = userID
	f.LastCursor = cursor
	f.LastPageSize = pageSize
	if f.ListFollowingFunc != nil {
		return f.ListFollowingFunc(ctx, userID, cursor, pageSize)
	}
	return &feed.Page{Items: []feed.Item{}, HasMore: false}, nil
}

func newTestEngine(
	logger *slog.Logger,
	authMiddleware gin.HandlerFunc,
	optionalAuthMiddleware gin.HandlerFunc,
	likeService *fakeLikeRouteService,
	commentService *fakeCommentRouteService,
) *gin.Engine {
	hotService := &fakeHotRouteService{}
	return New(Dependencies{
		Logger:                 logger,
		HealthHandler:          health.NewHandler("test-version", logger),
		UserHandler:            user.NewHandler(nil),
		AuthMiddleware:         authMiddleware,
		OptionalAuthMiddleware: optionalAuthMiddleware,
		VideoHandler:           video.NewHandler(&fakeVideoRouteService{}, likeService),
		FollowHandler:          interaction.NewFollowHandler(nil, logger),
		LikeHandler:            interaction.NewLikeHandler(likeService, logger),
		CommentHandler:         interaction.NewCommentHandler(commentService, logger),
		HotHandler: hot.NewHandler(
			hotService,
			hotService,
			hotService,
			hotService,
			hotService,
			logger,
			0, // 这组用例走完整路径，不开读模型缓存
		),
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
		&fakeCommentRouteService{},
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

func TestMetricsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	engine := New(Dependencies{
		Logger:        logger,
		HealthHandler: health.NewHandler("test-version", logger),
		MetricsHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("metrics-body"))
		}),
	})

	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", response.Code)
	}
	if response.Body.String() != "metrics-body" {
		t.Fatalf("expected metrics body, got %q", response.Body.String())
	}
}

func TestHotViewRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	t.Run("匿名请求记录 IP viewer key", func(t *testing.T) {
		hotService := &fakeHotRouteService{}
		engine := New(Dependencies{
			Logger:        logger,
			HealthHandler: health.NewHandler("test", logger),
			HotHandler: hot.NewHandler(
				hotService,
				hotService,
				hotService,
				hotService,
				hotService,
				logger,
				0,
			),
			AuthMiddleware:         func(c *gin.Context) { c.Next() },
			OptionalAuthMiddleware: func(c *gin.Context) { c.Next() },
		})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/12/view", nil)
		req.RemoteAddr = "203.0.113.7:12345"
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", recorder.Code)
		}
		if hotService.calls != 1 || hotService.videoID != 12 {
			t.Fatalf("unexpected hot service call: %+v", hotService)
		}
		if !strings.HasPrefix(hotService.viewerKey, "ip:") {
			t.Fatalf("expected IP viewer key, got %q", hotService.viewerKey)
		}
	})

	t.Run("登录请求使用 user viewer key", func(t *testing.T) {
		hotService := &fakeHotRouteService{}
		optionalAuth := func(c *gin.Context) {
			ctx := authctx.WithUserID(c.Request.Context(), 7)
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		}
		engine := New(Dependencies{
			Logger:        logger,
			HealthHandler: health.NewHandler("test", logger),
			HotHandler: hot.NewHandler(
				hotService,
				hotService,
				hotService,
				hotService,
				hotService,
				logger,
				0,
			),
			AuthMiddleware:         func(c *gin.Context) { c.Next() },
			OptionalAuthMiddleware: optionalAuth,
		})

		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/12/view", nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", recorder.Code)
		}
		if hotService.viewerKey != "user:7" {
			t.Fatalf("expected user viewer key, got %q", hotService.viewerKey)
		}
	})
}

func TestHotFeedRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hotService := &fakeHotRouteService{
		listHotVideos: []hot.RankedVideo{{VideoID: 3, Score: 5}},
		readyVideos: []video.Video{
			{
				ID:          3,
				AuthorID:    10,
				Title:       "hot video",
				Description: "desc",
				ContentType: "video/mp4",
				CreatedAt:   time.Now(),
				Status:      video.StatusReady,
			},
		},
		userMap: map[uint64]*user.User{
			10: {ID: 10, Username: "author"},
		},
		likeCounts: map[uint64]int64{3: 7},
	}

	engine := New(Dependencies{
		Logger:        logger,
		HealthHandler: health.NewHandler("test", logger),
		HotHandler: hot.NewHandler(
			hotService,
			hotService,
			hotService,
			hotService,
			hotService,
			logger,
			0,
		),
		AuthMiddleware:         func(c *gin.Context) { c.Next() },
		OptionalAuthMiddleware: func(c *gin.Context) { c.Next() },
	})

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/feed/hot?window=1h&page_size=10",
		nil,
	)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if hotService.listHotCalls != 1 {
		t.Fatalf("expected one ListHot call, got %d", hotService.listHotCalls)
	}
	if !strings.Contains(recorder.Body.String(), `"id":3`) {
		t.Fatalf("expected ranked video in body, got %s", recorder.Body.String())
	}
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
		&fakeCommentRouteService{},
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
				&fakeCommentRouteService{},
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

func TestCommentRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	commentService := &fakeCommentRouteService{}
	authMiddleware := func(c *gin.Context) {
		ctx := authctx.WithUserID(c.Request.Context(), 1)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
	engine := newTestEngine(
		logger,
		authMiddleware,
		func(c *gin.Context) { c.Next() },
		&fakeLikeRouteService{},
		commentService,
	)

	t.Run("public list route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/2/comments", nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
		}
		if commentService.listCalls != 1 {
			t.Fatalf("expected List called once, got %d", commentService.listCalls)
		}
	})

	t.Run("create comment route", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/videos/2/comments",
			strings.NewReader(`{"content":"hello"}`),
		)
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusCreated {
			t.Fatalf("expected status %d, got %d", http.StatusCreated, recorder.Code)
		}
		if commentService.createCalls != 1 {
			t.Fatalf("expected Create called once, got %d", commentService.createCalls)
		}
	})

	t.Run("delete comment route", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/2", nil)
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, req)

		if recorder.Code != http.StatusNoContent {
			t.Fatalf("expected status %d, got %d", http.StatusNoContent, recorder.Code)
		}
		if commentService.deleteCalls != 1 {
			t.Fatalf("expected Delete called once, got %d", commentService.deleteCalls)
		}
	})
}

func TestRouter_FeedFollowing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	fakeFeedSvc := &fakeFeedService{
		ListFollowingFunc: func(ctx context.Context, userID uint64, cursor *feed.Cursor, pageSize int) (*feed.Page, error) {
			return &feed.Page{
				Items:      []feed.Item{{ID: 1, Title: "test video"}},
				NextCursor: "cursor-123",
				HasMore:    true,
			}, nil
		},
	}
	feedHandler := feed.NewHandler(fakeFeedSvc, logger)

	t.Run("未登录访问 Feed 返回 401", func(t *testing.T) {
		engine := New(Dependencies{
			Logger:                 logger,
			HealthHandler:          health.NewHandler("test", logger),
			AuthMiddleware:         middleware.Auth(nil, logger),
			OptionalAuthMiddleware: middleware.OptionalAuth(nil, logger),
			FeedHandler:            feedHandler,
		})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/feed/following", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("登录后请求能到达 Handler 并返回 200", func(t *testing.T) {
		authMiddleware := func(c *gin.Context) {
			ctx := authctx.WithUserID(c.Request.Context(), 1)
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		}
		engine := New(Dependencies{
			Logger:                 logger,
			HealthHandler:          health.NewHandler("test", logger),
			AuthMiddleware:         authMiddleware,
			OptionalAuthMiddleware: func(c *gin.Context) { c.Next() },
			FeedHandler:            feedHandler,
		})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/feed/following?page_size=10", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. body=%s", w.Code, w.Body.String())
		}
		if fakeFeedSvc.CallCount != 1 {
			t.Fatalf("expected FeedService called once, got %d", fakeFeedSvc.CallCount)
		}
		if fakeFeedSvc.LastUserID != 1 {
			t.Errorf("expected userID 1, got %d", fakeFeedSvc.LastUserID)
		}
		if fakeFeedSvc.LastPageSize != 10 {
			t.Errorf("expected pageSize 10, got %d", fakeFeedSvc.LastPageSize)
		}
	})
}

// TestNew_IgnoresForwardedFor 验证路由层关闭了 XFF 采信。
//
// 客户端 IP 被用于匿名浏览去重，如果采信客户端自带的 X-Forwarded-For，
// 攻击者换个头部值就能伪造出无数个"新观众"，把任意视频刷上热榜。
func TestNew_IgnoresForwardedFor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	noop := func(c *gin.Context) { c.Next() }

	engine := New(Dependencies{
		Logger:                 logger,
		HealthHandler:          health.NewHandler("test", logger),
		AuthMiddleware:         noop,
		OptionalAuthMiddleware: noop,
	})

	// 借一条探针路由把 c.ClientIP() 暴露出来，方便断言。
	engine.GET("/__probe/client-ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	req := httptest.NewRequest(http.MethodGet, "/__probe/client-ip", nil)
	req.RemoteAddr = "203.0.113.7:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got := w.Body.String(); got != "203.0.113.7" {
		t.Fatalf("X-Forwarded-For 必须被忽略: expected 203.0.113.7, got %q", got)
	}
}

// alwaysDenyLimiter 模拟额度已经用光。
// TestNew_ServesEmbeddedConsole 验证内嵌的最小前端能从根路径打开。
// TestRouter_ListVideosByAuthor 验证 /users/:id/videos 这条路由确实挂上了。
// TestRouter_LatestFeed 验证 /feed/latest 不需要登录就能访问。
func TestRouter_LatestFeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	feedSvc := &fakeFeedService{}
	engine := New(Dependencies{
		Logger:                 logger,
		HealthHandler:          health.NewHandler("test", logger),
		OptionalAuthMiddleware: func(c *gin.Context) { c.Next() },
		FeedHandler:            feed.NewHandler(feedSvc, logger),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/feed/latest?page_size=5", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if feedSvc.CallCount != 1 {
		t.Fatalf("expected the service to be called once, got %d", feedSvc.CallCount)
	}
	if feedSvc.LastUserID != 0 {
		t.Errorf("expected anonymous viewer ID 0, got %d", feedSvc.LastUserID)
	}
	if feedSvc.LastPageSize != 5 {
		t.Errorf("expected page_size 5, got %d", feedSvc.LastPageSize)
	}
}

func TestRouter_ListVideosByAuthor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	videoSvc := &fakeVideoRouteService{}
	engine := New(Dependencies{
		Logger:                 logger,
		HealthHandler:          health.NewHandler("test", logger),
		OptionalAuthMiddleware: func(c *gin.Context) { c.Next() },
		VideoHandler:           video.NewHandler(videoSvc, &fakeLikeRouteService{}),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/7/videos", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "my video") {
		t.Fatalf("expected the author's video in the body, got %s", w.Body.String())
	}
}

func TestNew_ServesEmbeddedConsole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	engine := New(Dependencies{
		Logger:        logger,
		HealthHandler: health.NewHandler("test", logger),
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("expected an HTML content type, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), "Feed 流控制台") {
		t.Fatal("expected the embedded console page")
	}
}

type alwaysDenyLimiter struct {
	calls int
}

func (l *alwaysDenyLimiter) Allow(key string) bool {
	l.calls++
	return false
}

func (l *alwaysDenyLimiter) RetryAfter() time.Duration {
	return time.Second
}

// TestNew_AppliesRateLimitAndExemptsProbes 验证限流确实挂在请求链路上，
// 同时探针被豁免：额度打满时健康检查仍然通过，否则容器会被判不健康重启。
func TestNew_AppliesRateLimitAndExemptsProbes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	limiter := &alwaysDenyLimiter{}
	rateLimit := middleware.RateLimit(middleware.RateLimitOptions{
		Scope:       "global",
		Limiter:     limiter,
		KeyFunc:     middleware.GlobalKey,
		ExemptPaths: []string{"/livez", "/readyz", "/metrics"},
		Logger:      logger,
	})

	engine := New(Dependencies{
		Logger:          logger,
		HealthHandler:   health.NewHandler("test", logger),
		GlobalRateLimit: rateLimit,
	})

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{name: "探针豁免限流", path: "/livez", wantStatus: http.StatusOK},
		{
			name:       "业务路由被限流",
			path:       "/api/v1/videos/1/file",
			wantStatus: http.StatusTooManyRequests,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d body=%s",
					tt.wantStatus,
					w.Code,
					w.Body.String(),
				)
			}
		})
	}

	// 只有业务路由那一次真正问过限流器。
	if limiter.calls != 1 {
		t.Fatalf("expected limiter to be consulted once, got %d", limiter.calls)
	}
}

// recordingDenyLimiter 记录收到的限流键，并一律拒绝。
type recordingDenyLimiter struct {
	keys []string
}

func (l *recordingDenyLimiter) Allow(key string) bool {
	l.keys = append(l.keys, key)
	return false
}

func (l *recordingDenyLimiter) RetryAfter() time.Duration {
	return time.Second
}

// TestNew_IdentityRateLimitSeesAuthenticatedUser 验证身份限流挂在鉴权之后。
//
// 限流键是 userID，而 userID 是鉴权中间件写进 context 的。
// 如果限流挂在鉴权之前，它只会看到空 key 然后跳过这一层 ——
// 这个测试就是用来卡住这个顺序的。
func TestNew_IdentityRateLimitSeesAuthenticatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	limiter := &recordingDenyLimiter{}
	identityRateLimit := middleware.RateLimit(middleware.RateLimitOptions{
		Scope:   "identity",
		Limiter: limiter,
		KeyFunc: middleware.IdentityKey,
		Logger:  logger,
	})

	engine := New(Dependencies{
		Logger:            logger,
		HealthHandler:     health.NewHandler("test", logger),
		IdentityRateLimit: identityRateLimit,
		AuthMiddleware: func(c *gin.Context) {
			ctx := authctx.WithUserID(c.Request.Context(), 7)
			c.Request = c.Request.WithContext(ctx)
			c.Next()
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d body=%s", w.Code, w.Body.String())
	}
	if len(limiter.keys) != 1 || limiter.keys[0] != "user:7" {
		t.Fatalf("expected limiter key user:7, got %v", limiter.keys)
	}
}
