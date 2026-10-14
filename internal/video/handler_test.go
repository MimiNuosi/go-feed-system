package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"go-feed-system/pkg/authctx"

	"github.com/gin-gonic/gin"
)

// Fake LikeStateReader
type fakeLikeStateReader struct {
	GetLikeStateFunc    func(ctx context.Context, viewerID, videoID uint64) (int64, bool, error)
	CountByVideoIDsFunc func(
		ctx context.Context,
		videoIDs []uint64,
	) (map[uint64]int64, error)
	LikedVideoIDsByUserFunc func(
		ctx context.Context,
		userID uint64,
		videoIDs []uint64,
	) (map[uint64]bool, error)
	CallCount    int
	LastViewerID uint64
	LastVideoID  uint64
	LastCountIDs []uint64
}

func (f *fakeLikeStateReader) GetLikeState(ctx context.Context, viewerID, videoID uint64) (int64, bool, error) {
	f.CallCount++
	f.LastViewerID = viewerID
	f.LastVideoID = videoID
	if f.GetLikeStateFunc != nil {
		return f.GetLikeStateFunc(ctx, viewerID, videoID)
	}
	return 0, false, nil
}

func (f *fakeLikeStateReader) CountByVideoIDs(
	ctx context.Context,
	videoIDs []uint64,
) (map[uint64]int64, error) {
	f.LastCountIDs = videoIDs
	if f.CountByVideoIDsFunc != nil {
		return f.CountByVideoIDsFunc(ctx, videoIDs)
	}
	return map[uint64]int64{}, nil
}

func (f *fakeLikeStateReader) LikedVideoIDsByUser(
	ctx context.Context,
	userID uint64,
	videoIDs []uint64,
) (map[uint64]bool, error) {
	f.LastViewerID = userID
	if f.LikedVideoIDsByUserFunc != nil {
		return f.LikedVideoIDsByUserFunc(ctx, userID, videoIDs)
	}
	return map[uint64]bool{}, nil
}

// 辅助函数：构造 multipart 请求体
func createMultipartBody(t *testing.T, filename string, fileSize int) (io.Reader, string) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)

	// 构造文件部分
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}

	// 为了测试 413，我们往文件里写入指定大小的数据
	// 这里为了测试速度，只生成少量数据，真正测 413 时通过伪造 Content-Length 或在 Header 里做限制
	fakeData := make([]byte, fileSize)
	for i := range fakeData {
		fakeData[i] = 'a'
	}
	if _, err := part.Write(fakeData); err != nil {
		t.Fatalf("write file data: %v", err)
	}
	writer.Close()

	return body, writer.FormDataContentType()
}

