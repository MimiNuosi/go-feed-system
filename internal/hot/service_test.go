package hot

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var errHotRepository = errors.New("hot repository failed")

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(now time.Time) {
	c.mu.Lock()
	c.now = now
	c.mu.Unlock()
}

type fakeRankingRepository struct {
	mu sync.Mutex

	calls       int
	lastLimit   int
	videos      []RankedVideo
	err         error
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
}

func (f *fakeRankingRepository) RecordView(
	ctx context.Context,
	videoID uint64,
	viewerKey string,
	occurredAt time.Time,
) (bool, error) {
	return false, nil
}

func (f *fakeRankingRepository) TopVideoIDs(
	ctx context.Context,
	window Window,
	at time.Time,
	cursor *Cursor,
	limit int,
) ([]RankedVideo, error) {
	f.mu.Lock()
	f.calls++
	f.lastLimit = limit
	err := f.err
	started := f.started
	release := f.release
	videos := append([]RankedVideo(nil), f.videos...)
	f.mu.Unlock()

	if started != nil {
		f.startedOnce.Do(func() {
			close(started)
		})
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err != nil {
		return nil, err
	}
	return videos, nil
}

func (f *fakeRankingRepository) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestService_ListHot_UsesCacheAndSingleflight(t *testing.T) {
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	started := make(chan struct{})
	release := make(chan struct{})
	repository := &fakeRankingRepository{
		videos:  []RankedVideo{{VideoID: 2, Score: 20}, {VideoID: 1, Score: 10}},
		started: started,
		release: release,
	}
	service := NewService(repository, nil)
	service.now = clock.Now

	const callers = 20
	var wg sync.WaitGroup
	errCh := make(chan error, callers)

	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			videos, err := service.ListHot(
				context.Background(),
				WindowOneHour,
				now,
				nil,
				DefaultHotPageSize,
			)
			if err != nil {
				errCh <- err
				return
			}
			if len(videos) != 2 {
				errCh <- errors.New("unexpected hot feed length")
			}
		}()
	}

	<-started
	if repository.Calls() != 1 {
		t.Fatalf("expected one repository call, got %d", repository.Calls())
	}
	close(release)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}

	if _, err := service.ListHot(
		context.Background(),
		WindowOneHour,
		now,
		nil,
		DefaultHotPageSize,
	); err != nil {
		t.Fatalf("fresh cache call: %v", err)
	}
	if repository.Calls() != 1 {
		t.Fatalf("expected cache hit to avoid repository call, got %d", repository.Calls())
	}
}

func TestService_ListHot_UsesStaleCacheOnRepositoryError(t *testing.T) {
	now := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: now}
	repository := &fakeRankingRepository{
		videos: []RankedVideo{{VideoID: 1, Score: 10}},
	}
	service := NewService(repository, nil)
	service.now = clock.Now

	if _, err := service.ListHot(
		context.Background(),
		WindowOneHour,
		now,
		nil,
		DefaultHotPageSize,
	); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	repository.mu.Lock()
	repository.err = errHotRepository
	repository.mu.Unlock()
	clock.Set(now.Add(10 * time.Second))

	videos, err := service.ListHot(
		context.Background(),
		WindowOneHour,
		now,
		nil,
		DefaultHotPageSize,
	)
	if err != nil {
		t.Fatalf("expected stale cache fallback, got %v", err)
	}
	if len(videos) != 1 || videos[0].VideoID != 1 {
		t.Fatalf("unexpected stale videos: %v", videos)
	}
	if repository.Calls() != 2 {
		t.Fatalf("expected repository retry, got %d calls", repository.Calls())
	}
}

func TestService_ListHot_ValidationAndPageSize(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name         string
		window       Window
		at           time.Time
		cursor       *Cursor
		pageSize     int
		wantErr      error
		wantRepoCall bool
		wantLimit    int
	}{
		{
			name:     "window 为空",
			at:       now,
			pageSize: 20,
			wantErr:  ErrInvalidInput,
		},
		{
			name:     "window 非法",
			window:   Window("7d"),
			at:       now,
			pageSize: 20,
			wantErr:  ErrInvalidInput,
		},
		{
			name:     "时间为零",
			window:   WindowOneHour,
			pageSize: 20,
			wantErr:  ErrInvalidInput,
		},
		{
			name:     "cursor 暂不支持",
			window:   WindowOneHour,
			at:       now,
			cursor:   &Cursor{},
			pageSize: 20,
			wantErr:  ErrInvalidInput,
		},
		{
			name:         "pageSize 默认值",
			window:       WindowOneHour,
			at:           now,
			pageSize:     0,
			wantRepoCall: true,
			wantLimit:    DefaultHotPageSize,
		},
		{
			name:         "pageSize 最大值",
			window:       WindowOneHour,
			at:           now,
			pageSize:     1000,
			wantRepoCall: true,
			wantLimit:    MaxHotPageSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &fakeRankingRepository{}
			service := NewService(repository, nil)
			_, err := service.ListHot(
				context.Background(),
				tt.window,
				tt.at,
				tt.cursor,
				tt.pageSize,
			)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("list hot: %v", err)
			}
			if tt.wantRepoCall && repository.Calls() != 1 {
				t.Fatalf("expected one repository call, got %d", repository.Calls())
			}
			if tt.wantLimit != 0 {
				repository.mu.Lock()
				gotLimit := repository.lastLimit
				repository.mu.Unlock()
				if gotLimit != tt.wantLimit {
					t.Fatalf("expected limit %d, got %d", tt.wantLimit, gotLimit)
				}
			}
		})
	}
}
