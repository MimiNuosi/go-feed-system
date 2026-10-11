package hot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

type RecordViewService interface {
	RecordView(
		ctx context.Context,
		videoID uint64,
		viewerKey string,
		occurredAt time.Time,
	) (bool, error)
}

type ListHotService interface {
	ListHot(
		ctx context.Context,
		window Window,
		at time.Time,
		cursor *Cursor,
		pageSize int,
	) ([]RankedVideo, error)
}

type VideoReader interface {
	ListReadyByIDs(ctx context.Context, ids []uint64) ([]video.Video, error)
}

type UserReader interface {
	GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
}

type LikeReader interface {
	CountByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error)
	LikedVideoIDsByUser(
		ctx context.Context,
		userID uint64,
		videoIDs []uint64,
	) (map[uint64]bool, error)
}

type Handler struct {
	recordViewService RecordViewService
	listHotService    ListHotService
	videos            VideoReader
	users             UserReader
	likes             LikeReader
	logger            *slog.Logger
}

func NewHandler(
	recordViewService RecordViewService,
	listHotService ListHotService,
	videos VideoReader,
	users UserReader,
	likes LikeReader,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		recordViewService: recordViewService,
		listHotService:    listHotService,
		videos:            videos,
		users:             users,
		likes:             likes,
		logger:            logger,
	}
}

// RecordView 处理 POST /api/v1/videos/:id/view。
func (h *Handler) RecordView(c *gin.Context) {
	videoID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || videoID == 0 {
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"invalid video id",
		)
		return
	}

	_, err = h.recordViewService.RecordView(
		c.Request.Context(),
		videoID,
		h.viewerKey(c),
		time.Now(),
	)
	if err != nil {
		h.handleListHotError(c, err)
		return
	}

	// 重复浏览也按成功处理，保证接口幂等。
	c.Status(http.StatusNoContent)
}

func (h *Handler) viewerKey(c *gin.Context) string {
	if userID, ok := authctx.UserID(c.Request.Context()); ok {
		return "user:" + strconv.FormatUint(userID, 10)
	}

	clientIP := strings.TrimSpace(c.ClientIP())
	if clientIP == "" {
		clientIP = "unknown"
	}
	sum := sha256.Sum256([]byte(clientIP))
	return "ip:" + hex.EncodeToString(sum[:])
}

func (h *Handler) handleListHotError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"invalid hot feed request",
		)
	default:
		h.logger.Error(
			"list hot feed failed",
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
}

func (h *Handler) ListHot(c *gin.Context) {
	// ---- 步骤 1：解析 window ----
	// TODO(你来写)：
	// 1. c.Query("window") 拿到字符串。
	// 2. hot.ParseWindow(raw) 转成 Window。
	// 3. 失败 → 400。
	raw := c.Query("window")
	window, err := ParseWindow(raw)
	if err != nil {
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"invalid window",
		)
		return
	}
	// ---- 步骤 2：解析 page_size ----
	// TODO(你来写)：
	// 1. c.Query("page_size")，如果是空字符串，用 DefaultHotPageSize。
	// 2. 否则 strconv.Atoi。
	// 3. <=0 用默认，> MaxHotPageSize 裁剪到 MaxHotPageSize。
	// 4. 解析失败 → 400。
	rawPageSize := c.Query("page_size")
	if rawPageSize == "" {
		rawPageSize = strconv.Itoa(DefaultHotPageSize)
	}
	pageSize, err := strconv.Atoi(rawPageSize)
	if err != nil {
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"invalid page_size",
		)
		return
	}
	if pageSize <= 0 {
		pageSize = DefaultHotPageSize
	} else if pageSize > MaxHotPageSize {
		pageSize = MaxHotPageSize
	}
	// ---- 步骤 3：cursor ----
	// TODO(你来写)：
	// 1. c.Query("cursor")，非空就返回 400（"cursor not supported yet"）。
	// 2. 空字符串时传 nil 给 service。
	cursor := c.Query("cursor")
	if cursor != "" {
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"cursor not supported yet",
		)
		return
	}
	// ---- 步骤 4：调 service ----
	// TODO(你来写)：
	// ranked, err := h.listHotService.ListHot(
	//     c.Request.Context(),
	//     window,
	//     time.Now(),
	//     nil,   // cursor
	//     pageSize,
	// )
	// if err != nil {
	//     h.handleListHotError(c, err)
	//     return
	// }

	// ---- 步骤 5：暂时先简单返回 ranked ----
	// TODO(你来写)：
	// c.JSON(http.StatusOK, ranked)
	//
	// 注意：这里先不做聚合。等接口稳定后，再加 VideoReader/UserReader/LikeReader 的调用。
	ranked, err := h.listHotService.ListHot(
		c.Request.Context(),
		window,
		time.Now(),
		nil, // cursor
		pageSize,
	)
	if err != nil {
		h.handleListHotError(c, err)
		return
	}

	c.JSON(http.StatusOK, ranked)
}
