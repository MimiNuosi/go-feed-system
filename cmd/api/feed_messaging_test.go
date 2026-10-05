package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	errTestConsumer = errors.New("consumer failed")
	errTestWorker   = errors.New("worker failed")
)

type fakeFeedConsumerRunner struct {
	run func(ctx context.Context) error
}

func (f *fakeFeedConsumerRunner) Consume(ctx context.Context) error {
	return f.run(ctx)
}

type fakeOutboxWorkerRunner struct {
	run func(ctx context.Context) error
}

func (f *fakeOutboxWorkerRunner) Run(ctx context.Context) error {
	return f.run(ctx)
}

func TestFeedMessagingRun_ConsumerErrorCancelsWorker(t *testing.T) {
	workerStarted := make(chan struct{})
	workerCanceled := make(chan struct{})

	messaging := &feedMessaging{
		consumer: &fakeFeedConsumerRunner{
			run: func(ctx context.Context) error {
				<-workerStarted
				return errTestConsumer
			},
		},
		worker: &fakeOutboxWorkerRunner{
			run: func(ctx context.Context) error {
				close(workerStarted)
				<-ctx.Done()
				close(workerCanceled)
				return nil
			},
		},
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- messaging.Run(context.Background())
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, errTestConsumer) {
			t.Fatalf("expected consumer error, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Run to return")
	}

	select {
	case <-workerCanceled:
	case <-time.After(time.Second):
		t.Fatal("worker was not canceled")
	}
}

func TestFeedMessagingRun_WorkerErrorCancelsConsumer(t *testing.T) {
	consumerStarted := make(chan struct{})
	consumerCanceled := make(chan struct{})

	messaging := &feedMessaging{
		consumer: &fakeFeedConsumerRunner{
			run: func(ctx context.Context) error {
				close(consumerStarted)
				<-ctx.Done()
				close(consumerCanceled)
				return nil
			},
		},
		worker: &fakeOutboxWorkerRunner{
			run: func(ctx context.Context) error {
				<-consumerStarted
				return errTestWorker
			},
		},
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
	case <-consumerCanceled:
	case <-time.After(time.Second):
		t.Fatal("consumer was not canceled")
	}
}

func TestFeedMessagingRun_NormalCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	consumerStarted := make(chan struct{})
	workerStarted := make(chan struct{})

	messaging := &feedMessaging{
		consumer: &fakeFeedConsumerRunner{
			run: func(ctx context.Context) error {
				close(consumerStarted)
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
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- messaging.Run(ctx)
	}()

	select {
	case <-consumerStarted:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for consumer")
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
