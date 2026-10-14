package hot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/authctx"
)

type fakeRecordViewService struct {
	calls      int
	videoID    uint64
	viewerKey  string
	recorded   bool
	err        error
	listCalls  int
	listWindow Window
	listAt     time.Time
	listCursor *Cursor
	listPage   int
	listVideos []RankedVideo
	listHotErr error
	listNext   string
	listMore   bool
	videosByID []video.Video
	userMap    map[uint64]*user.User
	likeCounts map[uint64]int64
	likedMap   map[uint64]bool

	// 读模型缓存相关的调用计数
	videoCalls     int
	userCalls      int
	likeCountCalls int
	likedCalls     int
}

func (f *fakeRecordViewService) RecordView(
	ctx context.Context,
	videoID uint64,
	viewerKey string,
	occurredAt time.Time,
) (bool, error) {
	f.calls++
	f.videoID = videoID
	f.viewerKey = viewerKey
	return f.recorded, f.err
}

func (f *fakeRecordViewService) ListHot(
	ctx context.Context,
	window Window,
	at time.Time,
	cursor *Cursor,
	pageSize int,
) (*Page, error) {
	f.listCalls++
	f.listWindow = window
	f.listAt = at
	f.listCursor = cursor
	f.listPage = pageSize
	if f.listHotErr != nil {
		return nil, f.listHotErr
	}

	return &Page{
		Videos:     f.listVideos,
		NextCursor: f.listNext,
		HasMore:    f.listMore,
	}, nil
}

func (f *fakeRecordViewService) ListReadyByIDs(
	ctx context.Context,
	ids []uint64,
) ([]video.Video, error) {
	f.videoCalls++
	return f.videosByID, nil
}

func (f *fakeRecordViewService) GetByIDs(
	ctx context.Context,
	ids []uint64,
) (map[uint64]*user.User, error) {
	f.userCalls++
	return f.userMap, nil
}

func (f *fakeRecordViewService) CountByVideoIDs(
	ctx context.Context,
	videoIDs []uint64,
) (map[uint64]int64, error) {
	f.likeCountCalls++
	return f.likeCounts, nil
}

func (f *fakeRecordViewService) LikedVideoIDsByUser(
	ctx context.Context,
	userID uint64,
	videoIDs []uint64,
) (map[uint64]bool, error) {
	f.likedCalls++
	return f.likedMap, nil
}

func TestHandler_RecordView(t *testing.T) {
	gin.SetMode(gin.TestMode)
	hashedIP := hashTestIP("203.0.113.7")

	tests := []struct {
		name string

		videoIDParam string
		userID       uint64
		recorded     bool
		serviceErr   error

		wantStatus    int
		wantCalls     int
		wantVideoID   uint64
		wantViewerKey string
	}{
		{
			name:          "登录用户记录浏览",
			videoIDParam:  "101",
			userID:        7,
			recorded:      true,
			wantStatus:    http.StatusNoContent,
			wantCalls:     1,
			wantVideoID:   101,
			wantViewerKey: "user:7",
		},
		{
			name:          "匿名用户使用 IP 哈希",
			videoIDParam:  "101",
			recorded:      true,
			wantStatus:    http.StatusNoContent,
			wantCalls:     1,
			wantVideoID:   101,
			wantViewerKey: "ip:" + hashedIP,
		},
		{
			name:         "重复浏览仍返回 204",
			videoIDParam: "101",
			recorded:     false,
			wantStatus:   http.StatusNoContent,
			wantCalls:    1,
			wantVideoID:  101,
		},
		{
			name:         "非法 video id",
			videoIDParam: "abc",
			wantStatus:   http.StatusBadRequest,
			wantCalls:    0,
		},
		{
			name:         "Service 参数错误",
			videoIDParam: "101",
			serviceErr:   ErrInvalidInput,
			wantStatus:   http.StatusBadRequest,
			wantCalls:    1,
			wantVideoID:  101,
		},
		{
			name:         "Service 内部错误",
			videoIDParam: "101",
			serviceErr:   errors.New("redis failed"),
			wantStatus:   http.StatusInternalServerError,
			wantCalls:    1,
			wantVideoID:  101,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeRecordViewService{
				recorded: tt.recorded,
				err:      tt.serviceErr,
			}
			handler := NewHandler(
				service,
				service,
				service,
				service,
				service,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				0, // 关闭读模型缓存：这一组用例关心的是每个请求的完整路径
			)

			engine := gin.New()
			engine.POST("/api/v1/videos/:id/view", handler.RecordView)

			request := httptest.NewRequest(
				http.MethodPost,
				"/api/v1/videos/"+tt.videoIDParam+"/view",
				nil,
			)
			request.RemoteAddr = "203.0.113.7:12345"
			if tt.userID != 0 {
				ctx := authctx.WithUserID(request.Context(), tt.userID)
				request = request.WithContext(ctx)
			}

			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d body=%s",
					tt.wantStatus,
					response.Code,
					response.Body.String(),
				)
			}
			if service.calls != tt.wantCalls {
				t.Fatalf("expected %d service calls, got %d", tt.wantCalls, service.calls)
			}
			if tt.wantVideoID != 0 && service.videoID != tt.wantVideoID {
				t.Fatalf("expected video ID %d, got %d", tt.wantVideoID, service.videoID)
			}
			if tt.wantViewerKey != "" && service.viewerKey != tt.wantViewerKey {
				t.Fatalf(
					"expected viewer key %q, got %q",
					tt.wantViewerKey,
					service.viewerKey,
				)
			}
			if tt.userID == 0 && tt.wantCalls > 0 &&
				strings.Contains(service.viewerKey, "203.0.113.7") {
				t.Fatal("viewer key must not expose raw client IP")
			}
		})
	}
}

