package video

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go-feed-system/pkg/authctx"
)

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
			handler := NewHandler(fakeSvc)
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

func TestHandler_GetDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		videoID        string
		mockSvcFunc    func(ctx context.Context, id uint64) (*VideoDetail, error)
		wantStatusCode int
	}{
		{
			name:    "成功获取详情 200",
			videoID: "1",
			mockSvcFunc: func(ctx context.Context, id uint64) (*VideoDetail, error) {
				return &VideoDetail{
					ID:     1,
					Title:  "test video",
					Author: AuthorInfo{ID: 1, Username: "user001"},
				}, nil
			},
			wantStatusCode: http.StatusOK,
		},
		{
			name:           "非法 ID 400",
			videoID:        "abc",
			wantStatusCode: http.StatusBadRequest,
		},
		{
			name:    "视频不存在 404",
			videoID: "999",
			mockSvcFunc: func(ctx context.Context, id uint64) (*VideoDetail, error) {
				return nil, ErrNotFound
			},
			wantStatusCode: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeSvc := &fakeVideoService{GetDetailFunc: tt.mockSvcFunc}
			handler := NewHandler(fakeSvc)

			r := gin.New()
			r.GET("/api/v1/videos/:id", handler.GetDetail)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+tt.videoID, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatusCode {
				t.Errorf("expected status %d, got %d. Body: %s", tt.wantStatusCode, w.Code, w.Body.String())
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
			handler := NewHandler(fakeSvc)

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
