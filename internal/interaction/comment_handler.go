package interaction

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

type CommentServiceAPI interface {
	Create(ctx context.Context, userID, videoID uint64, content string) (*Comment, error)
	List(ctx context.Context, videoID, cursor uint64, pageSize int) (*CommentPage, error)
	Delete(ctx context.Context, userID, commentID uint64) error
}

type CommentHandler struct {
	service CommentServiceAPI
	logger  *slog.Logger
}

func NewCommentHandler(service CommentServiceAPI, logger *slog.Logger) *CommentHandler {
	return &CommentHandler{
		service: service,
		logger:  logger,
	}
}

type createCommentRequest struct {
	Content string `json:"content"`
}

type createCommentResponse struct {
	ID        uint64    `json:"id"`
	VideoID   uint64    `json:"video_id"`
	UserID    uint64    `json:"user_id"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *CommentHandler) Create(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}

	videoID, ok := parseUintParam(c, "id", "invalid video id")
	if !ok {
		return
	}

	var request createCommentRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid request body")
		return
	}

	comment, err := h.service.Create(c.Request.Context(), userID, videoID, request.Content)
	if err != nil {
		h.handleError(c, err)
		return
	}

	// 返回 201 Created 和过滤后的 JSON
	c.JSON(http.StatusCreated, createCommentResponse{
		ID:        comment.ID,
		VideoID:   comment.VideoID,
		UserID:    comment.UserID,
		Content:   comment.Content,
		CreatedAt: comment.CreatedAt,
	})
}

func (h *CommentHandler) List(c *gin.Context) {
	videoID, ok := parseUintParam(c, "id", "invalid video id")
	if !ok {
		return
	}

	// 1. 解析 cursor（空字符串表示第一页，设为 0）
	var cursor uint64
	cursorStr := c.Query("cursor")
	if cursorStr != "" {
		var err error
		cursor, err = strconv.ParseUint(cursorStr, 10, 64)
		if err != nil {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid cursor")
			return
		}
	}

	// 2. 解析 page_size（非法值返回 400，交给 Service 处理默认值和最大值）
	var pageSize int
	pageSizeStr := c.Query("page_size")
	if pageSizeStr != "" {
		var err error
		pageSize, err = strconv.Atoi(pageSizeStr)
		if err != nil || pageSize < 0 {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid page_size")
			return
		}
	}

	// 3. 调用 Service
	page, err := h.service.List(c.Request.Context(), videoID, cursor, pageSize)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, page)
}

func (h *CommentHandler) Delete(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}

	commentID, ok := parseUintParam(c, "id", "invalid comment id")
	if !ok {
		return
	}

	err := h.service.Delete(c.Request.Context(), userID, commentID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	// 成功返回 204 No Content（不需要响应体）
	c.Status(http.StatusNoContent)
}

func parseUintParam(c *gin.Context, name, message string) (uint64, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || value == 0 {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, message)
		return 0, false
	}
	return value, true
}

func (h *CommentHandler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid comment request")
	case errors.Is(err, ErrInvalidTarget):
		httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "target not found")
	case errors.Is(err, ErrForbidden):
		httpx.Abort(c, http.StatusForbidden, httpx.ErrorCodeForbidden, "forbidden")
	default:
		h.logger.Error(
			"comment request failed",
			"request_id", requestid.FromContext(c.Request.Context()),
			"error", err,
		)
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
	}
}
