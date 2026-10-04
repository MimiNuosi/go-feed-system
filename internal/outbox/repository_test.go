package outbox

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"go-feed-system/pkg/database"
)

var errRollbackTest = errors.New("rollback test transaction")

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

	t.Cleanup(func() {
		if err := database.Close(db); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	return db
}

func openTestTx(t *testing.T) *gorm.DB {
	t.Helper()

	db := openTestDB(t)
	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin test transaction: %v", tx.Error)
	}

	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil && !errors.Is(err, gorm.ErrInvalidTransaction) {
			t.Errorf("rollback test transaction: %v", err)
		}
	})

	return tx
}

func cleanOutboxTable(t *testing.T, db *gorm.DB) {
	t.Helper()

	if err := db.Exec("DELETE FROM outbox_events").Error; err != nil {
		t.Fatalf("clean outbox_events: %v", err)
	}
}

func newTestEvent() *Event {
	return &Event{
		EventID:     uuid.NewString(),
		EventType:   EventTypeVideoPublished,
		AggregateID: 1001,
		Payload:     []byte(`{"video_id":1001,"author_id":10,"published_at":"2026-10-04T12:00:00+08:00"}`),
		Status:      StatusPending,
	}
}

func TestGORMRepository_CreateWithTx(t *testing.T) {
	t.Run("事务提交后事件可见", func(t *testing.T) {
		db := openTestDB(t)
		repo := NewGORMRepository(db)
		event := newTestEvent()

		t.Cleanup(func() {
			if err := db.Where("event_id = ?", event.EventID).Delete(&Event{}).Error; err != nil {
				t.Errorf("cleanup event: %v", err)
			}
		})

		err := db.Transaction(func(tx *gorm.DB) error {
			return repo.CreateWithTx(context.Background(), tx, event)
		})
		if err != nil {
			t.Fatalf("create event in transaction: %v", err)
		}

		var count int64
		if err := db.Model(&Event{}).
			Where("event_id = ?", event.EventID).
			Count(&count).Error; err != nil {
			t.Fatalf("count created event: %v", err)
		}
		if count != 1 {
			t.Fatalf("expected one event after commit, got %d", count)
		}
	})

	t.Run("事务回滚后事件不存在", func(t *testing.T) {
		db := openTestDB(t)
		repo := NewGORMRepository(db)
		event := newTestEvent()

		t.Cleanup(func() {
			if err := db.Where("event_id = ?", event.EventID).Delete(&Event{}).Error; err != nil {
				t.Errorf("cleanup event: %v", err)
			}
		})

		err := db.Transaction(func(tx *gorm.DB) error {
			if err := repo.CreateWithTx(context.Background(), tx, event); err != nil {
				return err
			}
			return errRollbackTest
		})
		if !errors.Is(err, errRollbackTest) {
			t.Fatalf("expected rollback error, got %v", err)
		}

		var count int64
		if err := db.Model(&Event{}).
			Where("event_id = ?", event.EventID).
			Count(&count).Error; err != nil {
			t.Fatalf("count rolled back event: %v", err)
		}
		if count != 0 {
			t.Fatalf("expected rollback to remove the event, got %d row", count)
		}
	})
}