// 3. 表驱动测试
func TestHandler_Upload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		hasAuth        bool
		filename       string
		contentType    string
		fileSize       int
		mockSvcFunc    func(ctx context.Context, input UploadInput) (*Video, error)
		wantStatusCode int
	}{
		{
			name:           "未授权 401",
			hasAuth:        false,
			filename:       "test.mp4",
			contentType:    "video/mp4",
			fileSize:       1024,
			wantStatusCode: http.StatusUnauthorized,
		},
		{
			name:    "缺少 multipart file 400",
			hasAuth: true,
			// 这里我们不发文件，直接发一个空请求，触发 FormFile 失败
			filename:       "",
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:        "不支持的媒体类型 415",
			hasAuth:     true,
			filename:    "test.exe",
			contentType: "application/octet-stream",
			fileSize:    1024,
			mockSvcFunc: func(ctx context.Context, input UploadInput) (*Video, error) {
				return nil, ErrUnsupportedMediaType
			},
			wantStatusCode: http.StatusUnsupportedMediaType,
		},
		{
			name:           "文件过大 413",
			hasAuth:        true,
			filename:       "test.mp4",
			contentType:    "video/mp4",
			fileSize:       MaxUploadSizeBytes + 1, // 触发 Handler 的 MaxBytesReader
			wantStatusCode: http.StatusRequestEntityTooLarge,
		},
		{
			name:        "Service 成功 201",
			hasAuth:     true,
			filename:    "test.mp4",
			contentType: "video/mp4",
			fileSize:    1024,
			mockSvcFunc: func(ctx context.Context, input UploadInput) (*Video, error) {
				return &Video{ID: 1, Title: "test video"}, nil
			},
			wantStatusCode: http.StatusCreated,
		},
		{
			name:        "Service 冲突 409",
			hasAuth:     true,
			filename:    "test.mp4",
			contentType: "video/mp4",
			fileSize:    1024,
			mockSvcFunc: func(ctx context.Context, input UploadInput) (*Video, error) {
				return nil, ErrConflict
			},
			wantStatusCode: http.StatusConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 Fake Service
			fakeSvc := &fakeVideoService{UploadFunc: tt.mockSvcFunc}

			// 构造 Handler 和 Router
			handler := NewHandler(fakeSvc, nil)
			r := gin.New()

			// 模拟 Auth 中间件：如果 hasAuth 为 true，则注入 UserID
			r.POST("/api/v1/videos", func(c *gin.Context) {
				if tt.hasAuth {
					ctx := authctx.WithUserID(c.Request.Context(), 1)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.Upload(c)
			})

			// 构造请求
			var req *http.Request
			if tt.filename != "" {
				body, contentType := createMultipartBody(t, tt.filename, tt.fileSize)
				req = httptest.NewRequest(http.MethodPost, "/api/v1/videos", body)
				req.Header.Set("Content-Type", contentType)
			} else {
				// 不传文件，模拟空表单
				req = httptest.NewRequest(http.MethodPost, "/api/v1/videos", nil)
				req.Header.Set("Content-Type", "multipart/form-data")
			}

			// 执行
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			// 断言状态码
			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d. Body: %s", tt.wantStatusCode, w.Code, w.Body.String())
			}
		})
	}
}

