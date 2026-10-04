package outbox

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"gorm.io/gorm"
)

var (
	errListPending     = errors.New("list pending failed")
	errEventPublish    = errors.New("mq publish failed")
	errMarkPublished   = errors.New("mark published failed")
	errIncrementOutbox = errors.New("increment retry failed")
)

type retryCall struct {
	eventID uint64
	message string
}

type fakeOutboxRepository struct {
	events  []Event
	listErr error

	markPublishedErr map[uint64]error
	incrementErr     error

	listLimit        int
	markPublishedIDs []uint64
	publishedAt      []time.Time
	retryCalls       []retryCall
}

func (f *fakeOutboxRepository) CreateWithTx(ctx context.Context, tx *gorm.DB, event *Event) error {
	return nil
}

func (f *fakeOutboxRepository) Create(ctx context.Context, event *Event) error {
	return nil
}

func (f *fakeOutboxRepository) ListPending(ctx context.Context, limit int) ([]Event, error) {
	f.listLimit = limit
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.events, nil
}

func (f *fakeOutboxRepository) MarkPublished(ctx context.Context, id uint64, publishedAt time.Time) error {
	f.markPublishedIDs = append(f.markPublishedIDs, id)
	f.publishedAt = append(f.publishedAt, publishedAt)
	if f.markPublishedErr != nil {
		return f.markPublishedErr[id]
	}
	return nil
}

func (f *fakeOutboxRepository) IncrementRetry(ctx context.Context, id uint64, lastError string) error {
	f.retryCalls = append(f.retryCalls, retryCall{eventID: id, message: lastError})
	return f.incrementErr
}

type fakeEventPublisher struct {
	publishErrors map[uint64]error
	published     []Event
}

func (f *fakeEventPublisher) Publish(ctx context.Context, event Event) error {
	f.published = append(f.published, event)
	if f.publishErrors != nil {
		return f.publishErrors[event.ID]
	}
	return nil
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestPublisher_PublishPending(t *testing.T) {
	events := []Event{
		{ID: 1, EventID: "event-1", EventType: EventTypeVideoPublished},
		{ID: 2, EventID: "event-2", EventType: EventTypeVideoPublished},
		{ID: 3, EventID: "event-3", EventType: EventTypeVideoPublished},
	}

	tests := []struct {
		name string

		events           []Event
		listErr          error
		publishErrors    map[uint64]error
		markPublishedErr map[uint64]error
		incrementErr     error

		wantCount         int
		wantErr           error
		wantListLimit     int
		wantPublishCount  int
		wantMarkCount     int
		wantRetryCount    int
		wantRetryEventID  uint64
		wantRetryMessage  string
		wantFirstMarkedID uint64
	}{
		{
			name:             "查询待发送事件失败",
			listErr:          errListPending,
			wantErr:          errListPending,
			wantListLimit:    3,
			wantPublishCount: 0,
			wantMarkCount:    0,
			wantRetryCount:   0,
		},
		{
			name:             "没有待发送事件",
			events:           nil,
			wantErr:          nil,
			wantListLimit:    3,
			wantPublishCount: 0,
			wantMarkCount:    0,
			wantRetryCount:   0,
		},
		{
			name:              "全部发送成功",
			events:            events,
			wantCount:         3,
			wantErr:           nil,
			wantListLimit:     3,
			wantPublishCount:  3,
			wantMarkCount:     3,
			wantRetryCount:    0,
			wantFirstMarkedID: 1,
		},
		{
			name:   "第一条发送失败后立即停止",
			events: events,
			publishErrors: map[uint64]error{
				1: errEventPublish,
			},
			wantCount:        0,
			wantErr:          errEventPublish,
			wantListLimit:    3,
			wantPublishCount: 1,
			wantMarkCount:    0,
			wantRetryCount:   1,
			wantRetryEventID: 1,
			wantRetryMessage: errEventPublish.Error(),
		},
		{
			name:   "部分成功后下一条失败立即停止",
			events: events,
			publishErrors: map[uint64]error{
				2: errEventPublish,
			},
			wantCount:         1,
			wantErr:           errEventPublish,
			wantListLimit:     3,
			wantPublishCount:  2,
			wantMarkCount:     1,
			wantRetryCount:    1,
			wantRetryEventID:  2,
			wantRetryMessage:  errEventPublish.Error(),
			wantFirstMarkedID: 1,
		},
		{
			name:   "发送成功但标记发布状态失败",
			events: events,
			markPublishedErr: map[uint64]error{
				1: errMarkPublished,
			},
			wantCount:         0,
			wantErr:           errMarkPublished,
			wantListLimit:     3,
			wantPublishCount:  1,
			wantMarkCount:     1,
			wantRetryCount:    0,
			wantFirstMarkedID: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &fakeOutboxRepository{
				events:           tt.events,
				listErr:          tt.listErr,
				markPublishedErr: tt.markPublishedErr,
				incrementErr:     tt.incrementErr,
			}
			eventPublisher := &fakeEventPublisher{
				publishErrors: tt.publishErrors,
			}

			publisher := NewPublisher(repository, eventPublisher, 3, newDiscardLogger())
			gotCount, err := publisher.PublishPending(context.Background())

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
			if gotCount != tt.wantCount {
				t.Errorf("expected published count %d, got %d", tt.wantCount, gotCount)
			}
			if repository.listLimit != tt.wantListLimit {
				t.Errorf("expected list limit %d, got %d", tt.wantListLimit, repository.listLimit)
			}
			if len(eventPublisher.published) != tt.wantPublishCount {
				t.Errorf("expected publish count %d, got %d", tt.wantPublishCount, len(eventPublisher.published))
			}
			if len(repository.markPublishedIDs) != tt.wantMarkCount {
				t.Errorf("expected mark count %d, got %d", tt.wantMarkCount, len(repository.markPublishedIDs))
			}
			if len(repository.retryCalls) != tt.wantRetryCount {
				t.Errorf("expected retry count %d, got %d", tt.wantRetryCount, len(repository.retryCalls))
			}

			if tt.wantRetryCount > 0 {
				gotRetry := repository.retryCalls[0]
				if gotRetry.eventID != tt.wantRetryEventID {
					t.Errorf("expected retry event ID %d, got %d", tt.wantRetryEventID, gotRetry.eventID)
				}
				if gotRetry.message != tt.wantRetryMessage {
					t.Errorf("expected retry message %q, got %q", tt.wantRetryMessage, gotRetry.message)
				}
			}

			if tt.wantMarkCount > 0 && repository.markPublishedIDs[0] != tt.wantFirstMarkedID {
				t.Errorf("expected first marked ID %d, got %d", tt.wantFirstMarkedID, repository.markPublishedIDs[0])
			}
		})
	}
}

