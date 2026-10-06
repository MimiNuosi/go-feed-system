package interaction

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"go-feed-system/internal/user"
)

type UserReader interface {
	GetByID(ctx context.Context, id uint64) (*user.User, error)
}

type BigAuthorCacheInvalidator interface {
	Delete(ctx context.Context, userID uint64) error
}

type FollowService struct {
	follows        FollowRepository
	users          UserReader
	bigAuthorCache BigAuthorCacheInvalidator
	logger         *slog.Logger
}

func NewFollowService(
	follows FollowRepository,
	users UserReader,
	bigAuthorCache BigAuthorCacheInvalidator,
	logger *slog.Logger,
) *FollowService {
	return &FollowService{
		follows:        follows,
		users:          users,
		bigAuthorCache: bigAuthorCache,
		logger:         logger,
	}
}

func (s *FollowService) Follow(ctx context.Context, followerID, followeeID uint64) error {
	// 1. 业务校验：不能关注自己
	if followerID == 0 || followeeID == 0 {
		return ErrInvalidInput
	}
	if followerID == followeeID {
		return ErrInvalidInput
	}

	// 2. 数据校验：确认被关注用户存在
	// followerID 不需要查，因为它是通过 JWT 鉴权中间件传进来的，必定有效
	_, err := s.users.GetByID(ctx, followeeID)
	if err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return ErrInvalidTarget // 业务层转换为交互模块的 ErrNotFound
		}
		return fmt.Errorf("follow user: check user: %w", err)
	}

	// 3. 执行插入
	err = s.follows.Create(ctx, followerID, followeeID)
	if err != nil {
		// 核心幂等逻辑：如果已经关注过了，视为成功
		if errors.Is(err, ErrAlreadyFollowing) {
			s.invalidateBigAuthors(ctx, followerID)
			return nil
		}
		return fmt.Errorf("follow user: %w", err)
	}

	s.invalidateBigAuthors(ctx, followerID)
	return nil
}

func (s *FollowService) Unfollow(ctx context.Context, followerID, followeeID uint64) error {
	// 1. 内存校验：ID 必须合法
	if followerID == 0 || followeeID == 0 {
		return ErrInvalidInput
	}
	// 注意：取消关注自己的逻辑通常也禁止，或者允许通过（取决于业务）。
	// 但既然 Follow 禁止了，这里建议保持一致。
	if followerID == followeeID {
		return ErrInvalidInput
	}

	// 2. 直接调用 Delete
	// Repository 层的 Delete 已经实现了幂等（删不存在的记录也返回 nil），
	// 所以 Service 层直接透传即可，不需要再查一遍用户是否存在。
	err := s.follows.Delete(ctx, followerID, followeeID)
	if err != nil {
		return fmt.Errorf("unfollow user: delete relation: %w", err)
	}

	s.invalidateBigAuthors(ctx, followerID)
	return nil
}

func (s *FollowService) IsFollowing(ctx context.Context, followerID, followeeID uint64) (bool, error) {
	if followerID == 0 || followeeID == 0 {
		return false, ErrInvalidInput
	}

	following, err := s.follows.Exists(ctx, followerID, followeeID)
	if err != nil {
		return false, fmt.Errorf("check follow status: %w", err)
	}

	return following, nil
}

func (s *FollowService) invalidateBigAuthors(ctx context.Context, userID uint64) {
	if s.bigAuthorCache == nil {
		return
	}
	if err := s.bigAuthorCache.Delete(ctx, userID); err != nil {
		logger := s.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("invalidate big author cache", "user_id", userID, "error", err)
	}
}
