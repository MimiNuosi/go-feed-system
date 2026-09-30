package interaction

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

type LikeServiceAPI interface {
	Like(ctx context.Context, userID, videoID uint64) error
	Unlike(ctx context.Context, userID, videoID uint64) error
}

type LikeHandler struct {
	service LikeServiceAPI
	logger  *slog.Logger
}

func NewLikeHandler(service LikeServiceAPI, logger *slog.Logger) *LikeHandler {
	return &LikeHandler{
		service: service,
		logger:  logger,
	}
}

func (h *LikeHandler) Like(c *gin.Context) {
	userID, videoID, ok := h.idsFromRequest(c)
	if !ok {
		return
	}

	if err := h.service.Like(c.Request.Context(), userID, videoID); err != nil {
		h.handleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *LikeHandler) Unlike(c *gin.Context) {
	userID, videoID, ok := h.idsFromRequest(c)
	if !ok {
		return
	}

	if err := h.service.Unlike(c.Request.Context(), userID, videoID); err != nil {
		h.handleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *LikeHandler) idsFromRequest(c *gin.Context) (uint64, uint64, bool) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeUnauthorized),
			httpx.ErrorCodeUnauthorized,
			"unauthorized",
		)
		return 0, 0, false
	}

	videoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || videoID == 0 {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInvalidArgument),
			httpx.ErrorCodeInvalidArgument,
			"invalid video id",
		)
		return 0, 0, false
	}

	return userID, videoID, true
}

func (h *LikeHandler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInvalidArgument),
			httpx.ErrorCodeInvalidArgument,
			"invalid like request",
		)
	case errors.Is(err, ErrInvalidTarget):
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeNotFound),
			httpx.ErrorCodeNotFound,
			"video not found",
		)
	default:
		h.logger.Error(
			"like request failed",
			"request_id", requestid.FromContext(c.Request.Context()),
			"error", err,
		)
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInternal),
			httpx.ErrorCodeInternal,
			"internal server error",
		)
	}
}
