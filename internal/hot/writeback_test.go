package hot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"
)

var (
	errWritebackDB  = errors.New("mysql is down")
	errWritebackAck = errors.New("redis is down")
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakePendingViewStore struct {
	mu         sync.Mutex
	pendings   map[uint64]int64
	pendingErr error
	ackErrOn   map[uint64]error
	ackedOrder []uint64
	lastLimit  int
}

func (f *fakePendingViewStore) PendingViews(
	ctx context.Context,
	limit int,
) (map[uint64]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.lastLimit = limit
	if f.pendingErr != nil {
		return nil, f.pendingErr
	}

	// 返回副本，避免被测量代码意外修改测试数据。
	out := make(map[uint64]int64, len(f.pendings))
	for videoID, delta := range f.pendings {
		out[videoID] = delta
	}
	return out, nil
}

func (f *fakePendingViewStore) AckPendingView(ctx context.Context, videoID uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.ackErrOn[videoID]; err != nil {
		return err
	}
	f.ackedOrder = append(f.ackedOrder, videoID)
	return nil
}

func (f *fakePendingViewStore) acked() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.ackedOrder...)
}

func (f *fakePendingViewStore) limit() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastLimit
}

type viewCountCall struct {
	videoID uint64
	delta   int64
}

type fakeViewCountWriter struct {
	mu      sync.Mutex
	errOn   map[uint64]error
	written []viewCountCall
}

func (f *fakeViewCountWriter) AddViewCount(
	ctx context.Context,
	videoID uint64,
	delta int64,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := f.errOn[videoID]; err != nil {
		return err
	}
	f.written = append(f.written, viewCountCall{videoID: videoID, delta: delta})
	return nil
}

func (f *fakeViewCountWriter) calls() []viewCountCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]viewCountCall(nil), f.written...)
}

type fakeViewWritebackMetrics struct {
	mu      sync.Mutex
	results []string
}

func (m *fakeViewWritebackMetrics) IncViewWriteback(result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.results = append(m.results, result)
}

func (m *fakeViewWritebackMetrics) count(result string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := 0
	for _, item := range m.results {
		if item == result {
			total++
		}
	}
	return total
}

func TestViewWriteback_WritebackPending(t *testing.T) {
	tests := []struct {
		name           string
		pendings       map[uint64]int64
		pendingErr     error
		writeErrOn     map[uint64]error
		ackErrOn       map[uint64]error
		canceled       bool
		wantWritten    int
		wantErr        error
		wantWriteOrder []viewCountCall
		wantAckOrder   []uint64
		wantSuccess    int
		wantWriteError int
		wantAckError   int
	}{
		{
			name:     "空批次什么都不做",
			pendings: map[uint64]int64{},
		},
		{
			name:        "两条都成功并按 videoID 升序处理",
			pendings:    map[uint64]int64{10: 3, 7: 1},
			wantWritten: 2,
			wantWriteOrder: []viewCountCall{
				{videoID: 7, delta: 1},
				{videoID: 10, delta: 3},
			},
			wantAckOrder: []uint64{7, 10},
			wantSuccess:  2,
		},
		{
			name:       "写库失败立即停手且不 ack",
			pendings:   map[uint64]int64{7: 1, 10: 3},
			writeErrOn: map[uint64]error{7: errWritebackDB},
			wantErr:    errWritebackDB,
			// 写库失败的条目不会进入 written 列表，也不会被 ack，
			// 它会留在 Redis 里等下一轮重试。
			wantWriteError: 1,
		},
		{
			name:     "ack 失败时库已写过但不算完成",
			pendings: map[uint64]int64{7: 1},
			ackErrOn: map[uint64]error{7: errWritebackAck},
			wantErr:  errWritebackAck,
			wantWriteOrder: []viewCountCall{
				{videoID: 7, delta: 1},
			},
			wantAckError: 1,
		},
		{
			name:        "非正增量只 ack 不写库",
			pendings:    map[uint64]int64{7: 0, 10: 2},
			wantWritten: 1,
			wantWriteOrder: []viewCountCall{
				{videoID: 10, delta: 2},
			},
			wantAckOrder: []uint64{7, 10},
			wantSuccess:  1,
		},
		{
			name:       "读取失败直接返回错误",
			pendingErr: errWritebackDB,
			wantErr:    errWritebackDB,
		},
		{
			name:     "context 已取消时不写库",
			pendings: map[uint64]int64{7: 1},
			canceled: true,
			wantErr:  context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakePendingViewStore{
				pendings:   tt.pendings,
				pendingErr: tt.pendingErr,
				ackErrOn:   tt.ackErrOn,
			}
			writer := &fakeViewCountWriter{errOn: tt.writeErrOn}
			metrics := &fakeViewWritebackMetrics{}
			writeback := NewViewWriteback(store, writer, 2, discardLogger(), metrics)

			ctx := context.Background()
			if tt.canceled {
				canceledCtx, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceledCtx
			}

			written, err := writeback.WritebackPending(ctx)

			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}

			if written != tt.wantWritten {
				t.Errorf("written = %d, want %d", written, tt.wantWritten)
			}
			if calls := writer.calls(); !reflect.DeepEqual(calls, tt.wantWriteOrder) {
				t.Errorf("write calls = %+v, want %+v", calls, tt.wantWriteOrder)
			}
			if acked := store.acked(); !reflect.DeepEqual(acked, tt.wantAckOrder) {
				t.Errorf("acked = %v, want %v", acked, tt.wantAckOrder)
			}
			if got := metrics.count("success"); got != tt.wantSuccess {
				t.Errorf("success metric = %d, want %d", got, tt.wantSuccess)
			}
			if got := metrics.count("write_error"); got != tt.wantWriteError {
				t.Errorf("write_error metric = %d, want %d", got, tt.wantWriteError)
			}
			if got := metrics.count("ack_error"); got != tt.wantAckError {
				t.Errorf("ack_error metric = %d, want %d", got, tt.wantAckError)
			}

			// 每轮都必须把 batchSize 透传给 Redis 侧。
			if got := store.limit(); got != 2 {
				t.Errorf("PendingViews limit = %d, want 2", got)
			}
		})
	}
}

