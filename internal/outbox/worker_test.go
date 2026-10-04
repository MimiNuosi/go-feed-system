package outbox

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

var errWorkerPublish = errors.New("worker publish failed")

type fakePendingPublisher struct {
	mu         sync.Mutex
	callCount  int
	publishFn  func(call int) (int, error)
	callSignal chan struct{}
}

func newFakePendingPublisher(publishFn func(call int) (int, error)) *fakePendingPublisher {
	return &fakePendingPublisher{
		publishFn:  publishFn,
		callSignal: make(chan struct{}, 20),
	}
}

func (f *fakePendingPublisher) PublishPending(ctx context.Context) (int, error) {
	f.mu.Lock()
	f.callCount++
	call := f.callCount
	f.mu.Unlock()

	select {
	case f.callSignal <- struct{}{}:
	default:
	}

	if f.publishFn != nil {
		return f.publishFn(call)
	}
	return 0, nil
}

func (f *fakePendingPublisher) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

func waitForWorkerCall(t *testing.T, publisher *fakePendingPublisher) {
	t.Helper()

	select {
	case <-publisher.callSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for worker call")
	}
}

func waitForWorkerExit(t *testing.T, errCh <-chan error) {
	t.Helper()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected worker to exit without error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for worker to exit")
	}
}

func TestWorker_RunImmediatelyAndPeriodically(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	publisher := newFakePendingPublisher(func(call int) (int, error) {
		return 1, nil
	})
	worker := NewWorker(publisher, 20*time.Millisecond, newDiscardLogger())

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	waitForWorkerCall(t, publisher)
	waitForWorkerCall(t, publisher)

	cancel()
	waitForWorkerExit(t, errCh)
}

func TestWorker_ContinuesAfterPublishError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	publisher := newFakePendingPublisher(func(call int) (int, error) {
		if call == 1 {
			return 0, errWorkerPublish
		}
		return 1, nil
	})
	worker := NewWorker(publisher, 20*time.Millisecond, newDiscardLogger())

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	waitForWorkerCall(t, publisher)
	waitForWorkerCall(t, publisher)

	cancel()
	waitForWorkerExit(t, errCh)

	if publisher.calls() < 2 {
		t.Fatalf("expected worker to continue after error, got %d calls", publisher.calls())
	}
}

func TestWorker_ContextCancelDoesNotWaitForTicker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	publisher := newFakePendingPublisher(nil)
	worker := NewWorker(publisher, time.Hour, newDiscardLogger())

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	waitForWorkerCall(t, publisher)
	cancel()
	waitForWorkerExit(t, errCh)
}
