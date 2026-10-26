// Package profile 提供用户主页需要的聚合查询。
//
// 它只做"读模型"：用户、作品数、粉丝/关注数、获赞数、粉丝/关注列表、
// 我赞过的视频。写操作仍然留在 user / interaction / video 各自的领域里。
package profile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"go-feed-system/internal/interaction"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/authctx"
	"go-feed-system/pkg/feedmodel"
	"go-feed-system/pkg/httpx"
	"go-feed-system/pkg/requestid"
)

type UserReader interface {
	GetByID(ctx context.Context, id uint64) (*user.User, error)
	GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
}

type VideoReader interface {
	CountByAuthor(ctx context.Context, authorID uint64) (int64, error)
	ListReadyByIDs(ctx context.Context, ids []uint64) ([]video.Video, error)
}

type FollowReader interface {
	Counts(ctx context.Context, userID uint64) (int64, int64, error)
	IsFollowing(ctx context.Context, followerID, followeeID uint64) (bool, error)
	ListFollowers(ctx context.Context, userID uint64, cursor uint64, limit int) ([]interaction.FollowEdge, bool, error)
	ListFollowing(ctx context.Context, userID uint64, cursor uint64, limit int) ([]interaction.FollowEdge, bool, error)
}

type LikeReader interface {
	ListLikedVideoEdges(ctx context.Context, userID uint64, cursor uint64, limit int) ([]interaction.LikeEdge, bool, error)
	CountLikesReceivedByAuthor(ctx context.Context, authorID uint64) (int64, error)
}

type LikeCountReader interface {
	CountByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error)
}

type Handler struct {
	users   UserReader
	videos  VideoReader
	follows FollowReader
	likes   LikeReader
	likeCnt LikeCountReader
	logger  *slog.Logger
}

func NewHandler(
	users UserReader,
	videos VideoReader,
	follows FollowReader,
	likes LikeReader,
	likeCnt LikeCountReader,
	logger *slog.Logger,
) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		users: users, videos: videos, follows: follows,
		likes: likes, likeCnt: likeCnt, logger: logger,
	}
}

type profileResponse struct {
	User          userItem `json:"user"`
	Followers     int64    `json:"followers"`
	Following     int64    `json:"following"`
	Videos        int64    `json:"videos"`
	LikesReceived int64    `json:"likes_received"`
	IsFollowing   bool     `json:"is_following"`
	IsMe          bool     `json:"is_me"`
}

type userItem struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
}

type userListResponse struct {
	Items      []userItem `json:"items"`
	NextCursor string     `json:"next_cursor,omitempty"`
	HasMore    bool       `json:"has_more"`
}

