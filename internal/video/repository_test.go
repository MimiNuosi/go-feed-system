package video

import (
	"context"
	"encoding/json"
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
