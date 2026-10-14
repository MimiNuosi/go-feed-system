package video

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/feedmodel"
	"go-feed-system/pkg/httpx"
)

type VideoService interface {
	Upload(ctx context.Context, input UploadInput) (*Video, error)
	GetByID(ctx context.Context, id uint64) (*Video, error)
	GetDetail(ctx context.Context, id uint64) (*VideoDetail, error)
	OpenFile(ctx context.Context, id uint64) (io.ReadSeekCloser, *Video, error)
	GetAuthorInfo(ctx context.Context, userID uint64) (AuthorInfo, error)
	ListByAuthor(
		ctx context.Context,
		authorID uint64,
		cursor string,
		pageSize int,
	) (*AuthorVideoPage, error)
}

// LikeStateReader 是视频详情需要的点赞状态读取能力。
//
// 接口定义在 video 包中，interaction.LikeService 可以隐式实现它。
// 这样 Video 模块不需要导入 interaction，避免形成包依赖环。
type LikeStateReader interface {
	GetLikeState(ctx context.Context, viewerID, videoID uint64) (count int64, isLikedBy bool, err error)

	// 下面两个批量方法供列表类接口使用，避免按视频逐条查询。
	CountByVideoIDs(
		ctx context.Context,
		videoIDs []uint64,
	) (map[uint64]int64, error)
	LikedVideoIDsByUser(
		ctx context.Context,
		userID uint64,
		videoIDs []uint64,
	) (map[uint64]bool, error)
}

type Handler struct {
	service    VideoService
	likeReader LikeStateReader
}

func NewHandler(service VideoService, likeReader LikeStateReader) *Handler {
	return &Handler{
		service:    service,
		likeReader: likeReader,
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

	// 1. 获取可选鉴权注入的 userID
	// 匿名访问时 ok 为 false，此时 viewerID 默认就是 0，不需要写 if-else
	viewerID, _ := authctx.UserID(c.Request.Context())

	// 2. 调用 likeReader 获取点赞状态
	count, isLikedBy, err := h.likeReader.GetLikeState(c.Request.Context(), viewerID, id)
	if err != nil {
		// 3. 错误处理
		if errors.Is(err, ErrNotFound) {
			httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "video not found")
			return
		}
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
		return
	}

	// 4. 将 count 和 isLikedBy 写入 detail
	detail.LikeCount = count
	detail.IsLikedBy = isLikedBy

	// 5. 返回最终的 JSON 响应
	c.JSON(http.StatusOK, detail)
}

// ListByAuthor 处理 GET /api/v1/users/:id/videos：某个作者的作品列表。
//
// 公开可读（别人的作品列表也要能看），登录后额外附带"我是否点过赞"。
// 跨领域的聚合放在 Handler：视频来自 VideoService，点赞数来自 LikeReader。
func (h *Handler) ListByAuthor(c *gin.Context) {
	authorID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || authorID == 0 {
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"invalid user id",
		)
		return
	}

	ctx := c.Request.Context()

	// 先确认作者存在：不存在就 404，而不是返回空列表 ——
	// 否则调用方分不清"这个人没有作品"和"这个人不存在"。
	author, err := h.service.GetAuthorInfo(ctx, authorID)
	if err != nil {
		h.handleListByAuthorError(c, err)
		return
	}

	pageSize := 0
	if raw := c.Query("page_size"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			httpx.Abort(
				c,
				http.StatusBadRequest,
				httpx.ErrorCodeInvalidArgument,
				"invalid page_size",
			)
			return
		}
		pageSize = parsed
	}

	page, err := h.service.ListByAuthor(ctx, authorID, c.Query("cursor"), pageSize)
	if err != nil {
		h.handleListByAuthorError(c, err)
		return
	}

	result := &feedmodel.Page{
		Items:      []feedmodel.Item{},
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}
	if len(page.Videos) == 0 {
		c.JSON(http.StatusOK, result)
		return
	}

	videoIDs := make([]uint64, 0, len(page.Videos))
	for _, item := range page.Videos {
		videoIDs = append(videoIDs, item.ID)
	}

	likeCounts, err := h.likeReader.CountByVideoIDs(ctx, videoIDs)
	if err != nil {
		httpx.Abort(
			c,
			http.StatusInternalServerError,
			httpx.ErrorCodeInternal,
			"internal server error",
		)
		return
	}

	likedMap := map[uint64]bool{}
	if viewerID, ok := authctx.UserID(ctx); ok && viewerID > 0 {
		likedMap, err = h.likeReader.LikedVideoIDsByUser(ctx, viewerID, videoIDs)
		if err != nil {
			httpx.Abort(
				c,
				http.StatusInternalServerError,
				httpx.ErrorCodeInternal,
				"internal server error",
			)
			return
		}
	}

	for _, item := range page.Videos {
		result.Items = append(result.Items, feedmodel.Item{
			ID: item.ID,
			Author: feedmodel.AuthorInfo{
				ID:       author.ID,
				Username: author.Username,
			},
			Title:       item.Title,
			Description: item.Description,
			ContentType: item.ContentType,
			CreatedAt:   item.CreatedAt,
			ViewCount:   item.ViewCount,
			LikeCount:   likeCounts[item.ID],
			IsLikedBy:   likedMap[item.ID],
		})
	}

	c.JSON(http.StatusOK, result)
}

func (h *Handler) handleListByAuthorError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"invalid request",
		)
	case errors.Is(err, ErrNotFound):
		httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "user not found")
	default:
		httpx.Abort(
			c,
			http.StatusInternalServerError,
			httpx.ErrorCodeInternal,
			"internal server error",
		)
	}
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
