package video

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"go-feed-system/internal/outbox"
	"go-feed-system/internal/user"
	"go-feed-system/pkg/database"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}

	db, err := database.Open(context.Background(), database.MySQLConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin test transaction: %v", tx.Error)
	}

	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil && err != gorm.ErrInvalidTransaction {
			t.Errorf("rollback test transaction: %v", err)
		}
		if err := database.Close(db); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	return tx
}

func createTestAuthor(t *testing.T, db *gorm.DB) *user.User {
	t.Helper()

	suffix := uuid.NewString()
	author := &user.User{
		Username:     "outbox-author-" + suffix[:8],
		Email:        suffix + "@example.com",
		PasswordHash: "hash",
	}
	if err := db.Create(author).Error; err != nil {
		t.Fatalf("create test author: %v", err)
	}

	return author
}

func newOutboxTestVideo(authorID uint64) *Video {
	return &Video{
		AuthorID:         authorID,
		Title:            "Outbox integration video",
		Description:      "test transaction",
		StorageKey:       "test/videos/outbox/" + uuid.NewString() + ".mp4",
		OriginalFilename: "outbox.mp4",
		ContentType:      "video/mp4",
		SizeBytes:        1024,
		Status:           StatusReady,
		CreatedAt:        time.Now().UTC().Truncate(time.Millisecond),
	}
}

func newOutboxTestEvent() *outbox.Event {
	return &outbox.Event{
		EventID:   uuid.NewString(),
		EventType: outbox.EventTypeVideoPublished,
		Status:    outbox.StatusPending,
	}
}

func TestGORMRepository_CreateWithOutbox(t *testing.T) {
	t.Run("提交后视频与 Outbox 事件同时存在", func(t *testing.T) {
		tx := openTestDB(t)
		author := createTestAuthor(t, tx)
		outboxRepo := outbox.NewGORMRepository(tx)
		repo := NewGORMRepository(tx, outboxRepo)

		video := newOutboxTestVideo(author.ID)
		event := newOutboxTestEvent()

		if err := repo.CreateWithOutbox(context.Background(), video, event); err != nil {
			t.Fatalf("create video with outbox: %v", err)
		}
		if video.ID == 0 {
			t.Fatal("expected MySQL to populate video ID")
		}

		var storedVideo Video
		if err := tx.Where("storage_key = ?", video.StorageKey).First(&storedVideo).Error; err != nil {
			t.Fatalf("load stored video: %v", err)
		}
		if storedVideo.ID != video.ID {
			t.Errorf("expected video ID %d, got %d", video.ID, storedVideo.ID)
		}

		var storedEvent outbox.Event
		if err := tx.Where("event_id = ?", event.EventID).First(&storedEvent).Error; err != nil {
			t.Fatalf("load stored outbox event: %v", err)
		}
		if storedEvent.AggregateID != video.ID {
			t.Errorf("expected aggregate ID %d, got %d", video.ID, storedEvent.AggregateID)
		}
		if storedEvent.Status != outbox.StatusPending {
			t.Errorf("expected status %q, got %q", outbox.StatusPending, storedEvent.Status)
		}

		var payload outbox.VideoPublishedPayload
		if err := json.Unmarshal(storedEvent.Payload, &payload); err != nil {
			t.Fatalf("unmarshal stored payload: %v", err)
		}
		if payload.VideoID != video.ID {
			t.Errorf("expected payload video ID %d, got %d", video.ID, payload.VideoID)
		}
		if payload.AuthorID != author.ID {
			t.Errorf("expected payload author ID %d, got %d", author.ID, payload.AuthorID)
		}
		if !payload.PublishedAt.Equal(video.CreatedAt) {
			t.Errorf("expected published at %v, got %v", video.CreatedAt, payload.PublishedAt)
		}
	})

	t.Run("Outbox 写入失败时视频回滚", func(t *testing.T) {
		tx := openTestDB(t)
		author := createTestAuthor(t, tx)
		outboxRepo := outbox.NewGORMRepository(tx)
		repo := NewGORMRepository(tx, outboxRepo)

		existingEvent := newOutboxTestEvent()
		existingEvent.Payload = []byte(`{"existing":true}`)
		if err := outboxRepo.Create(context.Background(), existingEvent); err != nil {
			t.Fatalf("create conflicting outbox event: %v", err)
		}

		video := newOutboxTestVideo(author.ID)
		event := newOutboxTestEvent()
		event.EventID = existingEvent.EventID

		err := repo.CreateWithOutbox(context.Background(), video, event)
		if err == nil {
			t.Fatal("expected outbox unique conflict")
		}

		var count int64
		if err := tx.Model(&Video{}).
			Where("storage_key = ?", video.StorageKey).
			Count(&count).Error; err != nil {
			t.Fatalf("count rolled back video: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected video insert to roll back, found %d row", count)
		}
	})
}

