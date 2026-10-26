package message

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go-feed-system/internal/user"
)

type UserReader interface {
	GetByID(ctx context.Context, id uint64) (*user.User, error)
	GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
}

type Service struct {
	repo   Repository
	users  UserReader
	hub    *Hub
	logger *slog.Logger
}

func NewService(repo Repository, users UserReader, hub *Hub, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{repo: repo, users: users, hub: hub, logger: logger}
}

func (s *Service) Send(
	ctx context.Context,
	senderID uint64,
	receiverID uint64,
	content string,
) (*Item, error) {
	if senderID == 0 || receiverID == 0 || senderID == receiverID {
		return nil, ErrInvalidInput
	}
	content = strings.TrimSpace(content)
	if content == "" || utf8.RuneCountInString(content) > MaxContentLength {
		return nil, ErrInvalidInput
	}

	if _, err := s.users.GetByID(ctx, receiverID); err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return nil, ErrRecipientNotFound
		}
		return nil, fmt.Errorf("send message: check receiver: %w", err)
	}

	item := &Message{
		SenderID:   senderID,
		ReceiverID: receiverID,
		Content:    content,
		CreatedAt:  time.Now().UTC().Truncate(time.Millisecond),
	}
	if err := s.repo.Create(ctx, item); err != nil {
		return nil, err
	}

	response := itemFromModel(*item)
	if s.hub != nil {
		s.hub.Broadcast(response)
	}
	return &response, nil
}

// List 返回从旧到新的消息页；next_cursor 是本页最旧一条消息的 ID。
func (s *Service) List(
	ctx context.Context,
	userID uint64,
	peerID uint64,
	beforeID uint64,
	pageSize int,
) (*Page, error) {
	if userID == 0 || peerID == 0 || userID == peerID {
		return nil, ErrInvalidInput
	}
	if _, err := s.users.GetByID(ctx, peerID); err != nil {
		if errors.Is(err, user.ErrNotFound) {
			return nil, ErrRecipientNotFound
		}
		return nil, fmt.Errorf("list messages: check peer: %w", err)
	}

	pageSize = normalizePageSize(pageSize)
	records, err := s.repo.ListBetween(ctx, userID, peerID, beforeID, pageSize+1)
	if err != nil {
		return nil, err
	}
	hasMore := len(records) > pageSize
	if hasMore {
		records = records[:pageSize]
	}

	items := make([]Item, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- {
		items = append(items, itemFromModel(records[i]))
	}

	nextCursor := ""
	if hasMore && len(items) > 0 {
		nextCursor = strconv.FormatUint(items[0].ID, 10)
	}
	return &Page{Items: items, NextCursor: nextCursor, HasMore: hasMore}, nil
}

func (s *Service) ListConversations(
	ctx context.Context,
	userID uint64,
) (*ConversationList, error) {
	if userID == 0 {
		return nil, ErrInvalidInput
	}

	latest, err := s.repo.ListLatestConversations(ctx, userID, DefaultConversationLimit)
	if err != nil {
		return nil, err
	}
	if len(latest) == 0 {
		return &ConversationList{Items: []Conversation{}}, nil
	}

	peerIDs := make([]uint64, 0, len(latest))
	for _, item := range latest {
		if item.SenderID == userID {
			peerIDs = append(peerIDs, item.ReceiverID)
		} else {
			peerIDs = append(peerIDs, item.SenderID)
		}
	}
	users, err := s.users.GetByIDs(ctx, peerIDs)
	if err != nil {
		return nil, fmt.Errorf("list conversations: load peers: %w", err)
	}
	unread, err := s.repo.UnreadBySender(ctx, userID, peerIDs)
	if err != nil {
		return nil, err
	}

	items := make([]Conversation, 0, len(latest))
	for _, last := range latest {
		peerID := last.SenderID
		if peerID == userID {
			peerID = last.ReceiverID
		}
		peer := Peer{ID: peerID, Username: fmt.Sprintf("用户 #%d", peerID)}
		if found := users[peerID]; found != nil {
			peer.Username = found.Username
			if found.AvatarKey != "" {
				peer.AvatarURL = fmt.Sprintf("/api/v1/users/%d/avatar", found.ID)
			}
		}
		items = append(items, Conversation{
			Peer: peer,
			LastMessage: LastMessage{
				Content:   last.Content,
				SenderID:  last.SenderID,
				CreatedAt: last.CreatedAt,
			},
			UnreadCount: unread[peerID],
		})
	}
	return &ConversationList{Items: items}, nil
}

func (s *Service) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	if userID == 0 {
		return 0, ErrInvalidInput
	}
	return s.repo.UnreadCount(ctx, userID)
}

func (s *Service) MarkConversationRead(
	ctx context.Context,
	userID uint64,
	peerID uint64,
) error {
	if userID == 0 || peerID == 0 || userID == peerID {
		return ErrInvalidInput
	}
	return s.repo.MarkConversationRead(ctx, userID, peerID)
}

func (s *Service) Subscribe(userID uint64) (<-chan Item, func()) {
	return s.hub.Subscribe(userID)
}

func normalizePageSize(pageSize int) int {
	if pageSize <= 0 {
		return DefaultPageSize
	}
	if pageSize > MaxPageSize {
		return MaxPageSize
	}
	return pageSize
}

func itemFromModel(item Message) Item {
	return Item{
		ID:         item.ID,
		SenderID:   item.SenderID,
		ReceiverID: item.ReceiverID,
		Content:    item.Content,
		CreatedAt:  item.CreatedAt,
		Read:       item.ReadAt != nil,
	}
}
