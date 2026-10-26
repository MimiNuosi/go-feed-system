package avatar

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
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

// Upload 处理 POST /users/me/avatar（multipart：file）。
func (h *Handler) Upload(c *gin.Context) {
	userID, ok := authctx.UserID(c.Request.Context())
	if !ok {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "missing avatar file")
		return
	}
	file, err := fileHeader.Open()
	if err != nil {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "cannot read avatar")
		return
	}
	defer file.Close()

	key, err := h.service.Update(c.Request.Context(), userID, file, fileHeader.Size)
	if err != nil {
		if errors.Is(err, ErrInvalidImage) {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid avatar image")
			return
		}
		h.logger.Error("upload avatar failed", "error", err)
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
		return
	}
	c.JSON(http.StatusOK, gin.H{"avatar_key": key})
}

// Get 处理 GET /users/:id/avatar（公开）。
func (h *Handler) Get(c *gin.Context) {
	userID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || userID == 0 {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid user id")
		return
	}

	file, contentType, err := h.service.Open(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "avatar not found")
			return
		}
		h.logger.Error("open avatar failed", "error", err)
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
		return
	}
	defer file.Close()

	c.Header("Content-Type", contentType)
	c.Header("Cache-Control", "public, max-age=300")
	http.ServeContent(c.Writer, c.Request, "avatar", time.Time{}, file)
}
