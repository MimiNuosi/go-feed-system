package notification

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"go-feed-system/internal/realtime"
	"go-feed-system/internal/user"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type Service struct {
	repo      Repository
	users     UserReader
	hub       *Hub
	publisher realtime.Publisher
	logger    *slog.Logger
}

type UserReader interface {
	GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
}

func NewService(
	repo Repository,
	users UserReader,
	hub *Hub,
	publisher realtime.Publisher,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		repo:      repo,
		users:     users,
		hub:       hub,
		publisher: publisher,
		logger:    logger,
	}
}

// Notify 写入通知并推送给在线订阅者；自己操作自己不通知。
func (s *Service) Notify(
	ctx context.Context,
	recipientID uint64,
	actorID uint64,
	kind string,
	videoID uint64,
	content string,
) error {
	if recipientID == 0 || actorID == 0 || recipientID == actorID {
		return nil
	}

	item := &Notification{
		UserID:    recipientID,
		ActorID:   actorID,
		Kind:      kind,
		Content:   content,
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	if videoID != 0 {
		item.VideoID = &videoID
	}
	if err := s.repo.Create(ctx, item); err != nil {
		s.logger.Warn(
			"create notification",
			"recipient_id", recipientID,
			"actor_id", actorID,
			"kind", kind,
			"video_id", videoID,
			"error", err,
		)
		return err
	}
	if s.hub != nil || s.publisher != nil {
		event, err := s.toItem(ctx, *item)
		if err != nil {
			// 通知已经落库，在线推送只是加速通道；补操作者信息失败时降级推送。
			s.logger.Warn(
				"enrich notification event",
				"notification_id", item.ID,
				"actor_id", actorID,
				"error", err,
			)
			event = itemFromRecord(*item, nil)
		}
		s.publishOrBroadcast(ctx, event)
	}
	return nil
}

func (s *Service) publishOrBroadcast(ctx context.Context, item Item) {
	if s.publisher == nil {
		if s.hub != nil {
			s.hub.Broadcast(item)
		}
		return
	}

	envelope, err := realtime.NewEnvelope(
		realtime.EventNotificationCreated,
		item.UserID,
		item,
	)
	if err == nil {
		err = s.publisher.Publish(ctx, envelope)
	}
	if err == nil {
		return
	}

	// Redis 发布失败时退回本实例 Hub，远端实例靠数据库页面补查。
	s.logger.Warn(
		"publish realtime notification",
		"notification_id", item.ID,
		"recipient_id", item.UserID,
		"error", err,
	)
	if s.hub != nil {
		s.hub.Broadcast(item)
	}
}

func (s *Service) List(
	ctx context.Context,
	userID uint64,
	cursor uint64,
	limit int,
) (*Page, error) {
	if userID == 0 {
		return nil, fmt.Errorf("user id is zero")
	}
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	items, err := s.repo.ListByUser(ctx, userID, cursor, limit+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}

	responseItems, err := s.toItems(ctx, items)
	if err != nil {
		return nil, err
	}

	nextCursor := ""
	if hasMore && len(responseItems) > 0 {
		nextCursor = strconv.FormatUint(responseItems[len(responseItems)-1].ID, 10)
	}

	return &Page{
		Items:      responseItems,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}, nil
}

func (s *Service) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	return s.repo.UnreadCount(ctx, userID)
}

func (s *Service) MarkAllRead(ctx context.Context, userID uint64) error {
	return s.repo.MarkAllRead(ctx, userID)
}

func (s *Service) Subscribe(userID uint64) (<-chan Item, func()) {
	return s.hub.Subscribe(userID)
}

func (s *Service) toItems(ctx context.Context, records []Notification) ([]Item, error) {
	if len(records) == 0 {
		return []Item{}, nil
	}

	userMap := make(map[uint64]*user.User)
	if s.users != nil {
		actorIDs := make([]uint64, 0, len(records))
		seen := make(map[uint64]struct{}, len(records))
		for _, record := range records {
			if _, ok := seen[record.ActorID]; ok {
				continue
			}
			seen[record.ActorID] = struct{}{}
			actorIDs = append(actorIDs, record.ActorID)
		}

		var err error
		userMap, err = s.users.GetByIDs(ctx, actorIDs)
		if err != nil {
			return nil, fmt.Errorf("load notification actors: %w", err)
		}
	}

	items := make([]Item, 0, len(records))
	for _, record := range records {
		items = append(items, itemFromRecord(record, userMap[record.ActorID]))
	}
	return items, nil
}

func (s *Service) toItem(ctx context.Context, record Notification) (Item, error) {
	items, err := s.toItems(ctx, []Notification{record})
	if err != nil {
		return Item{}, err
	}
	return items[0], nil
}

func itemFromRecord(record Notification, actor *user.User) Item {
	username := fmt.Sprintf("用户 #%d", record.ActorID)
	avatarURL := ""
	if actor != nil {
		username = actor.Username
		if actor.AvatarKey != "" {
			avatarURL = fmt.Sprintf("/api/v1/users/%d/avatar", actor.ID)
		}
	}

	return Item{
		ID:     record.ID,
		UserID: record.UserID,
		Actor: Actor{
			ID:        record.ActorID,
			Username:  username,
			AvatarURL: avatarURL,
		},
		Kind:      record.Kind,
		VideoID:   record.VideoID,
		Content:   record.Content,
		CreatedAt: record.CreatedAt,
		Read:      record.ReadAt != nil,
	}
}
