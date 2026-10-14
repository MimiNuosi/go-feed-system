package hot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/feedmodel"
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
	) (*Page, error)
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

	// itemCache 缓存热榜页里与访问者无关的部分（视频、作者、点赞数）。
	itemCache *itemCache

	// now 允许测试注入假时钟，生产环境就是 time.Now。
	now func() time.Time
}

func NewHandler(
	recordViewService RecordViewService,
	listHotService ListHotService,
	videos VideoReader,
	users UserReader,
	likes LikeReader,
	logger *slog.Logger,
	itemCacheTTL time.Duration,
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
		itemCache:         newItemCache(itemCacheTTL),
		now:               time.Now,
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
		h.handleError(c, err)
		return
	}

	// 重复浏览也按成功处理，保证接口幂等。
	c.Status(http.StatusNoContent)
}

func (h *Handler) viewerKey(c *gin.Context) string {
	if userID, ok := authctx.UserID(c.Request.Context()); ok {
		return "user:" + strconv.FormatUint(userID, 10)
	}

	// TODO: 匿名身份的去重精度有限。
	// 路由层已经关掉 XFF 采信，所以同一个部署里的匿名请求都来自同一个
	// 对端地址，这里实际等价于"整个部署共用一个匿名身份"。
	// 后续应改为客户端设备 ID（不可信，只能当线索）+ 限流兜底，
	// 见阶段 9 的匿名浏览去重与限流任务。
	clientIP := strings.TrimSpace(c.ClientIP())
	if clientIP == "" {
		clientIP = "unknown"
	}
	sum := sha256.Sum256([]byte(clientIP))
	return "ip:" + hex.EncodeToString(sum[:])
}

func (h *Handler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"invalid hot request",
		)
	default:
		h.logger.Error(
			"hot request failed",
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
	// 游标是客户端可控输入，解析失败一律按 invalid input 处理。
	cursor, err := DecodeCursor(c.Query("cursor"))
	if err != nil {
		httpx.Abort(
			c,
			http.StatusBadRequest,
			httpx.ErrorCodeInvalidArgument,
			"invalid cursor",
		)
		return
	}

	ranked, err := h.listHotService.ListHot(
		c.Request.Context(),
		window,
		time.Now(),
		cursor,
		pageSize,
	)
	if err != nil {
		h.handleError(c, err)
		return
	}

	page, err := h.buildHotPage(c.Request.Context(), ranked)
	if err != nil {
		h.handleError(c, err)
		return
	}

	c.JSON(http.StatusOK, page)
}

func (h *Handler) buildHotPage(
	ctx context.Context,
	page *Page,
) (*feedmodel.Page, error) {
	ranked := page.Videos

	result := &feedmodel.Page{
		Items:      []feedmodel.Item{},
		NextCursor: page.NextCursor,
		HasMore:    page.HasMore,
	}
	if len(ranked) == 0 {
		return result, nil
	}

	videoIDs := make([]uint64, 0, len(ranked))
	for _, item := range ranked {
		videoIDs = append(videoIDs, item.VideoID)
	}

	// 1. 与访问者无关的部分走短 TTL 缓存：
	//    视频元数据 + 作者名 + 点赞数。命中时这一页一条 SQL 都不发。
	now := h.now()
	items, ok := h.itemCache.Get(videoIDs, now)
	if !ok {
		var err error
		items, err = h.loadItemsForPage(ctx, ranked)
		if err != nil {
			return nil, err
		}
		h.itemCache.Set(videoIDs, items, now)
	}

	// 2. is_liked_by 与访问者有关，永远实时查。
	//    注意必须改在 Get 返回的副本上，否则会把上一个访问者的点赞状态
	//    带进缓存给所有人看。
	if userID, ok := authctx.UserID(ctx); ok && userID > 0 {
		likedMap, err := h.likes.LikedVideoIDsByUser(ctx, userID, videoIDs)
		if err != nil {
			return nil, fmt.Errorf("list hot feed: get liked status: %w", err)
		}
		for i := range items {
			items[i].IsLikedBy = likedMap[items[i].ID]
		}
	}

	result.Items = items
	return result, nil
}

// loadItemsForPage 用 3 条 SQL 补齐一页热榜的视频元数据、作者名与点赞数。
//
// 返回的条目里 IsLikedBy 一律为 false —— 它属于访问者维度，
// 由调用方按请求填充，因此这份结果可以跨用户共享和缓存。
func (h *Handler) loadItemsForPage(
	ctx context.Context,
	ranked []RankedVideo,
) ([]feedmodel.Item, error) {
	videoIDs := make([]uint64, 0, len(ranked))
	for _, item := range ranked {
		videoIDs = append(videoIDs, item.VideoID)
	}

	videos, err := h.videos.ListReadyByIDs(ctx, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("list hot feed: list videos: %w", err)
	}

	videoMap := make(map[uint64]video.Video, len(videos))
	authorIDSet := make(map[uint64]struct{}, len(videos))
	for _, item := range videos {
		videoMap[item.ID] = item
		authorIDSet[item.AuthorID] = struct{}{}
	}

	authorIDs := make([]uint64, 0, len(authorIDSet))
	for authorID := range authorIDSet {
		authorIDs = append(authorIDs, authorID)
	}

	userMap, err := h.users.GetByIDs(ctx, authorIDs)
	if err != nil {
		return nil, fmt.Errorf("list hot feed: get authors: %w", err)
	}

	likeCountMap, err := h.likes.CountByVideoIDs(ctx, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("list hot feed: count likes: %w", err)
	}

	items := make([]feedmodel.Item, 0, len(ranked))
	for _, rankedVideo := range ranked {
		videoItem, ok := videoMap[rankedVideo.VideoID]
		if !ok {
			continue
		}

		username := "已注销用户"
		if author, ok := userMap[videoItem.AuthorID]; ok && author != nil {
			username = author.Username
		}
		items = append(items, feedmodel.Item{
			ID: videoItem.ID,
			Author: feedmodel.AuthorInfo{
				ID:       videoItem.AuthorID,
				Username: username,
			},
			Title:       videoItem.Title,
			Description: videoItem.Description,
			ContentType: videoItem.ContentType,
			CreatedAt:   videoItem.CreatedAt,
			ViewCount:   videoItem.ViewCount,
			LikeCount:   likeCountMap[videoItem.ID],
		})
	}

	return items, nil
}
