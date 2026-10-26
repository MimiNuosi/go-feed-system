package message

import (
	"context"
	"encoding/json"
	"errors"
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
	Send(ctx context.Context, senderID, receiverID uint64, content string) (*Item, error)
	List(
		ctx context.Context,
		userID uint64,
		peerID uint64,
		beforeID uint64,
		pageSize int,
	) (*Page, error)
	ListConversations(ctx context.Context, userID uint64) (*ConversationList, error)
	UnreadCount(ctx context.Context, userID uint64) (int64, error)
	MarkConversationRead(ctx context.Context, userID uint64, peerID uint64) error
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

type sendRequest struct {
	ReceiverID uint64 `json:"receiver_id"`
	Content    string `json:"content"`
}

func (h *Handler) Send(c *gin.Context) {
	userID, ok := messageUserID(c)
	if !ok {
		return
	}
	var request sendRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid request body")
		return
	}

	item, err := h.service.Send(c.Request.Context(), userID, request.ReceiverID, request.Content)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, item)
}

func (h *Handler) List(c *gin.Context) {
	userID, ok := messageUserID(c)
	if !ok {
		return
	}
	peerID, ok := parseID(c.Param("peer_id"))
	if !ok {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid peer id")
		return
	}
	beforeID, ok := parseOptionalUint64(c.Query("cursor"))
	if !ok {
		return
	}
	pageSize, ok := parseOptionalInt(c.Query("page_size"))
	if !ok {
		return
	}

	page, err := h.service.List(
		c.Request.Context(),
		userID,
		peerID,
		beforeID,
		pageSize,
	)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func (h *Handler) Conversations(c *gin.Context) {
	userID, ok := messageUserID(c)
	if !ok {
		return
	}
	items, err := h.service.ListConversations(c.Request.Context(), userID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *Handler) UnreadCount(c *gin.Context) {
	userID, ok := messageUserID(c)
	if !ok {
		return
	}
	count, err := h.service.UnreadCount(c.Request.Context(), userID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"count": count})
}

func (h *Handler) MarkRead(c *gin.Context) {
	userID, ok := messageUserID(c)
	if !ok {
		return
	}
	peerID, ok := parseID(c.Param("peer_id"))
	if !ok {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid peer id")
		return
	}
	if err := h.service.MarkConversationRead(c.Request.Context(), userID, peerID); err != nil {
		h.handleError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) Stream(c *gin.Context) {
	userID, ok := messageUserID(c)
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
					"marshal message event",
					"request_id", requestid.FromContext(c.Request.Context()),
					"message_id", item.ID,
					"error", err,
				)
				continue
			}
			if _, err := fmt.Fprintf(c.Writer, "event: message\ndata: %s\n\n", payload); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

func messageUserID(c *gin.Context) (uint64, bool) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok || userID == 0 {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return 0, false
	}
	return userID, true
}

func parseID(raw string) (uint64, bool) {
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil && value > 0
}

func parseOptionalUint64(raw string) (uint64, bool) {
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil
}

func parseOptionalInt(raw string) (int, bool) {
	if raw == "" {
		return 0, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 0
}

func (h *Handler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid message request")
	case errors.Is(err, ErrRecipientNotFound):
		httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "user not found")
	default:
		h.logger.Error(
			"message request failed",
			"request_id", requestid.FromContext(c.Request.Context()),
			"error", err,
		)
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
	}
}
