package message

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go-feed-system/internal/user"
)

type fakeRepository struct {
	createFunc           func(ctx context.Context, item *Message) error
	listBetweenFunc      func(ctx context.Context, userA, userB, beforeID uint64, limit int) ([]Message, error)
	listLatestFunc       func(ctx context.Context, userID uint64, limit int) ([]Message, error)
	unreadCountFunc      func(ctx context.Context, userID uint64) (int64, error)
	unreadBySenderFunc   func(ctx context.Context, userID uint64, senderIDs []uint64) (map[uint64]int64, error)
	markConversationFunc func(ctx context.Context, userID, peerID uint64) error
	lastBeforeID         uint64
	lastLimit            int
	lastMarkUserID       uint64
	lastMarkPeerID       uint64
}

func (f *fakeRepository) Create(ctx context.Context, item *Message) error {
	if f.createFunc != nil {
		return f.createFunc(ctx, item)
	}
	item.ID = 1
	return nil
}

func (f *fakeRepository) ListBetween(
	ctx context.Context,
	userA, userB, beforeID uint64,
	limit int,
) ([]Message, error) {
	f.lastBeforeID = beforeID
	f.lastLimit = limit
	if f.listBetweenFunc != nil {
		return f.listBetweenFunc(ctx, userA, userB, beforeID, limit)
	}
	return []Message{}, nil
}

func (f *fakeRepository) ListLatestConversations(
	ctx context.Context,
	userID uint64,
	limit int,
) ([]Message, error) {
	f.lastLimit = limit
	if f.listLatestFunc != nil {
		return f.listLatestFunc(ctx, userID, limit)
	}
	return []Message{}, nil
}

func (f *fakeRepository) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	if f.unreadCountFunc != nil {
		return f.unreadCountFunc(ctx, userID)
	}
	return 0, nil
}

func (f *fakeRepository) UnreadBySender(
	ctx context.Context,
	userID uint64,
	senderIDs []uint64,
) (map[uint64]int64, error) {
	if f.unreadBySenderFunc != nil {
		return f.unreadBySenderFunc(ctx, userID, senderIDs)
	}
	return map[uint64]int64{}, nil
}

func (f *fakeRepository) MarkConversationRead(
	ctx context.Context,
	userID, peerID uint64,
) error {
	f.lastMarkUserID = userID
	f.lastMarkPeerID = peerID
	if f.markConversationFunc != nil {
		return f.markConversationFunc(ctx, userID, peerID)
	}
	return nil
}

type fakeUserReader struct {
	getByIDFunc  func(ctx context.Context, id uint64) (*user.User, error)
	getByIDsFunc func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
	lastIDs      []uint64
}

func (f *fakeUserReader) GetByID(ctx context.Context, id uint64) (*user.User, error) {
	if f.getByIDFunc != nil {
		return f.getByIDFunc(ctx, id)
	}
	return &user.User{ID: id, Username: "peer"}, nil
}

func (f *fakeUserReader) GetByIDs(
	ctx context.Context,
	ids []uint64,
) (map[uint64]*user.User, error) {
	f.lastIDs = append([]uint64(nil), ids...)
	if f.getByIDsFunc != nil {
		return f.getByIDsFunc(ctx, ids)
	}
	return map[uint64]*user.User{}, nil
}

func TestServiceSend(t *testing.T) {
	tests := []struct {
		name          string
		senderID      uint64
		receiverID    uint64
		content       string
		userErr       error
		repoErr       error
		wantErr       error
		wantRepoCalls int
		wantBroadcast bool
		wantContent   string
	}{
		{
			name:          "成功发送",
			senderID:      1,
			receiverID:    2,
			content:       "  hello  ",
			wantRepoCalls: 1,
			wantBroadcast: true,
			wantContent:   "hello",
		},
		{
			name:       "不能发给自己",
			senderID:   1,
			receiverID: 1,
			content:    "hello",
			wantErr:    ErrInvalidInput,
		},
		{
			name:       "空内容",
			senderID:   1,
			receiverID: 2,
			content:    "   ",
			wantErr:    ErrInvalidInput,
		},
		{
			name:       "超长内容",
			senderID:   1,
			receiverID: 2,
			content:    strings.Repeat("字", MaxContentLength+1),
			wantErr:    ErrInvalidInput,
		},
		{
			name:       "收件人不存在",
			senderID:   1,
			receiverID: 9,
			content:    "hello",
			userErr:    user.ErrNotFound,
			wantErr:    ErrRecipientNotFound,
		},
		{
			name:          "仓储写入失败",
			senderID:      1,
			receiverID:    2,
			content:       "hello",
			repoErr:       errors.New("db down"),
			wantRepoCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repoCalls := 0
			repo := &fakeRepository{
				createFunc: func(ctx context.Context, item *Message) error {
					repoCalls++
					item.ID = 8
					return tt.repoErr
				},
			}
			users := &fakeUserReader{
				getByIDFunc: func(ctx context.Context, id uint64) (*user.User, error) {
					return nil, tt.userErr
				},
			}
			hub := NewHub()
			service := NewService(repo, users, hub, newMessageDiscardLogger())
			var events <-chan Item
			if tt.wantBroadcast {
				events, _ = hub.Subscribe(tt.receiverID)
			}

			item, err := service.Send(context.Background(), tt.senderID, tt.receiverID, tt.content)
			if !sameError(err, tt.wantErr, tt.repoErr) {
				t.Fatalf("Send() error = %v, want %v", err, tt.wantErr)
			}
			if repoCalls != tt.wantRepoCalls {
				t.Fatalf("repo calls = %d, want %d", repoCalls, tt.wantRepoCalls)
			}
			if tt.wantErr != nil || tt.repoErr != nil {
				return
			}
			if item.Content != tt.wantContent {
				t.Errorf("content = %q, want %q", item.Content, tt.wantContent)
			}
			if !tt.wantBroadcast {
				return
			}
			select {
			case event := <-events:
				if event.ID != 8 || event.Content != tt.wantContent {
					t.Errorf("broadcast = %+v", event)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for broadcast")
			}
		})
	}
}

