package profile

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/interaction"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/feedmodel"
)

type fakeUsers struct {
	target *user.User
	byID   map[uint64]*user.User
	getErr error
	idsErr error
}

func (f *fakeUsers) GetByID(ctx context.Context, id uint64) (*user.User, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.target == nil {
		return nil, user.ErrNotFound
	}
	return f.target, nil
}

func (f *fakeUsers) GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
	if f.idsErr != nil {
		return nil, f.idsErr
	}
	return f.byID, nil
}

type fakeVideos struct {
	count int64
	items []video.Video
	err   error
}

func (f *fakeVideos) CountByAuthor(ctx context.Context, authorID uint64) (int64, error) {
	return f.count, f.err
}

func (f *fakeVideos) ListReadyByIDs(ctx context.Context, ids []uint64) ([]video.Video, error) {
	return f.items, f.err
}

type fakeFollows struct {
	followers int64
	following int64
	isFollow  bool
	edges     []interaction.FollowEdge
	hasMore   bool
	err       error
}

func (f *fakeFollows) Counts(ctx context.Context, userID uint64) (int64, int64, error) {
	return f.followers, f.following, f.err
}

func (f *fakeFollows) IsFollowing(ctx context.Context, followerID, followeeID uint64) (bool, error) {
	return f.isFollow, f.err
}

func (f *fakeFollows) ListFollowers(
	ctx context.Context,
	userID uint64,
	cursor uint64,
	limit int,
) ([]interaction.FollowEdge, bool, error) {
	return f.edges, f.hasMore, f.err
}

func (f *fakeFollows) ListFollowing(
	ctx context.Context,
	userID uint64,
	cursor uint64,
	limit int,
) ([]interaction.FollowEdge, bool, error) {
	return f.edges, f.hasMore, f.err
}

type fakeLikes struct {
	edges    []interaction.LikeEdge
	hasMore  bool
	received int64
	err      error
}

func (f *fakeLikes) ListLikedVideoEdges(
	ctx context.Context,
	userID uint64,
	cursor uint64,
	limit int,
) ([]interaction.LikeEdge, bool, error) {
	return f.edges, f.hasMore, f.err
}

func (f *fakeLikes) CountLikesReceivedByAuthor(ctx context.Context, authorID uint64) (int64, error) {
	return f.received, f.err
}

type fakeLikeCounts struct {
	counts map[uint64]int64
	err    error
}

func (f *fakeLikeCounts) CountByVideoIDs(ctx context.Context, ids []uint64) (map[uint64]int64, error) {
	return f.counts, f.err
}

func newTestHandler(t *testing.T, users *fakeUsers, videos *fakeVideos, follows *fakeFollows, likes *fakeLikes, counts *fakeLikeCounts) (*gin.Engine, *Handler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := NewHandler(users, videos, follows, likes, counts, logger)
	engine := gin.New()
	engine.GET("/users/:id/profile", handler.GetProfile)
	engine.GET("/users/:id/followers", handler.ListFollowers)
	engine.GET("/users/:id/following", handler.ListFollowing)
	engine.GET("/users/me/liked-videos", handler.ListLikedVideos)
	return engine, handler
}

func TestHandler_GetProfile(t *testing.T) {
	users := &fakeUsers{target: &user.User{ID: 7, Username: "alice"}}
	follows := &fakeFollows{followers: 12, following: 3, isFollow: true}
	videos := &fakeVideos{count: 5}
	likes := &fakeLikes{received: 42}

	engine, _ := newTestHandler(t, users, videos, follows, likes, &fakeLikeCounts{})
	req := httptest.NewRequest(http.MethodGet, "/users/7/profile", nil)
	req = req.WithContext(authctx.WithUserID(req.Context(), 9))
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var got profileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.User.ID != 7 || got.User.Username != "alice" {
		t.Fatalf("unexpected user: %+v", got.User)
	}
	if got.Followers != 12 || got.Following != 3 || got.Videos != 5 || got.LikesReceived != 42 {
		t.Fatalf("unexpected counts: %+v", got)
	}
	if !got.IsFollowing || got.IsMe {
		t.Fatalf("unexpected flags: %+v", got)
	}
}

func TestHandler_GetProfile_NotFound(t *testing.T) {
	users := &fakeUsers{getErr: user.ErrNotFound}
	engine, _ := newTestHandler(t, users, &fakeVideos{}, &fakeFollows{}, &fakeLikes{}, &fakeLikeCounts{})

	req := httptest.NewRequest(http.MethodGet, "/users/404/profile", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestHandler_ListFollowers(t *testing.T) {
	follows := &fakeFollows{
		edges: []interaction.FollowEdge{
			{CursorID: 30, UserID: 1, Username: "a"},
			{CursorID: 20, UserID: 2, Username: "b"},
		},
		hasMore: true,
	}
	engine, _ := newTestHandler(t, &fakeUsers{}, &fakeVideos{}, follows, &fakeLikes{}, &fakeLikeCounts{})

	req := httptest.NewRequest(http.MethodGet, "/users/7/followers?cursor=40&page_size=2", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var got userListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 2 || got.Items[0].Username != "a" {
		t.Fatalf("unexpected items: %+v", got.Items)
	}
	if got.NextCursor != "20" || !got.HasMore {
		t.Fatalf("unexpected cursor: %+v", got)
	}
}

func TestHandler_ListLikedVideos_RequiresAuth(t *testing.T) {
	engine, _ := newTestHandler(t, &fakeUsers{}, &fakeVideos{}, &fakeFollows{}, &fakeLikes{}, &fakeLikeCounts{})
	req := httptest.NewRequest(http.MethodGet, "/users/me/liked-videos", nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestHandler_ListLikedVideos(t *testing.T) {
	users := &fakeUsers{byID: map[uint64]*user.User{
		7: {ID: 7, Username: "alice"},
	}}
	videos := &fakeVideos{items: []video.Video{{
		ID:          100,
		AuthorID:    7,
		Title:       "hello",
		ContentType: "video/mp4",
		ViewCount:   9,
		Status:      video.StatusReady,
		CreatedAt:   time.Now(),
	}}}
	likes := &fakeLikes{edges: []interaction.LikeEdge{{CursorID: 5, VideoID: 100}}}
	counts := &fakeLikeCounts{counts: map[uint64]int64{100: 3}}

	engine, _ := newTestHandler(t, users, videos, &fakeFollows{}, likes, counts)
	req := httptest.NewRequest(http.MethodGet, "/users/me/liked-videos", nil)
	req = req.WithContext(authctx.WithUserID(req.Context(), 9))
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var got feedmodel.Page
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("expected 1 item, got %+v", got.Items)
	}
	item := got.Items[0]
	if item.ID != 100 || item.Author.Username != "alice" || !item.IsLikedBy || item.LikeCount != 3 {
		t.Fatalf("unexpected item: %+v", item)
	}
}