// TestHandler_ListHot_ItemCache 验证读模型缓存：
// 命中时不再查视频/作者/点赞，TTL 过期后重新查。
func TestHandler_ListHot_ItemCache(t *testing.T) {
	gin.SetMode(gin.TestMode)

	clock := &fakeClock{now: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)}
	service := newHotPageFakeService()
	handler := NewHandler(
		service, service, service, service, service,
		discardLogger(), time.Second,
	)
	handler.now = clock.Now

	engine := gin.New()
	engine.GET("/api/v1/feed/hot", handler.ListHot)

	call := func() string {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/feed/hot?window=1h", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}
		return w.Body.String()
	}

	first := call()
	if service.videoCalls != 1 || service.userCalls != 1 || service.likeCountCalls != 1 {
		t.Fatalf(
			"第一次请求应该各查一次：video=%d user=%d like_count=%d",
			service.videoCalls,
			service.userCalls,
			service.likeCountCalls,
		)
	}

	second := call()
	if service.videoCalls != 1 || service.userCalls != 1 || service.likeCountCalls != 1 {
		t.Fatalf(
			"缓存命中后不该再查数据库：video=%d user=%d like_count=%d",
			service.videoCalls,
			service.userCalls,
			service.likeCountCalls,
		)
	}
	if second != first {
		t.Fatalf("缓存命中后的响应应该完全一致：\nfirst =%s\nsecond=%s", first, second)
	}

	clock.Set(clock.Now().Add(2 * time.Second))
	call()
	if service.videoCalls != 2 {
		t.Fatalf("TTL 过期后应该重新查，video 调用次数 = %d", service.videoCalls)
	}
}

