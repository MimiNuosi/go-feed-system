package notification

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"go-feed-system/internal/user"
)

type fakeRepository struct {
	createFunc       func(ctx context.Context, item *Notification) error
	listByUserFunc   func(ctx context.Context, userID, beforeID uint64, limit int) ([]Notification, error)
	unreadCountFunc  func(ctx context.Context, userID uint64) (int64, error)
	markAllReadFunc  func(ctx context.Context, userID uint64) error
	lastListUserID   uint64
	lastListBeforeID uint64
	lastListLimit    int
}

func (f *fakeRepository) Create(ctx context.Context, item *Notification) error {
	if f.createFunc != nil {
		return f.createFunc(ctx, item)
	}
	item.ID = 1
	return nil
}

func (f *fakeRepository) ListByUser(
	ctx context.Context,
	userID uint64,
	beforeID uint64,
	limit int,
) ([]Notification, error) {
	f.lastListUserID = userID
	f.lastListBeforeID = beforeID
	f.lastListLimit = limit
	if f.listByUserFunc != nil {
		return f.listByUserFunc(ctx, userID, beforeID, limit)
	}
	return nil, nil
}

func (f *fakeRepository) UnreadCount(ctx context.Context, userID uint64) (int64, error) {
	if f.unreadCountFunc != nil {
		return f.unreadCountFunc(ctx, userID)
	}
	return 0, nil
}

func (f *fakeRepository) MarkAllRead(ctx context.Context, userID uint64) error {
	if f.markAllReadFunc != nil {
		return f.markAllReadFunc(ctx, userID)
	}
	return nil
}

type fakeUserReader struct {
	getByIDsFunc func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
	lastIDs      []uint64
}

func (f *fakeUserReader) GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
	f.lastIDs = append([]uint64(nil), ids...)
	if f.getByIDsFunc != nil {
		return f.getByIDsFunc(ctx, ids)
	}
	return map[uint64]*user.User{}, nil
}

func TestServiceNotify(t *testing.T) {
	tests := []struct {
		name          string
		recipientID   uint64
		actorID       uint64
		repoErr       error
		wantCreate    bool
		wantBroadcast bool
		wantErr       bool
	}{
		{
			name:          "正常写入并广播",
			recipientID:   2,
			actorID:       1,
			wantCreate:    true,
			wantBroadcast: true,
		},
		{
			name:        "自己操作自己不写入",
			recipientID: 1,
			actorID:     1,
		},
		{
			name:        "身份为零不写入",
			recipientID: 0,
			actorID:     1,
		},
		{
			name:        "仓储失败返回错误",
			recipientID: 2,
			actorID:     1,
			repoErr:     errors.New("db down"),
			wantCreate:  true,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			created := false
			repo := &fakeRepository{
				createFunc: func(ctx context.Context, item *Notification) error {
					created = true
					item.ID = 42
					return tt.repoErr
				},
			}
			users := &fakeUserReader{
				getByIDsFunc: func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
					return map[uint64]*user.User{
						tt.actorID: {ID: tt.actorID, Username: "alice", AvatarKey: "avatar/a"},
					}, nil
				},
			}
			hub := NewHub()
			service := NewService(repo, users, hub, newNotificationDiscardLogger())

			var events <-chan Item
			if tt.wantBroadcast {
				events, _ = hub.Subscribe(tt.recipientID)
			}

			err := service.Notify(
				context.Background(),
				tt.recipientID,
				tt.actorID,
				"like",
				7,
				"",
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Notify() error = %v, wantErr %v", err, tt.wantErr)
			}
			if created != tt.wantCreate {
				t.Fatalf("Create called = %v, want %v", created, tt.wantCreate)
			}
			if !tt.wantBroadcast {
				return
			}

			select {
			case event := <-events:
				if event.Actor.Username != "alice" {
					t.Errorf("actor username = %q, want alice", event.Actor.Username)
				}
				if event.Actor.AvatarURL == "" {
					t.Error("expected actor avatar URL")
				}
				if event.VideoID == nil || *event.VideoID != 7 {
					t.Errorf("video id = %v, want 7", event.VideoID)
				}
			case <-time.After(time.Second):
				t.Fatal("timed out waiting for notification event")
			}
		})
	}
}

func TestServiceList(t *testing.T) {
	now := time.Now().UTC()
	records := make([]Notification, 0, 21)
	for id := uint64(21); id >= 1; id-- {
		records = append(records, Notification{
			ID:        id,
			UserID:    99,
			ActorID:   1,
			Kind:      "like",
			CreatedAt: now,
		})
	}

	repo := &fakeRepository{
		listByUserFunc: func(
			ctx context.Context,
			userID, beforeID uint64,
			limit int,
		) ([]Notification, error) {
			return records, nil
		},
	}
	users := &fakeUserReader{
		getByIDsFunc: func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
			return map[uint64]*user.User{
				1: {ID: 1, Username: "alice"},
			}, nil
		},
	}
	service := NewService(repo, users, NewHub(), newNotificationDiscardLogger())

	page, err := service.List(context.Background(), 99, 30, DefaultPageSize)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if repo.lastListUserID != 99 || repo.lastListBeforeID != 30 || repo.lastListLimit != 21 {
		t.Fatalf(
			"unexpected repository args: user=%d before=%d limit=%d",
			repo.lastListUserID,
			repo.lastListBeforeID,
			repo.lastListLimit,
		)
	}
	if !page.HasMore || len(page.Items) != DefaultPageSize {
		t.Fatalf("page = %+v, want 20 items and has_more", page)
	}
	if page.NextCursor != "2" {
		t.Errorf("next cursor = %q, want 2", page.NextCursor)
	}
	if page.Items[0].Actor.Username != "alice" {
		t.Errorf("actor username = %q, want alice", page.Items[0].Actor.Username)
	}
	if len(users.lastIDs) != 1 || users.lastIDs[0] != 1 {
		t.Errorf("user lookup ids = %v, want [1]", users.lastIDs)
	}
}

func TestServiceListUserLookupError(t *testing.T) {
	repo := &fakeRepository{
		listByUserFunc: func(
			ctx context.Context,
			userID, beforeID uint64,
			limit int,
		) ([]Notification, error) {
			return []Notification{{ID: 1, UserID: 2, ActorID: 1}}, nil
		},
	}
	users := &fakeUserReader{
		getByIDsFunc: func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
			return nil, errors.New("user db down")
		},
	}
	service := NewService(repo, users, NewHub(), newNotificationDiscardLogger())

	if _, err := service.List(context.Background(), 2, 0, 20); err == nil {
		t.Fatal("expected user lookup error")
	}
}

func newNotificationDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