// TestGORMRepository_ViewCountRoundTrip 验证浏览量能从 MySQL 写进去、读出来。
//
// 它同时确认两件事：
//  1. AddViewCount 是累加语义（view_count + delta），不是覆盖。
//  2. ViewCount 字段加进模型之后，FindByID 的 SELECT * 会自动把它读回来，
//     仓储层不需要额外改动。
func TestGORMRepository_ViewCountRoundTrip(t *testing.T) {
	tx := openTestDB(t)
	author := createTestAuthor(t, tx)
	repository := NewGORMRepository(tx, outbox.NewGORMRepository(tx))
	ctx := context.Background()

	item := newOutboxTestVideo(author.ID)
	if err := repository.Create(ctx, item); err != nil {
		t.Fatalf("create video: %v", err)
	}
	if item.ViewCount != 0 {
		t.Fatalf("expected new video view count 0, got %d", item.ViewCount)
	}

	if err := repository.AddViewCount(ctx, item.ID, 7); err != nil {
		t.Fatalf("add view count: %v", err)
	}
	reloaded, err := repository.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("find video by id: %v", err)
	}
	if reloaded.ViewCount != 7 {
		t.Fatalf("expected view count 7, got %d", reloaded.ViewCount)
	}

	// 再来一次必须是累加，不能把上一次的值覆盖掉
	if err := repository.AddViewCount(ctx, item.ID, 3); err != nil {
		t.Fatalf("add view count again: %v", err)
	}
	reloaded, err = repository.FindByID(ctx, item.ID)
	if err != nil {
		t.Fatalf("find video by id: %v", err)
	}
	if reloaded.ViewCount != 10 {
		t.Fatalf("expected accumulated view count 10, got %d", reloaded.ViewCount)
	}

	// 视频不存在时 RowsAffected == 0，属于正常情况：增量丢弃，不返回错误
	if err := repository.AddViewCount(ctx, item.ID+999999, 1); err != nil {
		t.Fatalf("expected missing video to be tolerated, got %v", err)
	}

	// 非正增量是非法输入
	if err := repository.AddViewCount(ctx, item.ID, 0); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for zero delta, got %v", err)
	}
}

func TestGORMRepository_ListReadyByIDs(t *testing.T) {
	t.Run("空 ID 返回非 nil 空切片", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx, nil)

		videos, err := repo.ListReadyByIDs(context.Background(), nil)
		if err != nil {
			t.Fatalf("list ready videos: %v", err)
		}
		if videos == nil || len(videos) != 0 {
			t.Fatalf("expected non-nil empty slice, got %#v", videos)
		}
	})

	t.Run("只返回 ready 视频并忽略缺失 ID", func(t *testing.T) {
		tx := openTestDB(t)
		author := createTestAuthor(t, tx)
		repo := NewGORMRepository(tx, nil)

		ready1 := newOutboxTestVideo(author.ID)
		ready2 := newOutboxTestVideo(author.ID)
		pending := newOutboxTestVideo(author.ID)
		pending.Status = "pending"

		for _, video := range []*Video{ready1, ready2, pending} {
			if err := tx.Create(video).Error; err != nil {
				t.Fatalf("create test video: %v", err)
			}
		}

		videos, err := repo.ListReadyByIDs(
			context.Background(),
			[]uint64{ready1.ID, pending.ID, ready2.ID, 999999},
		)
		if err != nil {
			t.Fatalf("list ready videos: %v", err)
		}
		if len(videos) != 2 {
			t.Fatalf("expected two ready videos, got %v", videos)
		}

		got := map[uint64]bool{}
		for _, item := range videos {
			got[item.ID] = true
			if item.Status != StatusReady {
				t.Fatalf("expected ready video, got status %q", item.Status)
			}
		}
		if !got[ready1.ID] || !got[ready2.ID] {
			t.Fatalf("expected IDs %d and %d, got %v", ready1.ID, ready2.ID, videos)
		}
	})
}