func TestPublisher_PublishPending_RetryFailureDoesNotMaskPublishError(t *testing.T) {
	repository := &fakeOutboxRepository{
		events:       []Event{{ID: 1, EventID: "event-1"}},
		incrementErr: errIncrementOutbox,
	}
	eventPublisher := &fakeEventPublisher{
		publishErrors: map[uint64]error{
			1: errEventPublish,
		},
	}

	publisher := NewPublisher(repository, eventPublisher, 10, newDiscardLogger())
	count, err := publisher.PublishPending(context.Background())

	if count != 0 {
		t.Errorf("expected published count 0, got %d", count)
	}
	if !errors.Is(err, errEventPublish) {
		t.Errorf("expected publish error in chain, got %v", err)
	}
	if errors.Is(err, errIncrementOutbox) {
		t.Errorf("retry bookkeeping error must not replace publish error: %v", err)
	}
	if len(repository.retryCalls) != 1 {
		t.Fatalf("expected one retry call, got %d", len(repository.retryCalls))
	}
}

func TestPublisher_PublishPending_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	repository := &fakeOutboxRepository{
		events: []Event{{ID: 1, EventID: "event-1"}},
	}
	eventPublisher := &fakeEventPublisher{}

	publisher := NewPublisher(repository, eventPublisher, 10, newDiscardLogger())
	count, err := publisher.PublishPending(ctx)

	if count != 0 {
		t.Errorf("expected published count 0, got %d", count)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if len(eventPublisher.published) != 0 {
		t.Errorf("expected no publish calls after cancellation, got %d", len(eventPublisher.published))
	}
	if len(repository.retryCalls) != 0 {
		t.Errorf("expected no retry calls after cancellation, got %d", len(repository.retryCalls))
	}
}
