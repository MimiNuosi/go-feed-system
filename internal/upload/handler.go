package upload

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/video"
	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

type Handler struct {
	service *Service
	logger  *slog.Logger
}

func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}

type initRequest struct {
	FileName    string `json:"file_name" binding:"required"`
	FileSize    int64  `json:"file_size" binding:"required"`
	FileMD5     string `json:"file_md5" binding:"required"`
	ContentType string `json:"content_type"`
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	ChunkSize   int    `json:"chunk_size"`
}

// Init 创建分片上传会话；命中秒传时直接返回 video_id。
func (h *Handler) Init(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}

	var request initRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid request body")
		return
	}

	result, err := h.service.Init(c.Request.Context(), InitInput{
		AuthorID:    userID,
		FileName:    request.FileName,
		FileSize:    request.FileSize,
		FileMD5:     request.FileMD5,
		ContentType: request.ContentType,
		Title:       request.Title,
		Description: request.Description,
		ChunkSize:   request.ChunkSize,
	})
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

// UploadChunk 接收一片（multipart：upload_id / chunk_index / file）。
func (h *Handler) UploadChunk(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}

	uploadID := c.PostForm("upload_id")
	index, err := strconv.Atoi(c.PostForm("chunk_index"))
	if uploadID == "" || err != nil || index < 0 {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid chunk fields")
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "missing chunk file")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "cannot read chunk file")
		return
	}
	defer file.Close()

	received, total, err := h.service.UploadChunk(
		c.Request.Context(),
		userID,
		uploadID,
		index,
		fileHeader.Size,
		file,
	)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"received": received, "total": total})
}

// Status 返回已上传分片，前端用它做断点续传。
func (h *Handler) Status(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}
	uploadID := c.Query("upload_id")
	if uploadID == "" {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "missing upload_id")
		return
	}

	session, indexes, err := h.service.Status(c.Request.Context(), userID, uploadID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	response := gin.H{
		"upload_id":    session.ID,
		"status":       session.Status,
		"uploaded":     indexes,
		"total_chunks": session.TotalChunks,
	}
	if session.VideoID != nil {
		response["video_id"] = *session.VideoID
	}
	c.JSON(http.StatusOK, response)
}

type completeRequest struct {
	UploadID string `json:"upload_id" binding:"required"`
}

// Complete 合并分片并生成视频。
func (h *Handler) Complete(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}

	var request completeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid request body")
		return
	}

	videoID, err := h.service.Complete(c.Request.Context(), userID, request.UploadID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"video_id": videoID})
}

func (h *Handler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid upload request")
	case errors.Is(err, ErrSessionNotFound):
		httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "upload session not found")
	case errors.Is(err, ErrForbidden):
		httpx.Abort(c, http.StatusForbidden, httpx.ErrorCodeForbidden, "upload session forbidden")
	case errors.Is(err, ErrIncomplete):
		httpx.Abort(c, http.StatusConflict, httpx.ErrorCodeConflict, "upload is not complete")
	case errors.Is(err, ErrUploadBusy):
		httpx.Abort(c, http.StatusConflict, httpx.ErrorCodeConflict, "upload merge is busy")
	case errors.Is(err, video.ErrUnsupportedMediaType):
		httpx.Abort(c, http.StatusUnsupportedMediaType, httpx.ErrorCodeInvalidArgument, "unsupported video type")
	case errors.Is(err, video.ErrFileTooLarge):
		httpx.Abort(c, http.StatusRequestEntityTooLarge, httpx.ErrorCodeInvalidArgument, "file too large")
	case errors.Is(err, video.ErrConflict):
		httpx.Abort(c, http.StatusConflict, httpx.ErrorCodeConflict, "video conflict")
	default:
		h.logger.Error(
			"chunked upload failed",
			"request_id", requestid.FromContext(c.Request.Context()),
			"error", err,
		)
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
	}
}
