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

	// 2. 解析 cursor（核心：空字符串必须传 nil，而不是零值）
	var cursor *Cursor
	cursorStr := c.Query("cursor")
	if cursorStr != "" {
		decoded, err := decodeCursor(cursorStr)
		if err != nil {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid cursor")
			return
		}
		cursor = &decoded
	}

	// 3. 解析 page_size
	// 约定：Handler 只校验格式（非负整数），具体的默认值（20）和最大值（100）由 Service 负责
	var pageSize int
	pageSizeStr := c.Query("page_size")
	if pageSizeStr != "" {
		parsed, err := strconv.Atoi(pageSizeStr)
		if err != nil || parsed < 0 {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid page_size")
			return
		}
		pageSize = parsed
	}

	// 4. 调用 Service
	page, err := h.service.ListFollowing(c.Request.Context(), userID, cursor, pageSize)
	if err != nil {
		h.handleError(c, err)
		return
	}

	// 5. 成功响应
	c.JSON(http.StatusOK, page)
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
