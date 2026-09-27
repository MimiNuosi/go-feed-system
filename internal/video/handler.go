package video

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
)

type VideoService interface {
	Upload(ctx context.Context, input UploadInput) (*Video, error)
	GetByID(ctx context.Context, id uint64) (*Video, error)
	GetDetail(ctx context.Context, id uint64) (*VideoDetail, error)
	OpenFile(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error)
}

type Handler struct {
	service VideoService
}

func NewHandler(service VideoService) *Handler {
	return &Handler{
		service: service,
	}
}

type VideoResponse struct {
	ID               uint64 `json:"id"`
	AuthorID         uint64 `json:"author_id"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	OriginalFilename string `json:"original_filename"`
	ContentType      string `json:"content_type"`
	SizeBytes        int64  `json:"size_bytes"`
	Status           string `json:"status"`
}

func NewVideoResponse(item *Video) VideoResponse {
	return VideoResponse{
		ID:               item.ID,
		AuthorID:         item.AuthorID,
		Title:            item.Title,
		Description:      item.Description,
		OriginalFilename: item.OriginalFilename,
		ContentType:      item.ContentType,
		SizeBytes:        item.SizeBytes,
		Status:           item.Status,
	}
}

func (h *Handler) Upload(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeUnauthorized),
			httpx.ErrorCodeUnauthorized,
			"unauthorized",
		)
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxUploadSizeBytes)

	title := c.PostForm("title")
	description := c.PostForm("description")

	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpx.Abort(c, http.StatusRequestEntityTooLarge, httpx.ErrorCodeInvalidArgument, "video file is too large")
		} else {
			httpx.Abort(c, httpx.StatusCode(httpx.ErrorCodeInvalidArgument), httpx.ErrorCodeInvalidArgument, "invalid multipart request")
		}
		return
	}

	file, err := fileHeader.Open()
	if err != nil {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInternal),
			httpx.ErrorCodeInternal,
			"internal server error",
		)
		return
	}
	defer file.Close()

	createdVideo, err := h.service.Upload(c.Request.Context(), UploadInput{
		AuthorID:         userID,
		Title:            title,
		Description:      description,
		OriginalFilename: fileHeader.Filename,
		ContentType:      fileHeader.Header.Get("Content-Type"),
		SizeBytes:        fileHeader.Size,
		Content:          file,
	})
	if err != nil {
		h.handleUploadError(c, err)
		return
	}

	c.JSON(http.StatusCreated, NewVideoResponse(createdVideo))
}

func (h *Handler) handleUploadError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInvalidArgument),
			httpx.ErrorCodeInvalidArgument,
			"invalid video input",
		)
	case errors.Is(err, ErrFileTooLarge):
		httpx.Abort(
			c,
			http.StatusRequestEntityTooLarge,
			httpx.ErrorCodeInvalidArgument,
			"video file is too large",
		)
	case errors.Is(err, ErrUnsupportedMediaType):
		httpx.Abort(
			c,
			http.StatusUnsupportedMediaType,
			httpx.ErrorCodeInvalidArgument,
			"unsupported video media type",
		)
	case errors.Is(err, ErrConflict):
		httpx.Abort(
			c,
			http.StatusConflict,
			httpx.ErrorCodeConflict,
			"video already exists",
		)
	default:
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInternal),
			httpx.ErrorCodeInternal,
			"internal server error",
		)
	}
}

func (h *Handler) GetDetail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid video id")
		return
	}

	detail, err := h.service.GetDetail(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "video not found")
			return
		}
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
		return
	}

	c.JSON(http.StatusOK, detail)
}

func (h *Handler) File(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid video id")
		return
	}

	file, item, err := h.service.OpenFile(c.Request.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrObjectNotFound) {
			httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "file not found")
			return
		}
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
		return
	}
	defer file.Close()

	// 核心：设置 Content-Type，并交给 http.ServeContent 自动处理 Range
	c.Header("Content-Type", item.ContentType)
	c.Header("Accept-Ranges", "bytes")

	// http.ServeContent 需要 ReadSeeker 和修改时间（传 time.Now() 或数据库的 CreatedAt 都可以）
	// 它内部会自动处理 Range 请求，返回 206 Partial Content
	http.ServeContent(c.Writer, c.Request, item.OriginalFilename, item.CreatedAt, file)
}
