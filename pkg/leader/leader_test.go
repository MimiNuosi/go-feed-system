package leader

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

type fakeStore struct {
	mu       sync.Mutex
	owner    map[string]string
	renewOK  bool
	acquires int
	releases int
}

func newFakeStore() *fakeStore {
	return &fakeStore{owner: make(map[string]string), renewOK: true}
}

func (s *fakeStore) Acquire(
	ctx context.Context,
	name string,
	owner string,
	ttl time.Duration,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquires++
	if current := s.owner[name]; current != "" && current != owner {
		return false, nil
	}
	s.owner[name] = owner
	return true, nil
}

func (s *fakeStore) Renew(
	ctx context.Context,
	name string,
	owner string,
	ttl time.Duration,
) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renewOK && s.owner[name] == owner, nil
}

func (s *fakeStore) Release(ctx context.Context, name string, owner string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releases++
	if s.owner[name] == owner {
		delete(s.owner, name)
	}
	return nil
}

type fakeLeaderMetrics struct {
	mu      sync.Mutex
	leaders map[string]bool
	events  map[string]int
}

func (m *fakeLeaderMetrics) SetBackgroundWorkerLeader(worker string, leader bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leaders == nil {
		m.leaders = make(map[string]bool)
	}
	m.leaders[worker] = leader
}

func (m *fakeLeaderMetrics) IncBackgroundWorkerLease(worker string, result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.events == nil {
		m.events = make(map[string]int)
	}
	m.events[worker+":"+result]++
}

func TestElectorRunsOneWorkerAndFailsOver(t *testing.T) {
	store := newFakeStore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	options := Options{
		LeaseTTL:        200 * time.Millisecond,
		RenewInterval:   20 * time.Millisecond,
		AcquireInterval: 10 * time.Millisecond,
	}
	first, err := NewElector(store, options, logger, nil)
	if err != nil {
		t.Fatalf("new first elector: %v", err)
	}
	second, err := NewElector(store, options, logger, nil)
	if err != nil {
		t.Fatalf("new second elector: %v", err)
	}

	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	firstWorker := blockWorker(firstStarted)
	secondWorker := blockWorker(secondStarted)

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstErrCh := make(chan error, 1)
	go func() {
		firstErrCh <- first.Wrap("outbox", firstWorker).Run(firstCtx)
	}()
	waitSignal(t, firstStarted, "first worker")

	secondCtx, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondErrCh := make(chan error, 1)
	go func() {
		secondErrCh <- second.Wrap("outbox", secondWorker).Run(secondCtx)
	}()
	select {
	case <-secondStarted:
		t.Fatal("second worker became leader while lease was held")
	case <-time.After(50 * time.Millisecond):
	}

	cancelFirst()
	waitError(t, firstErrCh, "first elector")
	waitSignal(t, secondStarted, "second worker after failover")

	cancelSecond()
	waitError(t, secondErrCh, "second elector")
}

func TestElectorRenewsLease(t *testing.T) {
	store := newFakeStore()
	metrics := &fakeLeaderMetrics{}
	elector, err := NewElector(store, Options{
		LeaseTTL:        100 * time.Millisecond,
		RenewInterval:   10 * time.Millisecond,
		AcquireInterval: 5 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), metrics)
	if err != nil {
		t.Fatalf("new elector: %v", err)
	}
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- elector.Wrap("view", blockWorker(started)).Run(ctx)
	}()
	waitSignal(t, started, "worker")
	time.Sleep(35 * time.Millisecond)
	cancel()
	waitError(t, errCh, "elector")

	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if metrics.events["view:renewed"] < 2 {
		t.Fatalf("renewed events = %d, want at least 2", metrics.events["view:renewed"])
	}
}

type blockingWorker struct {
	started chan<- struct{}
	once    sync.Once
}

func blockWorker(started chan<- struct{}) *blockingWorker {
	return &blockingWorker{started: started}
}

func (w *blockingWorker) Run(ctx context.Context) error {
	w.once.Do(func() { close(w.started) })
	<-ctx.Done()
	return nil
}

func waitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func waitError(t *testing.T, errors <-chan error, name string) {
	t.Helper()
	select {
	case err := <-errors:
		if err != nil {
			t.Fatalf("%s returned error: %v", name, err)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