// TestHandler_ListHot_IsLikedByIsNotSharedBetweenViewers 卡住这个缓存最容易写错的地方：
// 缓存条目里绝不包含 is_liked_by，否则会把上一个访问者的点赞状态发给所有人。
func TestHandler_ListHot_IsLikedByIsNotSharedBetweenViewers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	clock := &fakeClock{now: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)}
	service := newHotPageFakeService()
	service.likedMap = map[uint64]bool{3: true}

	handler := NewHandler(
		service, service, service, service, service,
		discardLogger(), time.Second,
	)
	handler.now = clock.Now

	engine := gin.New()
	engine.GET("/api/v1/feed/hot", handler.ListHot)

	callAs := func(userID uint64) bool {
		t.Helper()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/feed/hot?window=1h", nil)
		if userID > 0 {
			ctx := authctx.WithUserID(req.Context(), userID)
			req = req.WithContext(ctx)
		}

		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
		}

		var body struct {
			Items []struct {
				IsLikedBy bool `json:"is_liked_by"`
			} `json:"items"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(body.Items) != 1 {
			t.Fatalf("expected 1 item, got %d", len(body.Items))
		}
		return body.Items[0].IsLikedBy
	}

	// 1) 匿名访问写入缓存，且不该查"我是否点过赞"
	if isLiked := callAs(0); isLiked {
		t.Fatal("匿名请求的 is_liked_by 必须为 false")
	}
	if service.likedCalls != 0 {
		t.Fatalf("匿名请求不该查询点赞状态，实际调用 %d 次", service.likedCalls)
	}

	// 2) 登录用户命中同一条缓存，但点赞状态必须实时查
	if isLiked := callAs(7); !isLiked {
		t.Fatal("登录用户应该看到 is_liked_by=true")
	}
	if service.likedCalls != 1 {
		t.Fatalf("登录用户应该查一次点赞状态，实际 %d 次", service.likedCalls)
	}
	if service.videoCalls != 1 {
		t.Fatalf("命中缓存不该重查视频，实际 %d 次", service.videoCalls)
	}

	// 3) 再匿名：缓存不能被上一个访问者的点赞状态污染
	if isLiked := callAs(0); isLiked {
		t.Fatal("缓存被污染：匿名请求看到了别人的点赞状态")
	}
}

// newHotPageFakeService 返回一个"热榜页有 1 条数据"的假依赖。
func newHotPageFakeService() *fakeRecordViewService {
	return &fakeRecordViewService{
		listVideos: []RankedVideo{{VideoID: 3, Score: 5}},
		videosByID: []video.Video{{
			ID:          3,
			AuthorID:    10,
			Title:       "hot video",
			Description: "desc",
			ContentType: "video/mp4",
			Status:      video.StatusReady,
		}},
		userMap:    map[uint64]*user.User{10: {ID: 10, Username: "author"}},
		likeCounts: map[uint64]int64{3: 7},
	}
}

func TestHandler_ListHot(t *testing.T) {
	gin.SetMode(gin.TestMode)

	validCursor, err := EncodeCursor(Cursor{Score: 20, VideoID: 103})
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}

	tests := []struct {
		name string

		query         string
		serviceVideos []RankedVideo
		serviceErr    error
		serviceNext   string
		serviceMore   bool
		wantStatus    int
		wantCalls     int
		wantWindow    Window
		wantPageSize  int
		wantBody      string
		wantViewCount int64
		wantCursorID  uint64
	}{
		{
			name:          "默认分页返回热榜",
			query:         "?window=1h",
			serviceVideos: []RankedVideo{{VideoID: 3, Score: 5}},
			wantStatus:    http.StatusOK,
			wantCalls:     1,
			wantWindow:    WindowOneHour,
			wantPageSize:  DefaultHotPageSize,
			wantBody:      `"id":3`,
			wantViewCount: 42,
		},
		{
			name:         "page_size 裁剪到上限",
			query:        "?window=24h&page_size=1000",
			wantStatus:   http.StatusOK,
			wantCalls:    1,
			wantWindow:   WindowTwentyFourH,
			wantPageSize: MaxHotPageSize,
		},
		{
			name:       "window 非法",
			query:      "?window=7d",
			wantStatus: http.StatusBadRequest,
			wantCalls:  0,
		},
		{
			name:       "page_size 非法",
			query:      "?window=1h&page_size=abc",
			wantStatus: http.StatusBadRequest,
			wantCalls:  0,
		},
		{
			name:       "cursor 非法",
			query:      "?window=1h&cursor=abc",
			wantStatus: http.StatusBadRequest,
			wantCalls:  0,
		},
		{
			name:          "合法游标会透传给服务并返回下一页信息",
			query:         "?window=1h&cursor=" + validCursor,
			serviceVideos: []RankedVideo{{VideoID: 3, Score: 5}},
			serviceNext:   "next-token",
			serviceMore:   true,
			wantStatus:    http.StatusOK,
			wantCalls:     1,
			wantWindow:    WindowOneHour,
			wantPageSize:  DefaultHotPageSize,
			wantBody:      `"next_cursor":"next-token"`,
			wantCursorID:  103,
		},
		{
			name:         "Service 参数错误",
			query:        "?window=1h",
			serviceErr:   ErrInvalidInput,
			wantStatus:   http.StatusBadRequest,
			wantCalls:    1,
			wantWindow:   WindowOneHour,
			wantPageSize: DefaultHotPageSize,
		},
		{
			name:         "Service 内部错误",
			query:        "?window=1h",
			serviceErr:   errors.New("redis failed"),
			wantStatus:   http.StatusInternalServerError,
			wantCalls:    1,
			wantWindow:   WindowOneHour,
			wantPageSize: DefaultHotPageSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeRecordViewService{
				listVideos: tt.serviceVideos,
				listHotErr: tt.serviceErr,
				listNext:   tt.serviceNext,
				listMore:   tt.serviceMore,
				videosByID: []video.Video{
					{
						ID:          3,
						AuthorID:    10,
						Title:       "hot video",
						Description: "desc",
						ContentType: "video/mp4",
						CreatedAt:   time.Now(),
						ViewCount:   42,
						Status:      video.StatusReady,
					},
				},
				userMap: map[uint64]*user.User{
					10: {ID: 10, Username: "author"},
				},
				likeCounts: map[uint64]int64{3: 7},
			}
			handler := NewHandler(
				service,
				service,
				service,
				service,
				service,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
				0, // 这组用例走完整路径，不开读模型缓存
			)

			engine := gin.New()
			engine.GET("/api/v1/feed/hot", handler.ListHot)

			request := httptest.NewRequest(
				http.MethodGet,
				"/api/v1/feed/hot"+tt.query,
				nil,
			)
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)

			if response.Code != tt.wantStatus {
				t.Fatalf(
					"expected status %d, got %d body=%s",
					tt.wantStatus,
					response.Code,
					response.Body.String(),
				)
			}
			if service.listCalls != tt.wantCalls {
				t.Fatalf(
					"expected %d ListHot calls, got %d",
					tt.wantCalls,
					service.listCalls,
				)
			}
			if tt.wantCalls > 0 {
				if service.listWindow != tt.wantWindow {
					t.Fatalf(
						"expected window %q, got %q",
						tt.wantWindow,
						service.listWindow,
					)
				}
				if service.listPage != tt.wantPageSize {
					t.Fatalf(
						"expected page size %d, got %d",
						tt.wantPageSize,
						service.listPage,
					)
				}
				if tt.wantCursorID != 0 {
					if service.listCursor == nil ||
						service.listCursor.VideoID != tt.wantCursorID {
						t.Fatalf(
							"expected cursor video ID %d, got %+v",
							tt.wantCursorID,
							service.listCursor,
						)
					}
				}
			}
			if tt.wantBody != "" && !strings.Contains(response.Body.String(), tt.wantBody) {
				t.Fatalf(
					"expected body to contain %q, got %s",
					tt.wantBody,
					response.Body.String(),
				)
			}

			// 热榜 Item 的播放量来自批量查回的 videos，必须透传出来
			if tt.wantViewCount > 0 {
				expected := fmt.Sprintf(`"view_count":%d`, tt.wantViewCount)
				if !strings.Contains(response.Body.String(), expected) {
					t.Fatalf(
						"expected body to contain %q, got %s",
						expected,
						response.Body.String(),
					)
				}
			}

			// 分页元数据要透传到响应里
			if tt.serviceMore {
				if !strings.Contains(response.Body.String(), `"has_more":true`) {
					t.Fatalf(
						"expected body to contain has_more true, got %s",
						response.Body.String(),
					)
				}
			}
		})
	}
}

func hashTestIP(ip string) string {
	sum := sha256.Sum256([]byte(ip))
	return hex.EncodeToString(sum[:])
}
