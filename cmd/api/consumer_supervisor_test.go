package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

var errConsumerReconnect = errors.New("consumer reconnect")

type fakeManagedConsumer struct {
	consume func(ctx context.Context) error
}

func (f *fakeManagedConsumer) Consume(ctx context.Context) error {
	return f.consume(ctx)
}

type fakeConsumerSession struct {
	consumer   managedConsumer
	readyErr   error
	closeCount int
}

func (f *fakeConsumerSession) Consumer() managedConsumer {
	return f.consumer
}

func (f *fakeConsumerSession) Ready(ctx context.Context) error {
	return f.readyErr
}

func (f *fakeConsumerSession) Close() error {
	f.closeCount++
	return nil
}

type fakeConsumerConnector struct {
	mu        sync.Mutex
	callCount int
	connect   func(ctx context.Context, call int) (consumerSession, error)
}

type fakeSupervisorMetrics struct {
	reconnectResults []string
	connected        []bool
}

func (f *fakeSupervisorMetrics) IncConsumerReconnect(result string) {
	f.reconnectResults = append(f.reconnectResults, result)
}

func (f *fakeSupervisorMetrics) SetConsumerConnected(connected bool) {
	f.connected = append(f.connected, connected)
}

func (f *fakeConsumerConnector) Connect(ctx context.Context) (consumerSession, error) {
	f.mu.Lock()
	f.callCount++
	call := f.callCount
	f.mu.Unlock()
	return f.connect(ctx, call)
}

func (f *fakeConsumerConnector) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callCount
}

func TestConsumerSupervisorRun_ReconnectsAfterConnectError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	connected := make(chan struct{})

	connector := &fakeConsumerConnector{
		connect: func(ctx context.Context, call int) (consumerSession, error) {
			if call == 1 {
				return nil, errConsumerReconnect
			}
			close(connected)
			return &fakeConsumerSession{
				consumer: &fakeManagedConsumer{
					consume: func(ctx context.Context) error {
						<-ctx.Done()
						return nil
					},
				},
			}, nil
		},
	}
	supervisor := NewConsumerSupervisor(connector, time.Millisecond, time.Millisecond, newSupervisorTestLogger(), nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- supervisor.Run(ctx)
	}()

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnect")
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for supervisor to stop")
	}
	if connector.calls() < 2 {
		t.Fatalf("expected at least two connect calls, got %d", connector.calls())
	}
}

func TestConsumerSupervisorRun_RecordsMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	connected := make(chan struct{})

	connector := &fakeConsumerConnector{
		connect: func(ctx context.Context, call int) (consumerSession, error) {
			if call == 1 {
				return nil, errConsumerReconnect
			}
			close(connected)
			return &fakeConsumerSession{
				consumer: &fakeManagedConsumer{
					consume: func(ctx context.Context) error {
						<-ctx.Done()
						return nil
					},
				},
			}, nil
		},
	}
	metrics := &fakeSupervisorMetrics{}
	supervisor := NewConsumerSupervisor(
		connector,
		time.Millisecond,
		time.Millisecond,
		newSupervisorTestLogger(),
		metrics,
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- supervisor.Run(ctx)
	}()

	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reconnect")
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for supervisor to stop")
	}

	wantReconnects := []string{"connect_error", "success"}
	if len(metrics.reconnectResults) != len(wantReconnects) {
		t.Fatalf("expected reconnect metrics %v, got %v", wantReconnects, metrics.reconnectResults)
	}
	for i := range wantReconnects {
		if metrics.reconnectResults[i] != wantReconnects[i] {
			t.Fatalf("expected reconnect metrics %v, got %v", wantReconnects, metrics.reconnectResults)
		}
	}
	if len(metrics.connected) != 3 ||
		metrics.connected[0] != false ||
		metrics.connected[1] != true ||
		metrics.connected[2] != false {
		t.Fatalf("expected connected false, true, false, got %v", metrics.connected)
	}
}

func TestConsumerSupervisorRun_ReconnectsAfterConsumeError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	secondSession := make(chan struct{})

	connector := &fakeConsumerConnector{
		connect: func(ctx context.Context, call int) (consumerSession, error) {
			if call == 1 {
				return &fakeConsumerSession{
					consumer: &fakeManagedConsumer{
						consume: func(ctx context.Context) error {
							return errConsumerReconnect
						},
					},
				}, nil
			}
			close(secondSession)
			return &fakeConsumerSession{
				consumer: &fakeManagedConsumer{
					consume: func(ctx context.Context) error {
						<-ctx.Done()
						return nil
					},
				},
			}, nil
		},
	}
	supervisor := NewConsumerSupervisor(connector, time.Millisecond, time.Millisecond, newSupervisorTestLogger(), nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- supervisor.Run(ctx)
	}()

	select {
	case <-secondSession:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second session")
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for supervisor to stop")
	}
}

func TestConsumerSupervisorRun_CancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	connectCalled := make(chan struct{})

	connector := &fakeConsumerConnector{
		connect: func(ctx context.Context, call int) (consumerSession, error) {
			close(connectCalled)
			return nil, errConsumerReconnect
		},
	}
	supervisor := NewConsumerSupervisor(connector, 10*time.Second, 10*time.Second, newSupervisorTestLogger(), nil)

	errCh := make(chan error, 1)
	go func() {
		errCh <- supervisor.Run(ctx)
	}()

	select {
	case <-connectCalled:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for connect")
	}
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("supervisor did not stop during backoff")
	}
}

func TestConsumerSupervisorReadyAndClose(t *testing.T) {
	connector := &fakeConsumerConnector{}
	supervisor := NewConsumerSupervisor(connector, time.Second, time.Second, newSupervisorTestLogger(), nil)

	if err := supervisor.Ready(context.Background()); err == nil {
		t.Fatal("expected disconnected supervisor to be not ready")
	}

	session := &fakeConsumerSession{
		consumer: &fakeManagedConsumer{},
		readyErr: errConsumerReconnect,
	}
	supervisor.replaceSession(session)
	if err := supervisor.Ready(context.Background()); !errors.Is(err, errConsumerReconnect) {
		t.Fatalf("expected session ready error, got %v", err)
	}

	if err := supervisor.Close(); err != nil {
		t.Fatalf("close supervisor: %v", err)
	}
	if session.closeCount != 1 {
		t.Fatalf("expected session close once, got %d", session.closeCount)
	}
}

func newSupervisorTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