func TestHandler_ListByAuthor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	errLike := errors.New("like store failed")

	tests := []struct {
		name          string
		path          string
		withViewer    bool
		authorErr     error
		listErr       error
		listPage      *AuthorVideoPage
		likeErr       error
		likeCounts    map[uint64]int64
		likedMap      map[uint64]bool
		wantStatus    int
		wantItems     int
		wantLikeCount int64
		wantIsLiked   bool
		wantNext      string
		wantHasMore   bool
		wantLikedCall bool
	}{
		{
			name: "第一页正常返回且带分页信息",
			path: "/api/v1/users/7/videos?page_size=2",
			listPage: &AuthorVideoPage{
				Videos:     []Video{{ID: 9, Title: "v9"}, {ID: 8, Title: "v8"}},
				NextCursor: "8",
				HasMore:    true,
			},
			likeCounts:    map[uint64]int64{9: 5},
			wantStatus:    http.StatusOK,
			wantItems:     2,
			wantLikeCount: 5,
			wantNext:      "8",
			wantHasMore:   true,
		},
		{
			name:          "登录用户附带自己的点赞状态",
			path:          "/api/v1/users/7/videos",
			withViewer:    true,
			listPage:      &AuthorVideoPage{Videos: []Video{{ID: 9, Title: "v9"}}},
			likedMap:      map[uint64]bool{9: true},
			wantStatus:    http.StatusOK,
			wantItems:     1,
			wantIsLiked:   true,
			wantLikedCall: true,
		},
		{
			name:       "没有作品时返回空列表",
			path:       "/api/v1/users/7/videos",
			listPage:   &AuthorVideoPage{Videos: []Video{}},
			wantStatus: http.StatusOK,
			wantItems:  0,
		},
		{
			name:       "作者不存在返回 404",
			path:       "/api/v1/users/7/videos",
			authorErr:  ErrNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "用户 ID 非法返回 400",
			path:       "/api/v1/users/abc/videos",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "page_size 非法返回 400",
			path:       "/api/v1/users/7/videos?page_size=abc",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "游标非法返回 400",
			path:       "/api/v1/users/7/videos?cursor=oops",
			listErr:    ErrInvalidInput,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "点赞查询失败返回 500",
			path:       "/api/v1/users/7/videos",
			listPage:   &AuthorVideoPage{Videos: []Video{{ID: 9}}},
			likeErr:    errLike,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeSvc := &fakeVideoService{
				GetAuthorInfoFunc: func(ctx context.Context, userID uint64) (AuthorInfo, error) {
					if tt.authorErr != nil {
						return AuthorInfo{}, tt.authorErr
					}
					return AuthorInfo{ID: userID, Username: "author"}, nil
				},
				ListByAuthorFunc: func(
					ctx context.Context,
					authorID uint64,
					cursor string,
					pageSize int,
				) (*AuthorVideoPage, error) {
					if tt.listErr != nil {
						return nil, tt.listErr
					}
					return tt.listPage, nil
				},
			}
			likedCalled := false
			fakeLike := &fakeLikeStateReader{
				CountByVideoIDsFunc: func(
					ctx context.Context,
					videoIDs []uint64,
				) (map[uint64]int64, error) {
					if tt.likeErr != nil {
						return nil, tt.likeErr
					}
					return tt.likeCounts, nil
				},
				LikedVideoIDsByUserFunc: func(
					ctx context.Context,
					userID uint64,
					videoIDs []uint64,
				) (map[uint64]bool, error) {
					likedCalled = true
					return tt.likedMap, nil
				},
			}

			handler := NewHandler(fakeSvc, fakeLike)
			engine := gin.New()
			engine.GET("/api/v1/users/:id/videos", func(c *gin.Context) {
				if tt.withViewer {
					ctx := authctx.WithUserID(c.Request.Context(), 1001)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.ListByAuthor(c)
			})

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Fatalf("expected %d, got %d body=%s", tt.wantStatus, w.Code, w.Body.String())
			}
			if tt.wantStatus != http.StatusOK {
				return
			}

			var body struct {
				Items []struct {
					ID        uint64 `json:"id"`
					LikeCount int64  `json:"like_count"`
					IsLikedBy bool   `json:"is_liked_by"`
					Author    struct {
						Username string `json:"username"`
					} `json:"author"`
				} `json:"items"`
				NextCursor string `json:"next_cursor"`
				HasMore    bool   `json:"has_more"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}

			if len(body.Items) != tt.wantItems {
				t.Fatalf("expected %d items, got %d", tt.wantItems, len(body.Items))
			}
			if body.NextCursor != tt.wantNext {
				t.Errorf("expected next_cursor %q, got %q", tt.wantNext, body.NextCursor)
			}
			if body.HasMore != tt.wantHasMore {
				t.Errorf("expected has_more %v, got %v", tt.wantHasMore, body.HasMore)
			}
			if tt.wantItems > 0 {
				first := body.Items[0]
				if first.Author.Username != "author" {
					t.Errorf("expected author username, got %q", first.Author.Username)
				}
				if first.LikeCount != tt.wantLikeCount {
					t.Errorf("expected like_count %d, got %d", tt.wantLikeCount, first.LikeCount)
				}
				if first.IsLikedBy != tt.wantIsLiked {
					t.Errorf("expected is_liked_by %v, got %v", tt.wantIsLiked, first.IsLikedBy)
				}
			}
			if likedCalled != tt.wantLikedCall {
				t.Errorf("expected liked lookup called %v, got %v", tt.wantLikedCall, likedCalled)
			}
		})
	}
}

func TestHandler_GetDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		videoID string
		hasAuth bool   // 是否模拟登录
		userID  uint64 // 模拟登录的用户ID

		mockVideoFunc func(ctx context.Context, id uint64) (*VideoDetail, error)
		mockLikeFunc  func(ctx context.Context, viewerID, videoID uint64) (int64, bool, error)

		wantStatusCode int
		wantLikeCount  int64
		wantIsLikedBy  bool
		wantViewCount  int64
		wantLikeCalls  int    // 预期调用 LikeStateReader 的次数
		wantViewerID   uint64 // 预期传递给 LikeStateReader 的 viewerID
	}{
		{
			name:    "成功获取详情 200（匿名用户）",
			videoID: "1",
			hasAuth: false, // 匿名
			mockVideoFunc: func(ctx context.Context, id uint64) (*VideoDetail, error) {
				return &VideoDetail{
					ID:        1,
					Title:     "test video",
					ViewCount: 42,
					Author:    AuthorInfo{ID: 1, Username: "user001"},
				}, nil
			},
			mockLikeFunc: func(ctx context.Context, viewerID, videoID uint64) (int64, bool, error) {
				return 100, false, nil // 匿名用户即使视频有点赞，isLikedBy 也为 false
			},
			wantStatusCode: http.StatusOK,
			wantLikeCount:  100,
			wantIsLikedBy:  false,
			wantViewCount:  42,
			wantLikeCalls:  1, // 核心：即使是匿名，也要查点赞总数
			wantViewerID:   0, // 核心：匿名传 0
		},
		{
			name:    "成功获取详情 200（登录用户已点赞）",
			videoID: "1",
			hasAuth: true,
			userID:  123,
			mockVideoFunc: func(ctx context.Context, id uint64) (*VideoDetail, error) {
				return &VideoDetail{ID: 1, Title: "test video"}, nil
			},
			mockLikeFunc: func(ctx context.Context, viewerID, videoID uint64) (int64, bool, error) {
				return 5, true, nil
			},
			wantStatusCode: http.StatusOK,
			wantLikeCount:  5,
			wantIsLikedBy:  true,
			wantLikeCalls:  1,
			wantViewerID:   123, // 核心：登录传真实 ID
		},
		{
			name:    "视频不存在 404，不应继续查点赞",
			videoID: "999",
			hasAuth: true,
			mockVideoFunc: func(ctx context.Context, id uint64) (*VideoDetail, error) {
				return nil, ErrNotFound
			},
			wantStatusCode: http.StatusNotFound,
			wantLikeCalls:  0, // 核心：视频没找到，绝对不能去查点赞
		},
		{
			name:    "点赞状态查询失败 404",
			videoID: "1",
			hasAuth: true,
			mockVideoFunc: func(ctx context.Context, id uint64) (*VideoDetail, error) {
				return &VideoDetail{ID: 1}, nil
			},
			mockLikeFunc: func(ctx context.Context, viewerID, videoID uint64) (int64, bool, error) {
				return 0, false, ErrNotFound // 模拟点赞模块返回视频不存在
			},
			wantStatusCode: http.StatusNotFound, // 整个详情接口映射为 404
			wantLikeCalls:  1,
		},
		{
			name:    "点赞状态查询内部错误 500",
			videoID: "1",
			hasAuth: true,
			mockVideoFunc: func(ctx context.Context, id uint64) (*VideoDetail, error) {
				return &VideoDetail{ID: 1}, nil
			},
			mockLikeFunc: func(ctx context.Context, viewerID, videoID uint64) (int64, bool, error) {
				return 0, false, errors.New("db down")
			},
			wantStatusCode: http.StatusInternalServerError,
			wantLikeCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 Fake Service
			fakeSvc := &fakeVideoService{GetDetailFunc: tt.mockVideoFunc}
			// 构造 Fake LikeReader
			fakeLike := &fakeLikeStateReader{GetLikeStateFunc: tt.mockLikeFunc}

			// 注意：这里 NewHandler 要注入 fakeLike
			handler := NewHandler(fakeSvc, fakeLike)
			r := gin.New()

			r.GET("/api/v1/videos/:id", func(c *gin.Context) {
				if tt.hasAuth {
					ctx := authctx.WithUserID(c.Request.Context(), tt.userID)
					c.Request = c.Request.WithContext(ctx)
				}
				handler.GetDetail(c)
			})

			req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+tt.videoID, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			// 1. 断言状态码
			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d. Body: %s", tt.wantStatusCode, w.Code, w.Body.String())
			}

			// 2. 断言 LikeStateReader 调用次数与参数
			if fakeLike.CallCount != tt.wantLikeCalls {
				t.Errorf("expected LikeStateReader called %d times, got %d", tt.wantLikeCalls, fakeLike.CallCount)
			}
			if tt.wantLikeCalls > 0 && fakeLike.LastViewerID != tt.wantViewerID {
				t.Errorf("expected viewerID %d passed to LikeStateReader, got %d", tt.wantViewerID, fakeLike.LastViewerID)
			}

			// 3. 如果状态码是 200，验证 JSON 响应体中的聚合字段
			if tt.wantStatusCode == http.StatusOK {
				var resp map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("failed to decode body: %v", err)
				}

				// 验证 like_count 字段
				gotLikeCount := int64(resp["like_count"].(float64))
				if gotLikeCount != tt.wantLikeCount {
					t.Errorf("expected like_count %d, got %d", tt.wantLikeCount, gotLikeCount)
				}

				// 验证 is_liked_by 字段
				gotIsLikedBy := resp["is_liked_by"].(bool)
				if gotIsLikedBy != tt.wantIsLikedBy {
					t.Errorf("expected is_liked_by %v, got %v", tt.wantIsLikedBy, gotIsLikedBy)
				}

				// 验证 view_count 字段（详情里的浏览量来自 MySQL 快照）
				gotViewCount, ok := resp["view_count"].(float64)
				if !ok {
					t.Fatalf("view_count missing in body: %s", w.Body.String())
				}
				if int64(gotViewCount) != tt.wantViewCount {
					t.Errorf(
						"expected view_count %d, got %d",
						tt.wantViewCount,
						int64(gotViewCount),
					)
				}
			}
		})
	}
}

func TestHandler_File(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 模拟一个 20 字节的文件内容
	fileContent := []byte("01234567890123456789")

	tests := []struct {
		name           string
		videoID        string
		rangeHeader    string
		mockSvcFunc    func(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error)
		wantStatusCode int
		wantHeaders    map[string]string // 需要验证的响应头
		wantBodyLength int
	}{
		{
			name:    "完整文件请求 200",
			videoID: "1",
			mockSvcFunc: func(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error) {
				return &fakeReadSeekCloser{bytes.NewReader(fileContent)}, &Video{
					ID: 1, OriginalFilename: "test.mp4", ContentType: "video/mp4",
				}, nil
			},
			wantStatusCode: http.StatusOK,
			wantHeaders: map[string]string{
				"Accept-Ranges": "bytes",
			},
			wantBodyLength: 20,
		},
		{
			name:        "Range 请求 206",
			videoID:     "1",
			rangeHeader: "bytes=0-9",
			mockSvcFunc: func(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error) {
				return &fakeReadSeekCloser{bytes.NewReader(fileContent)}, &Video{
					ID: 1, OriginalFilename: "test.mp4", ContentType: "video/mp4",
				}, nil
			},
			wantStatusCode: http.StatusPartialContent, // 206
			wantHeaders: map[string]string{
				"Content-Range": "bytes 0-9/20", // 验证 Content-Range
				"Accept-Ranges": "bytes",
			},
			wantBodyLength: 10, // 只返回 10 个字节
		},
		{
			name:    "文件不存在 404",
			videoID: "1",
			mockSvcFunc: func(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error) {
				return nil, nil, ErrObjectNotFound
			},
			wantStatusCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeSvc := &fakeVideoService{OpenFileFunc: tt.mockSvcFunc}
			handler := NewHandler(fakeSvc, nil)

			r := gin.New()
			r.GET("/api/v1/videos/:id/file", handler.File)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+tt.videoID+"/file", nil)
			if tt.rangeHeader != "" {
				req.Header.Set("Range", tt.rangeHeader)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			// 验证状态码
			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d. Body: %s", tt.wantStatusCode, w.Code, w.Body.String())
			}

			// 验证响应头
			for k, v := range tt.wantHeaders {
				if got := w.Header().Get(k); got != v {
					t.Errorf("expected header %s: %s, got: %s", k, v, got)
				}
			}

			// 验证响应体长度（对于流式传输很重要）
			if tt.wantBodyLength > 0 && w.Body.Len() != tt.wantBodyLength {
				t.Errorf("expected body length %d, got %d", tt.wantBodyLength, w.Body.Len())
			}
		})
	}
}