func TestServiceList(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeRepository{
		listBetweenFunc: func(
			ctx context.Context,
			userA, userB, beforeID uint64,
			limit int,
		) ([]Message, error) {
			return []Message{
				{ID: 4, SenderID: 1, ReceiverID: 2, Content: "4", CreatedAt: now},
				{ID: 3, SenderID: 2, ReceiverID: 1, Content: "3", CreatedAt: now},
				{ID: 2, SenderID: 1, ReceiverID: 2, Content: "2", CreatedAt: now},
			}, nil
		},
	}
	service := NewService(repo, &fakeUserReader{}, NewHub(), newMessageDiscardLogger())

	page, err := service.List(context.Background(), 1, 2, 10, 2)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repo.lastBeforeID != 10 || repo.lastLimit != 3 {
		t.Fatalf("repo args before=%d limit=%d", repo.lastBeforeID, repo.lastLimit)
	}
	if !page.HasMore || page.NextCursor != "3" {
		t.Fatalf("page cursor=%q hasMore=%v", page.NextCursor, page.HasMore)
	}
	if len(page.Items) != 2 || page.Items[0].ID != 3 || page.Items[1].ID != 4 {
		t.Fatalf("items are not oldest-to-newest: %+v", page.Items)
	}
}

func TestServiceListConversations(t *testing.T) {
	now := time.Now().UTC()
	repo := &fakeRepository{
		listLatestFunc: func(ctx context.Context, userID uint64, limit int) ([]Message, error) {
			return []Message{
				{ID: 9, SenderID: 3, ReceiverID: 1, Content: "newest", CreatedAt: now},
				{ID: 8, SenderID: 1, ReceiverID: 2, Content: "older", CreatedAt: now.Add(-time.Minute)},
			}, nil
		},
		unreadBySenderFunc: func(
			ctx context.Context,
			userID uint64,
			senderIDs []uint64,
		) (map[uint64]int64, error) {
			return map[uint64]int64{3: 2}, nil
		},
	}
	users := &fakeUserReader{
		getByIDsFunc: func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
			return map[uint64]*user.User{
				2: {ID: 2, Username: "bob"},
				3: {ID: 3, Username: "carol", AvatarKey: "avatar/c"},
			}, nil
		},
	}
	service := NewService(repo, users, NewHub(), newMessageDiscardLogger())

	list, err := service.ListConversations(context.Background(), 1)
	if err != nil {
		t.Fatalf("ListConversations() error = %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("conversation count = %d, want 2", len(list.Items))
	}
	if list.Items[0].Peer.Username != "carol" || list.Items[0].UnreadCount != 2 {
		t.Errorf("first conversation = %+v", list.Items[0])
	}
	if list.Items[0].Peer.AvatarURL == "" {
		t.Error("expected peer avatar URL")
	}
	if list.Items[1].Peer.Username != "bob" {
		t.Errorf("second conversation peer = %+v", list.Items[1].Peer)
	}
}

func TestServiceMarkConversationRead(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo, &fakeUserReader{}, NewHub(), newMessageDiscardLogger())

	if err := service.MarkConversationRead(context.Background(), 1, 2); err != nil {
		t.Fatalf("MarkConversationRead() error = %v", err)
	}
	if repo.lastMarkUserID != 1 || repo.lastMarkPeerID != 2 {
		t.Fatalf("mark args user=%d peer=%d", repo.lastMarkUserID, repo.lastMarkPeerID)
	}
}

func sameError(got error, want error, fallback error) bool {
	if want == nil {
		if fallback != nil {
			return got != nil
		}
		return got == nil
	}
	return errors.Is(got, want)
}

func newMessageDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
