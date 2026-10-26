package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

const sseHeartbeatInterval = 20 * time.Second

type ServiceAPI interface {
	List(ctx context.Context, userID, cursor uint64, limit int) (*Page, error)
	UnreadCount(ctx context.Context, userID uint64) (int64, error)
	MarkAllRead(ctx context.Context, userID uint64) error
	Subscribe(userID uint64) (<-chan Item, func())
}

type Handler struct {
	service ServiceAPI
	logger  *slog.Logger
}

func NewHandler(service ServiceAPI, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}

func (h *Handler) List(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	cursor, ok := parseOptionalUint(c, "cursor")
	if !ok {
		return
	}
	pageSize, ok := parseOptionalInt(c, "page_size")
	if !ok {
		return
	}

	page, err := h.service.List(c.Request.Context(), userID, cursor, pageSize)
	if err != nil {
		h.handleInternalError(c, "list notifications", err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) UnreadCount(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	count, err := h.service.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		h.handleInternalError(c, "count unread notifications", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func (h *Handler) MarkAllRead(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	if err := h.service.MarkAllRead(c.Request.Context(), userID); err != nil {
		h.handleInternalError(c, "mark notifications read", err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Stream 使用 SSE 保持一条服务端到浏览器的单向事件流。
//
// EventSource 不能设置 Authorization Header，所以前端使用 fetch 读取流，
// 避免把 access token 放进 URL 后被日志、代理和历史记录保存。
func (h *Handler) Stream(c *gin.Context) {
	userID, ok := currentUserID(c)
	if !ok {
		return
	}

	events, unsubscribe := h.service.Subscribe(userID)
	defer unsubscribe()

	c.Header("Content-Type", "text/event-stream; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.Flush()

	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()

	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(c.Writer, ": heartbeat\n\n"); err != nil {
				return
			}
			c.Writer.Flush()
		case item, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(item)
			if err != nil {
				h.logger.Error(
					"marshal notification event",
					"request_id", requestid.FromContext(c.Request.Context()),
					"notification_id", item.ID,
					"error", err,
				)
				continue
			}
			if _, err := fmt.Fprintf(c.Writer, "event: notification\ndata: %s\n\n", payload); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

func currentUserID(c *gin.Context) (uint64, bool) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok || userID == 0 {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return 0, false
	}
	return userID, true
}

func parseOptionalUint(c *gin.Context, name string) (uint64, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid "+name)
		return 0, false
	}
	return value, true
}

func parseOptionalInt(c *gin.Context, name string) (int, bool) {
	raw := c.Query(name)
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid "+name)
		return 0, false
	}
	return value, true
}

func (h *Handler) handleInternalError(c *gin.Context, operation string, err error) {
	h.logger.Error(
		operation,
		"request_id", requestid.FromContext(c.Request.Context()),
		"error", err,
	)
	httpx.Abort(
		c,
		http.StatusInternalServerError,
		httpx.ErrorCodeInternal,
		"internal server error",
	)
}