func TestNewViewWriteback_DefaultBatchSize(t *testing.T) {
	store := &fakePendingViewStore{pendings: map[uint64]int64{}}
	writeback := NewViewWriteback(
		store,
		&fakeViewCountWriter{},
		0,
		discardLogger(),
		nil,
	)

	if _, err := writeback.WritebackPending(context.Background()); err != nil {
		t.Fatalf("writeback pending: %v", err)
	}
	if got := store.limit(); got != DefaultViewWritebackBatchSize {
		t.Fatalf("limit = %d, want %d", got, DefaultViewWritebackBatchSize)
	}
}

type fakeViewWritebackRunner struct {
	mu         sync.Mutex
	calls      int
	callFn     func(call int) (int, error)
	callSignal chan struct{}
}

func newFakeViewWritebackRunner(
	callFn func(call int) (int, error),
) *fakeViewWritebackRunner {
	return &fakeViewWritebackRunner{
		callFn:     callFn,
		callSignal: make(chan struct{}, 20),
	}
}

func (f *fakeViewWritebackRunner) WritebackPending(ctx context.Context) (int, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()

	select {
	case f.callSignal <- struct{}{}:
	default:
	}

	if f.callFn != nil {
		return f.callFn(call)
	}
	return 0, nil
}

func (f *fakeViewWritebackRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func waitForWritebackCall(t *testing.T, runner *fakeViewWritebackRunner) {
	t.Helper()

	select {
	case <-runner.callSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for writeback call")
	}
}

func waitForWritebackExit(t *testing.T, errCh <-chan error) {
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

func TestViewWritebackWorker_RunImmediatelyAndPeriodically(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := newFakeViewWritebackRunner(func(call int) (int, error) {
		return 1, nil
	})
	worker := NewViewWritebackWorker(runner, 20*time.Millisecond, discardLogger())

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	waitForWritebackCall(t, runner)
	waitForWritebackCall(t, runner)

	cancel()
	waitForWritebackExit(t, errCh)
}

func TestViewWritebackWorker_ContinuesAfterError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := newFakeViewWritebackRunner(func(call int) (int, error) {
		if call == 1 {
			return 0, errWritebackDB
		}
		return 1, nil
	})
	worker := NewViewWritebackWorker(runner, 20*time.Millisecond, discardLogger())

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	waitForWritebackCall(t, runner)
	waitForWritebackCall(t, runner)

	cancel()
	waitForWritebackExit(t, errCh)

	if runner.callCount() < 2 {
		t.Fatalf("expected worker to continue after error, got %d calls", runner.callCount())
	}
}

func TestViewWritebackWorker_ContextCancelDoesNotWaitForTicker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := newFakeViewWritebackRunner(nil)
	worker := NewViewWritebackWorker(runner, time.Hour, discardLogger())

	errCh := make(chan error, 1)
	go func() {
		errCh <- worker.Run(ctx)
	}()

	waitForWritebackCall(t, runner)
	cancel()
	waitForWritebackExit(t, errCh)
}