// GetProfile 返回用户主页的聚合信息。
func (h *Handler) GetProfile(c *gin.Context) {
	targetID, ok := h.parseID(c)
	if !ok {
		return
	}

	target, err := h.users.GetByID(c.Request.Context(), targetID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	ctx := c.Request.Context()
	followers, following, err := h.follows.Counts(ctx, targetID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	videoCount, err := h.videos.CountByAuthor(ctx, targetID)
	if err != nil {
		h.handleError(c, err)
		return
	}
	likesReceived, err := h.likes.CountLikesReceivedByAuthor(ctx, targetID)
	if err != nil {
		h.handleError(c, err)
		return
	}

	viewerID, _ := authctx.UserID(ctx)
	isFollowing := false
	if viewerID > 0 && viewerID != targetID {
		isFollowing, err = h.follows.IsFollowing(ctx, viewerID, targetID)
		if err != nil {
			h.handleError(c, err)
			return
		}
	}

	c.JSON(http.StatusOK, profileResponse{
		User:          userItem{ID: target.ID, Username: target.Username},
		Followers:     followers,
		Following:     following,
		Videos:        videoCount,
		LikesReceived: likesReceived,
		IsFollowing:   isFollowing,
		IsMe:          viewerID == targetID,
	})
}

// ListFollowers 返回粉丝列表（按关注时间倒序）。
func (h *Handler) ListFollowers(c *gin.Context) {
	targetID, ok := h.parseID(c)
	if !ok {
		return
	}
	cursor, pageSize, ok := h.parsePage(c)
	if !ok {
		return
	}

	edges, hasMore, err := h.follows.ListFollowers(c.Request.Context(), targetID, cursor, pageSize)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildUserList(edges, hasMore))
}

// ListFollowing 返回关注的用户列表（按关注时间倒序）。
func (h *Handler) ListFollowing(c *gin.Context) {
	targetID, ok := h.parseID(c)
	if !ok {
		return
	}
	cursor, pageSize, ok := h.parsePage(c)
	if !ok {
		return
	}

	edges, hasMore, err := h.follows.ListFollowing(c.Request.Context(), targetID, cursor, pageSize)
	if err != nil {
		h.handleError(c, err)
		return
	}
	c.JSON(http.StatusOK, buildUserList(edges, hasMore))
}

// ListLikedVideos 返回"我赞过的视频"，只允许查看自己的。
func (h *Handler) ListLikedVideos(c *gin.Context) {
	viewerID, ok := authctx.UserID(c.Request.Context())
	if !ok || viewerID == 0 {
		httpx.Abort(c, http.StatusUnauthorized, httpx.ErrorCodeUnauthorized, "unauthorized")
		return
	}
	cursor, pageSize, ok := h.parsePage(c)
	if !ok {
		return
	}

	ctx := c.Request.Context()
	edges, hasMore, err := h.likes.ListLikedVideoEdges(ctx, viewerID, cursor, pageSize)
	if err != nil {
		h.handleError(c, err)
		return
	}

	videoIDs := make([]uint64, 0, len(edges))
	for _, edge := range edges {
		videoIDs = append(videoIDs, edge.VideoID)
	}

	videos, err := h.videos.ListReadyByIDs(ctx, videoIDs)
	if err != nil {
		h.handleError(c, err)
		return
	}
	videoMap := make(map[uint64]video.Video, len(videos))
	authorIDs := make([]uint64, 0, len(videos))
	for _, item := range videos {
		videoMap[item.ID] = item
		authorIDs = append(authorIDs, item.AuthorID)
	}
	authors, err := h.users.GetByIDs(ctx, authorIDs)
	if err != nil {
		h.handleError(c, err)
		return
	}
	likeCounts, err := h.likeCnt.CountByVideoIDs(ctx, videoIDs)
	if err != nil {
		h.handleError(c, err)
		return
	}

	items := make([]feedmodel.Item, 0, len(edges))
	for _, edge := range edges {
		item, exists := videoMap[edge.VideoID]
		if !exists {
			continue
		}
		username := "已注销用户"
		if author, exists := authors[item.AuthorID]; exists && author != nil {
			username = author.Username
		}
		items = append(items, feedmodel.Item{
			ID: item.ID,
			Author: feedmodel.AuthorInfo{
				ID:       item.AuthorID,
				Username: username,
			},
			Title:       item.Title,
			Description: item.Description,
			ContentType: item.ContentType,
			CreatedAt:   item.CreatedAt,
			ViewCount:   item.ViewCount,
			LikeCount:   likeCounts[item.ID],
			IsLikedBy:   true,
		})
	}

	nextCursor := ""
	if hasMore && len(edges) > 0 {
		nextCursor = strconv.FormatUint(edges[len(edges)-1].CursorID, 10)
	}
	c.JSON(http.StatusOK, feedmodel.Page{Items: items, NextCursor: nextCursor, HasMore: hasMore})
}

func buildUserList(edges []interaction.FollowEdge, hasMore bool) userListResponse {
	items := make([]userItem, 0, len(edges))
	for _, edge := range edges {
		items = append(items, userItem{ID: edge.UserID, Username: edge.Username})
	}
	nextCursor := ""
	if hasMore && len(edges) > 0 {
		nextCursor = strconv.FormatUint(edges[len(edges)-1].CursorID, 10)
	}
	return userListResponse{Items: items, NextCursor: nextCursor, HasMore: hasMore}
}

func (h *Handler) parseID(c *gin.Context) (uint64, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid user id")
		return 0, false
	}
	return id, true
}

func (h *Handler) parsePage(c *gin.Context) (uint64, int, bool) {
	var cursor uint64
	if raw := c.Query("cursor"); raw != "" {
		value, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid cursor")
			return 0, 0, false
		}
		cursor = value
	}

	pageSize := interaction.DefaultProfilePageSize
	if raw := c.Query("page_size"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid page_size")
			return 0, 0, false
		}
		if value > interaction.MaxProfilePageSize {
			value = interaction.MaxProfilePageSize
		}
		pageSize = value
	}
	return cursor, pageSize, true
}

func (h *Handler) handleError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, user.ErrNotFound):
		httpx.Abort(c, http.StatusNotFound, httpx.ErrorCodeNotFound, "user not found")
	case errors.Is(err, interaction.ErrInvalidInput):
		httpx.Abort(c, http.StatusBadRequest, httpx.ErrorCodeInvalidArgument, "invalid request")
	default:
		h.logger.Error(
			"profile request failed",
			"request_id", requestid.FromContext(c.Request.Context()),
			"error", fmt.Errorf("profile: %w", err),
		)
		httpx.Abort(c, http.StatusInternalServerError, httpx.ErrorCodeInternal, "internal server error")
	}
}
