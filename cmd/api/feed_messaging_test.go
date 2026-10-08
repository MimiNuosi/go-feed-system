package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-feed-system/internal/feed"
	"go-feed-system/internal/outbox"
	"go-feed-system/pkg/config"
)

var (
	errTestSupervisor = errors.New("supervisor failed")
	errTestWorker     = errors.New("worker failed")
)

type fakeConsumerSupervisorRunner struct {
	run        func(ctx context.Context) error
	readyErr   error
	closeCount int
}

func (f *fakeConsumerSupervisorRunner) Run(ctx context.Context) error {
	return f.run(ctx)
}

func (f *fakeConsumerSupervisorRunner) Ready(ctx context.Context) error {
	return f.readyErr
}

func (f *fakeConsumerSupervisorRunner) Close() error {
	f.closeCount++
	return nil
}

type fakeOutboxWorkerRunner struct {
	run func(ctx context.Context) error
}

func (f *fakeOutboxWorkerRunner) Run(ctx context.Context) error {
	return f.run(ctx)
}

func TestNewFeedMessaging_DoesNotConnectRabbitMQ(t *testing.T) {
	messaging, err := newFeedMessaging(
		config.Config{
			Feed: config.FeedConfig{
				FanoutFollowerThreshold: feed.DefaultFanoutFollowerThreshold,
			},
		},
		outbox.NewGORMRepository(nil),
		&connectorInbox{},
		&connectorFollowerReader{},
		nil,
		newSupervisorTestLogger(),
	)
	if err != nil {
		t.Fatalf("create feed messaging: %v", err)
	}
	if messaging.consumerSupervisor == nil {
		t.Fatal("expected consumer supervisor")
	}
	if messaging.producer == nil {
		t.Fatal("expected producer")
	}
	if messaging.retryProducer == nil {
		t.Fatal("expected retry producer")
	}
	if messaging.worker == nil {
		t.Fatal("expected outbox worker")
	}
}

func TestFeedMessagingRun_SupervisorErrorDoesNotStopWorker(t *testing.T) {
	supervisorStarted := make(chan struct{})
	workerStarted := make(chan struct{})

	messaging := &feedMessaging{
		consumerSupervisor: &fakeConsumerSupervisorRunner{
			run: func(ctx context.Context) error {
				close(supervisorStarted)
				return errTestSupervisor
			},
		},
		worker: &fakeOutboxWorkerRunner{
			run: func(ctx context.Context) error {
				close(workerStarted)
				<-ctx.Done()
				return nil
			},
		},
		logger: newSupervisorTestLogger(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- messaging.Run(ctx)
	}()

	select {
	case <-supervisorStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for supervisor")
	}
	select {
	case <-workerStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker")
	}

	select {
	case err := <-errCh:
		t.Fatalf("Run returned after supervisor error: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run to stop")
	}
}

func TestFeedMessagingRun_WorkerErrorCancelsSupervisor(t *testing.T) {
	supervisorStarted := make(chan struct{})
	supervisorCanceled := make(chan struct{})

	messaging := &feedMessaging{
		consumerSupervisor: &fakeConsumerSupervisorRunner{
			run: func(ctx context.Context) error {
				close(supervisorStarted)
				<-ctx.Done()
				close(supervisorCanceled)
				return nil
			},
		},
		worker: &fakeOutboxWorkerRunner{
			run: func(ctx context.Context) error {
				<-supervisorStarted
				return errTestWorker
			},
		},
		logger: newSupervisorTestLogger(),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- messaging.Run(context.Background())
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, errTestWorker) {
			t.Fatalf("expected worker error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run to return")
	}

	select {
	case <-supervisorCanceled:
	case <-time.After(time.Second):
		t.Fatal("supervisor was not canceled")
	}
}

func TestFeedMessagingRun_NormalCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	supervisorStarted := make(chan struct{})
	workerStarted := make(chan struct{})

	messaging := &feedMessaging{
		consumerSupervisor: &fakeConsumerSupervisorRunner{
			run: func(ctx context.Context) error {
				close(supervisorStarted)
				<-ctx.Done()
				return nil
			},
		},
		worker: &fakeOutboxWorkerRunner{
			run: func(ctx context.Context) error {
				close(workerStarted)
				<-ctx.Done()
				return nil
			},
		},
		logger: newSupervisorTestLogger(),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- messaging.Run(ctx)
	}()

	select {
	case <-supervisorStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for supervisor")
	}
	select {
	case <-workerStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for worker")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

func TestFeedMessagingReady_UsesSupervisor(t *testing.T) {
	messaging := &feedMessaging{
		consumerSupervisor: &fakeConsumerSupervisorRunner{
			readyErr: errTestSupervisor,
		},
	}

	if err := messaging.Ready(context.Background()); !errors.Is(err, errTestSupervisor) {
		t.Fatalf("expected supervisor ready error, got %v", err)
	}
}

func TestFeedMessagingClose_ClosesSupervisor(t *testing.T) {
	supervisor := &fakeConsumerSupervisorRunner{}
	messaging := &feedMessaging{
		consumerSupervisor: supervisor,
	}

	if err := messaging.Close(); err != nil {
		t.Fatalf("close feed messaging: %v", err)
	}
	if supervisor.closeCount != 1 {
		t.Fatalf("expected supervisor close once, got %d", supervisor.closeCount)
	}
}
