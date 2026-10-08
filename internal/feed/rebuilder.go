package feed

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

const (
	DefaultRebuildBatchSize = 500
	DefaultRebuildWindow    = 7 * 24 * time.Hour
)

// RebuildRepository 是 Inbox 重建需要的最小查询能力。
type RebuildRepository interface {
	ListFollowing(
		ctx context.Context,
		followerID uint64,
		cursor *Cursor,
		limit int,
	) ([]VideoRecord, error)
	ListBigAuthorIDs(
		ctx context.Context,
		followerID uint64,
		threshold int,
	) ([]uint64, error)
}

// RebuildInbox 是 Inbox 重建需要的最小写入能力。
type RebuildInbox interface {
	Add(
		ctx context.Context,
		userIDs []uint64,
		videoID uint64,
		publishedAt time.Time,
	) error
	Trim(ctx context.Context, userID uint64) error
}

type RebuildOptions struct {
	UserIDs   []uint64
	Since     time.Time
	BatchSize int
	Execute   bool
}

type RebuildResult struct {
	UserID   uint64
	Scanned  int
	Eligible int
	Written  int
	DryRun   bool
}

type Rebuilder struct {
	repository              RebuildRepository
	inbox                   RebuildInbox
	fanoutFollowerThreshold int
	logger                  *slog.Logger
}

func NewRebuilder(
	repository RebuildRepository,
	inbox RebuildInbox,
	fanoutFollowerThreshold int,
	logger *slog.Logger,
) *Rebuilder {
	if fanoutFollowerThreshold <= 0 {
		fanoutFollowerThreshold = DefaultFanoutFollowerThreshold
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Rebuilder{
		repository:              repository,
		inbox:                   inbox,
		fanoutFollowerThreshold: fanoutFollowerThreshold,
		logger:                  logger,
	}
}

// Rebuild 为指定用户重建 Redis Feed Inbox。
func (r *Rebuilder) Rebuild(
	ctx context.Context,
	options RebuildOptions,
) ([]RebuildResult, error) {
	if r.repository == nil {
		return nil, fmt.Errorf("rebuild feed inbox: repository is nil")
	}
	if r.inbox == nil {
		return nil, fmt.Errorf("rebuild feed inbox: inbox is nil")
	}

	options, err := normalizeRebuildOptions(options)
	if err != nil {
		return nil, err
	}

	results := make([]RebuildResult, 0, len(options.UserIDs))
	for _, userID := range options.UserIDs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("rebuild feed inbox: context canceled: %w", err)
		}

		result, err := r.rebuildUser(ctx, userID, options)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}

	return results, nil
}

func (r *Rebuilder) rebuildUser(
	ctx context.Context,
	userID uint64,
	options RebuildOptions,
) (RebuildResult, error) {
	result := RebuildResult{
		UserID: userID,
		DryRun: !options.Execute,
	}

	bigAuthorIDList, err := r.repository.ListBigAuthorIDs(ctx, userID, r.fanoutFollowerThreshold)
	if err != nil {
		return result, fmt.Errorf("rebuild feed inbox: list big authors: %w", err)
	}
	bigAuthorIDs := make(map[uint64]struct{}, len(bigAuthorIDList))
	for _, id := range bigAuthorIDList {
		bigAuthorIDs[id] = struct{}{}
	}
	cursor := (*Cursor)(nil)

	reachedSince := false
	for {
		results, err := r.repository.ListFollowing(ctx, userID, cursor, options.BatchSize)
		if err != nil {
			return result, fmt.Errorf("rebuild feed inbox: list following: %w", err)
		}
		if len(results) == 0 {
			break
		}

		for _, record := range results {
			if record.ID == 0 || record.CreatedAt.IsZero() {
				continue
			}
			result.Scanned++
			if record.CreatedAt.Before(options.Since) {
				reachedSince = true
				break
			}
			if _, ok := bigAuthorIDs[record.AuthorID]; ok {
				continue
			}
			result.Eligible++
			if options.Execute {
				err := r.inbox.Add(ctx, []uint64{userID}, record.ID, record.CreatedAt)
				if err != nil {
					return result, fmt.Errorf("rebuild feed inbox: add to inbox: %w", err)
				}
				result.Written++
			}
		}

		if reachedSince || len(results) < options.BatchSize {
			break
		}

		last := results[len(results)-1]
		cursor = &Cursor{
			ID:        last.ID,
			CreatedAt: last.CreatedAt,
		}
	}

	if options.Execute {
		if err := r.inbox.Trim(ctx, userID); err != nil {
			return result, fmt.Errorf("rebuild feed inbox: trim inbox: %w", err)
		}
	}
	return result, nil
}

func normalizeRebuildOptions(options RebuildOptions) (RebuildOptions, error) {
	if len(options.UserIDs) == 0 {
		return RebuildOptions{}, fmt.Errorf("rebuild feed inbox: no user IDs provided")
	}
	if options.Since.IsZero() {
		return RebuildOptions{}, fmt.Errorf("rebuild feed inbox: since must not be zero")
	}
	if options.BatchSize <= 0 {
		options.BatchSize = DefaultRebuildBatchSize
	}

	seen := make(map[uint64]struct{}, len(options.UserIDs))
	userIDs := make([]uint64, 0, len(options.UserIDs))
	for _, userID := range options.UserIDs {
		if userID == 0 {
			return RebuildOptions{}, fmt.Errorf("rebuild feed inbox: user ID must be positive")
		}
		if _, ok := seen[userID]; ok {
			continue
		}
		seen[userID] = struct{}{}
		userIDs = append(userIDs, userID)
	}
	options.UserIDs = userIDs

	return options, nil
}
