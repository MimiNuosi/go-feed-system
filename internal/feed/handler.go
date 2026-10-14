package feed

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

type ServiceAPI interface {
	ListFollowing(
		ctx context.Context,
		userID uint64,
		cursor *Cursor,
		pageSize int,
	) (*Page, error)
	ListLatest(
		ctx context.Context,
		viewerID uint64,
		cursor *Cursor,
		pageSize int,
	) (*Page, error)
}

type Handler struct {
	service ServiceAPI
	logger  *slog.Logger
}

func NewHandler(service ServiceAPI, logger *slog.Logger) *Handler {
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// ListFollowing 处理 GET /api/v1/feed/following。
func (h *Handler) ListFollowing(c *gin.Context) {
	// 1. 获取当前登录用户（Feed 接口必须登录）
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}

	// 2. 解析 cursor 与 page_size（空字符串必须传 nil 游标，而不是零值）
	cursor, pageSize, ok := parseFeedQuery(c)
	if !ok {
		return
	}

	// 3. 调用 Service
	page, err := h.service.ListFollowing(c.Request.Context(), userID, cursor, pageSize)
	if err != nil {
		h.handleError(c, err)
		return
	}

	// 5. 成功响应
	c.JSON(http.StatusOK, page)
}

// ListLatest 处理 GET /api/v1/feed/latest。
//
// 不需要登录：没有 token 时按匿名处理（viewerID = 0），
// 这样刚发布的视频对所有访客可见，是内容的冷启动入口。
func (h *Handler) ListLatest(c *gin.Context) {
	cursor, pageSize, ok := parseFeedQuery(c)
	if !ok {
		return
	}

	// 匿名访问时 authctx 里没有 userID，这里取到 0 即可
	viewerID, _ := authctx.UserID(c.Request.Context())

	page, err := h.service.ListLatest(c.Request.Context(), viewerID, cursor, pageSize)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, page)
}

// parseFeedQuery 解析 cursor 与 page_size。
//
// 两个 Feed 接口的解析规则完全一样，抽出来避免两处漂移；
// 解析失败时它会直接写出 400 并返回 ok=false。
func parseFeedQuery(c *gin.Context) (*Cursor, int, bool) {
	var cursor *Cursor
	if raw := c.Query("cursor"); raw != "" {
		decoded, err := decodeCursor(raw)
		if err != nil {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid cursor")
			return nil, 0, false
		}
		cursor = &decoded
	}

	// 约定：Handler 只校验格式（非负整数），默认值和最大值由 Service 负责
	pageSize := 0
	if raw := c.Query("page_size"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid page_size")
			return nil, 0, false
		}
		pageSize = parsed
	}

	return cursor, pageSize, true
}

// 统一错误映射
func (h *Handler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid feed request")
	default:
		// 记录完整的错误日志，带上 request_id
		h.logger.Error(
			"feed request failed",
			"request_id", requestid.FromContext(c.Request.Context()),
			"error", err,
		)
		// 对客户端统一返回 500，不暴露底层数据库错误
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
	}
}
