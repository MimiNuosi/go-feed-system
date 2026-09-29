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

type FollowServiceAPI interface {
	Follow(ctx context.Context, followerID, followeeID uint64) error
	Unfollow(ctx context.Context, followerID, followeeID uint64) error
	IsFollowing(ctx context.Context, followerID, followeeID uint64) (bool, error)
}

type FollowHandler struct {
	service FollowServiceAPI
	logger  *slog.Logger
}

func NewFollowHandler(service FollowServiceAPI, logger *slog.Logger) *FollowHandler {
	return &FollowHandler{
		service: service,
		logger:  logger,
	}
}

func (h *FollowHandler) Follow(c *gin.Context) {
	followerID, followeeID, ok := h.idsFromRequest(c)
	if !ok {
		return
	}

	if err := h.service.Follow(c.Request.Context(), followerID, followeeID); err != nil {
		h.handleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *FollowHandler) Unfollow(c *gin.Context) {
	followerID, followeeID, ok := h.idsFromRequest(c)
	if !ok {
		return
	}

	if err := h.service.Unfollow(c.Request.Context(), followerID, followeeID); err != nil {
		h.handleError(c, err)
		return
	}

	c.Status(http.StatusNoContent)
}

func (h *FollowHandler) Status(c *gin.Context) {
	followerID, followeeID, ok := h.idsFromRequest(c)
	if !ok {
		return
	}

	following, err := h.service.IsFollowing(c.Request.Context(), followerID, followeeID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"following": following,
	})
}

func (h *FollowHandler) idsFromRequest(c *gin.Context) (uint64, uint64, bool) {
	followerID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeUnauthorized),
			httpx.ErrorCodeUnauthorized,
			"unauthorized",
		)
		return 0, 0, false
	}

	followeeID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || followeeID == 0 {
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInvalidArgument),
			httpx.ErrorCodeInvalidArgument,
			"invalid user id",
		)
		return 0, 0, false
	}

	return followerID, followeeID, true
}

func (h *FollowHandler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeInvalidArgument),
			httpx.ErrorCodeInvalidArgument,
			"invalid follow request",
		)
	case errors.Is(err, ErrInvalidTarget):
		httpx.Abort(
			c,
			httpx.StatusCode(httpx.ErrorCodeNotFound),
			httpx.ErrorCodeNotFound,
			"user not found",
		)
	default:
		h.logger.Error(
			"follow request failed",
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
