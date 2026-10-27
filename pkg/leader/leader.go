package leader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	defaultLeaseTTL        = 15 * time.Second
	defaultRenewInterval   = 5 * time.Second
	defaultAcquireInterval = 2 * time.Second
	defaultReleaseTimeout  = 2 * time.Second
)

var errLeaseLost = errors.New("background worker lease lost")

type Worker interface {
	Run(ctx context.Context) error
}

type Store interface {
	Acquire(ctx context.Context, name string, owner string, ttl time.Duration) (bool, error)
	Renew(ctx context.Context, name string, owner string, ttl time.Duration) (bool, error)
	Release(ctx context.Context, name string, owner string) error
}

type Metrics interface {
	SetBackgroundWorkerLeader(worker string, leader bool)
	IncBackgroundWorkerLease(worker string, result string)
}

type Options struct {
	LeaseTTL        time.Duration
	RenewInterval   time.Duration
	AcquireInterval time.Duration
}

type Elector struct {
	store           Store
	owner           string
	logger          *slog.Logger
	metrics         Metrics
	leaseTTL        time.Duration
	renewInterval   time.Duration
	acquireInterval time.Duration
}

func NewRedisElector(
	client *redis.Client,
	options Options,
	logger *slog.Logger,
	metrics Metrics,
) (*Elector, error) {
	if client == nil {
		return nil, fmt.Errorf("redis client is nil")
	}
	return NewElector(NewRedisStore(client), options, logger, metrics)
}

func NewElector(
	store Store,
	options Options,
	logger *slog.Logger,
	metrics Metrics,
) (*Elector, error) {
	if store == nil {
		return nil, fmt.Errorf("leader store is nil")
	}
	if options.LeaseTTL <= 0 {
		options.LeaseTTL = defaultLeaseTTL
	}
	if options.RenewInterval <= 0 {
		options.RenewInterval = defaultRenewInterval
	}
	if options.AcquireInterval <= 0 {
		options.AcquireInterval = defaultAcquireInterval
	}
	if options.RenewInterval >= options.LeaseTTL {
		return nil, fmt.Errorf("renew interval must be shorter than lease TTL")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Elector{
		store:           store,
		owner:           uuid.NewString(),
		logger:          logger,
		metrics:         metrics,
		leaseTTL:        options.LeaseTTL,
		renewInterval:   options.RenewInterval,
		acquireInterval: options.AcquireInterval,
	}, nil
}

func (e *Elector) Wrap(name string, worker Worker) Worker {
	if e == nil || worker == nil {
		return worker
	}
	return &electedWorker{elector: e, name: name, worker: worker}
}

type electedWorker struct {
	elector *Elector
	name    string
	worker  Worker
}

func (w *electedWorker) Run(ctx context.Context) error {
	if w == nil || w.elector == nil || w.worker == nil {
		return fmt.Errorf("leader worker is not configured")
	}
	e := w.elector
	e.setLeader(w.name, false)
	defer e.setLeader(w.name, false)

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		acquired, err := e.store.Acquire(ctx, w.name, e.owner, e.leaseTTL)
		if err != nil {
			e.incLease(w.name, "acquire_error")
			e.logger.Warn("background worker acquire failed", "worker", w.name, "error", err)
			if !wait(ctx, e.acquireInterval) {
				return nil
			}
			continue
		}
		if !acquired {
			e.incLease(w.name, "waiting")
			if !wait(ctx, e.acquireInterval) {
				return nil
			}
			continue
		}

		e.incLease(w.name, "acquired")
		e.setLeader(w.name, true)
		e.logger.Info("background worker became leader", "worker", w.name)

		runErr := e.runAsLeader(ctx, w.name, w.worker)
		e.setLeader(w.name, false)
		e.release(w.name)

		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(runErr, errLeaseLost) {
			if !wait(ctx, e.acquireInterval) {
				return nil
			}
			continue
		}
		if runErr != nil {
			return runErr
		}
		return fmt.Errorf("background worker %s stopped unexpectedly", w.name)
	}
}

func (e *Elector) runAsLeader(
	ctx context.Context,
	name string,
	worker Worker,
) error {
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- worker.Run(workerCtx)
	}()

	ticker := time.NewTicker(e.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			return nil
		case err := <-resultCh:
			cancel()
			if err != nil {
				return fmt.Errorf("background worker %s: %w", name, err)
			}
			return nil
		case <-ticker.C:
			renewed, err := e.store.Renew(ctx, name, e.owner, e.leaseTTL)
			if err != nil {
				e.incLease(name, "renew_error")
				cancel()
				return fmt.Errorf("%w: %v", errLeaseLost, err)
			}
			if !renewed {
				e.incLease(name, "renew_lost")
				cancel()
				e.logger.Warn("background worker lease lost", "worker", name)
				return errLeaseLost
			}
			e.incLease(name, "renewed")
		}
	}
}

func (e *Elector) release(name string) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultReleaseTimeout)
	defer cancel()
	if err := e.store.Release(ctx, name, e.owner); err != nil {
		e.incLease(name, "release_error")
		e.logger.Warn("background worker release failed", "worker", name, "error", err)
		return
	}
	e.incLease(name, "released")
}

func (e *Elector) setLeader(name string, leader bool) {
	if e.metrics != nil {
		e.metrics.SetBackgroundWorkerLeader(name, leader)
	}
}

func (e *Elector) incLease(name string, result string) {
	if e.metrics != nil {
		e.metrics.IncBackgroundWorkerLease(name, result)
	}
}

func wait(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

type RedisStore struct {
	client *redis.Client
}

func NewRedisStore(client *redis.Client) *RedisStore {
	return &RedisStore{client: client}
}

var renewScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("PEXPIRE", KEYS[1], ARGV[2])
end
return 0
`)

var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
end
return 0
`)

func (s *RedisStore) Acquire(
	ctx context.Context,
	name string,
	owner string,
	ttl time.Duration,
) (bool, error) {
	if s == nil || s.client == nil {
		return false, fmt.Errorf("redis leader store is not configured")
	}
	return s.client.SetNX(ctx, leaseKey(name), owner, ttl).Result()
}

func (s *RedisStore) Renew(
	ctx context.Context,
	name string,
	owner string,
	ttl time.Duration,
) (bool, error) {
	if s == nil || s.client == nil {
		return false, fmt.Errorf("redis leader store is not configured")
	}
	result, err := renewScript.Run(
		ctx,
		s.client,
		[]string{leaseKey(name)},
		owner,
		ttl.Milliseconds(),
	).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}

func (s *RedisStore) Release(
	ctx context.Context,
	name string,
	owner string,
) error {
	if s == nil || s.client == nil {
		return fmt.Errorf("redis leader store is not configured")
	}
	_, err := releaseScript.Run(ctx, s.client, []string{leaseKey(name)}, owner).Result()
	return err
}

func leaseKey(name string) string {
	return "background:lease:" + name
}
