package hot

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	DefaultHotPageSize = 20
	MaxHotPageSize     = 100
)

type RankingRepository interface {
	RecordView(
		ctx context.Context,
		videoID uint64,
		viewerKey string,
		occurredAt time.Time,
	) (bool, error)
	TopVideoIDs(
		ctx context.Context,
		window Window,
		at time.Time,
		cursor *Cursor,
		limit int,
	) ([]RankedVideo, error)
}

type Service struct {
	repository   RankingRepository
	cache        *localHotCache
	flight       singleflight.Group
	logger       *slog.Logger
	now          func() time.Time
	queryTimeout time.Duration
}

func NewService(repository RankingRepository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repository:   repository,
		cache:        newLocalHotCache(defaultFreshTTL, defaultStaleTTL),
		logger:       logger,
		now:          time.Now,
		queryTimeout: 2 * time.Second,
	}
}

func (s *Service) RecordView(
	ctx context.Context,
	videoID uint64,
	viewerKey string,
	occurredAt time.Time,
) (bool, error) {
	if videoID == 0 {
		return false, fmt.Errorf("record view: video ID must be positive")
	}
	if viewerKey == "" {
		return false, fmt.Errorf("record view: viewer key must not be empty")
	}

	recorded, err := s.repository.RecordView(ctx, videoID, viewerKey, occurredAt)
	if err != nil {
		return false, fmt.Errorf("record view: %w", err)
	}
	return recorded, nil
}

func (s *Service) ListHot(
	ctx context.Context,
	window Window,
	at time.Time,
	cursor *Cursor,
	pageSize int,
) (*Page, error) {
	if _, err := window.Config(); err != nil {
		return nil, fmt.Errorf("list hot feed: invalid window: %w", ErrInvalidInput)
	}
	if at.IsZero() {
		return nil, fmt.Errorf("list hot feed: at is zero: %w", ErrInvalidInput)
	}
	if pageSize <= 0 {
		pageSize = DefaultHotPageSize
	}
	if pageSize > MaxHotPageSize {
		pageSize = MaxHotPageSize
	}

	videos, err := s.listRankedVideos(ctx, window, at, cursor, pageSize)
	if err != nil {
		return nil, err
	}

	return buildPage(videos, pageSize)
}

// listRankedVideos 带本地缓存和 singleflight 地取一页排名候选。
//
// 它按 pageSize+1 去取：多出来的那一条只用于判断 HasMore，不会返回给调用方。
func (s *Service) listRankedVideos(
	ctx context.Context,
	window Window,
	at time.Time,
	cursor *Cursor,
	pageSize int,
) ([]RankedVideo, error) {
	cacheKey := buildHotCacheKey(window, at, cursor, pageSize)

	if fresh, ok := s.cache.GetFresh(cacheKey, s.now()); ok {
		return cloneRankedVideos(fresh), nil
	}

	resultCh := s.flight.DoChan(cacheKey, func() (any, error) {
		if fresh, ok := s.cache.GetFresh(cacheKey, s.now()); ok {
			return cloneRankedVideos(fresh), nil
		}

		queryCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			s.queryTimeout,
		)
		defer cancel()

		// 多取一条：拿到 pageSize+1 条就说明还有下一页。
		videos, err := s.repository.TopVideoIDs(
			queryCtx,
			window,
			at,
			cursor,
			pageSize+1,
		)
		if err != nil {
			if stale, ok := s.cache.GetStale(cacheKey, s.now()); ok {
				s.logger.Warn("hot feed: using stale cache",
					"window", window, "error", err)
				return cloneRankedVideos(stale), nil
			}
			return nil, fmt.Errorf("list hot feed: top videos: %w", err)
		}

		s.cache.Set(cacheKey, videos, s.now())
		return cloneRankedVideos(videos), nil
	})

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		videos, ok := result.Val.([]RankedVideo)
		if !ok {
			return nil, fmt.Errorf("list hot feed: unexpected result type %T", result.Val)
		}
		return videos, nil
	}
}

// buildPage 把候选集裁剪成 pageSize 条，并生成下一页游标。
func buildPage(videos []RankedVideo, pageSize int) (*Page, error) {
	page := &Page{Videos: videos}
	if len(videos) <= pageSize {
		return page, nil
	}

	// 多出来的那一条只是"还有下一页"的探针。
	page.HasMore = true
	page.Videos = videos[:pageSize]

	last := page.Videos[len(page.Videos)-1]
	nextCursor, err := EncodeCursor(Cursor{
		Score:   last.Score,
		VideoID: last.VideoID,
	})
	if err != nil {
		return nil, fmt.Errorf("list hot feed: encode cursor: %w", err)
	}
	page.NextCursor = nextCursor

	return page, nil
}

func buildHotCacheKey(
	window Window,
	at time.Time,
	cursor *Cursor,
	pageSize int,
) string {
	config, err := window.Config()
	if err != nil {
		return ""
	}

	bucketStart := at.UTC().Truncate(config.BucketDuration).Unix()
	cursorPart := "first"
	if cursor != nil {
		cursorPart = fmt.Sprintf("%v:%d", cursor.Score, cursor.VideoID)
	}

	return fmt.Sprintf(
		"hot:list:%s:%d:%s:%d",
		window,
		bucketStart,
		cursorPart,
		pageSize,
	)
}

func cloneRankedVideos(videos []RankedVideo) []RankedVideo {
	return append([]RankedVideo(nil), videos...)
}