func TestGORMRepository_ListPending(t *testing.T) {
	tx := openTestTx(t)
	repo := NewGORMRepository(tx)
	cleanOutboxTable(t, tx)

	first := newTestEvent()
	second := newTestEvent()
	published := newTestEvent()
	published.Status = StatusPublished

	for _, event := range []*Event{first, second, published} {
		if err := repo.Create(context.Background(), event); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	events, err := repo.ListPending(context.Background(), 10)
	if err != nil {
		t.Fatalf("list pending events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected two pending events, got %d", len(events))
	}
	if events[0].ID != first.ID || events[1].ID != second.ID {
		t.Fatalf("expected pending events ordered by id [%d %d], got [%d %d]",
			first.ID, second.ID, events[0].ID, events[1].ID)
	}
}

func TestGORMRepository_MarkPublished(t *testing.T) {
	tx := openTestTx(t)
	repo := NewGORMRepository(tx)
	cleanOutboxTable(t, tx)

	event := newTestEvent()
	if err := repo.Create(context.Background(), event); err != nil {
		t.Fatalf("create event: %v", err)
	}

	publishedAt := time.Now().Truncate(time.Millisecond)
	if err := repo.MarkPublished(context.Background(), event.ID, publishedAt); err != nil {
		t.Fatalf("mark event published: %v", err)
	}

	var got Event
	if err := tx.First(&got, event.ID).Error; err != nil {
		t.Fatalf("reload event: %v", err)
	}
	if got.Status != StatusPublished {
		t.Errorf("expected status %q, got %q", StatusPublished, got.Status)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(publishedAt) {
		t.Errorf("expected published_at %v, got %v", publishedAt, got.PublishedAt)
	}

	// 已发布事件不能再被回退或改写，重复标记保持幂等。
	if err := repo.MarkPublished(context.Background(), event.ID, publishedAt.Add(time.Minute)); err != nil {
		t.Fatalf("mark event published twice: %v", err)
	}

	if err := tx.First(&got, event.ID).Error; err != nil {
		t.Fatalf("reload event after second mark: %v", err)
	}
	if got.PublishedAt == nil || !got.PublishedAt.Equal(publishedAt) {
		t.Errorf("second mark changed published_at to %v", got.PublishedAt)
	}
}

func TestGORMRepository_IncrementRetry(t *testing.T) {
	db := openTestDB(t)
	repo := NewGORMRepository(db)
	event := newTestEvent()

	t.Cleanup(func() {
		if err := db.Where("event_id = ?", event.EventID).Delete(&Event{}).Error; err != nil {
			t.Errorf("cleanup event: %v", err)
		}
	})

	if err := repo.Create(context.Background(), event); err != nil {
		t.Fatalf("create event: %v", err)
	}

	longError := strings.Repeat("错", 600)
	var wg sync.WaitGroup
	errs := make(chan error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.IncrementRetry(context.Background(), event.ID, longError)
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("increment retry: %v", err)
		}
	}

	var got Event
	if err := db.First(&got, event.ID).Error; err != nil {
		t.Fatalf("reload event: %v", err)
	}
	if got.RetryCount != 2 {
		t.Errorf("expected retry_count 2 after concurrent increments, got %d", got.RetryCount)
	}
	if len([]rune(got.LastError)) != 500 {
		t.Errorf("expected last_error to keep 500 runes, got %d", len([]rune(got.LastError)))
	}
	if !utf8.ValidString(got.LastError) {
		t.Error("expected last_error to remain valid UTF-8")
	}

	if err := repo.MarkPublished(context.Background(), event.ID, time.Now()); err != nil {
		t.Fatalf("mark event published: %v", err)
	}
	if err := repo.IncrementRetry(context.Background(), event.ID, "late failure"); err != nil {
		t.Fatalf("increment retry after publish: %v", err)
	}

	if err := db.First(&got, event.ID).Error; err != nil {
		t.Fatalf("reload event after late failure: %v", err)
	}
	if got.RetryCount != 2 {
		t.Errorf("published event retry_count changed to %d", got.RetryCount)
	}
	if got.LastError == "late failure" {
		t.Error("published event last_error was overwritten by a late failure")
	}
}

func TestTruncateLastError(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "短错误保持不变",
			input: "database unavailable",
			want:  "database unavailable",
		},
		{
			name:  "超过五百个中文字符按字符截断",
			input: strings.Repeat("错", 501),
			want:  strings.Repeat("错", 500),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateLastError(tt.input)
			if got != tt.want {
				t.Errorf("expected %d runes, got %d", len([]rune(tt.want)), len([]rune(got)))
			}
			if !utf8.ValidString(got) {
				t.Error("expected valid UTF-8")
			}
		})
	}
}
