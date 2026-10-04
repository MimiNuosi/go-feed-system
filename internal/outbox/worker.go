package outbox

import (
	"context"
	"log/slog"
	"time"
)

const DefaultPublishInterval = time.Second

// PendingPublisher 提供一次待处理事件发布能力。
//
// Worker 只依赖这个小接口，测试时可以用 Fake 精确控制每次调用的结果。
type PendingPublisher interface {
	PublishPending(ctx context.Context) (int, error)
}

// Worker 周期性触发 Outbox 发布。
type Worker struct {
	publisher PendingPublisher
	interval  time.Duration
	logger    *slog.Logger
}

func NewWorker(
	publisher PendingPublisher,
	interval time.Duration,
	logger *slog.Logger,
) *Worker {
	if interval <= 0 {
		interval = DefaultPublishInterval
	}

	return &Worker{
		publisher: publisher,
		interval:  interval,
		logger:    logger,
	}
}

// Run 启动后台调度循环，直到 ctx 被取消。
func (w *Worker) Run(ctx context.Context) error {
	w.publishOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.publishOnce(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

func (w *Worker) publishOnce(ctx context.Context) {
	count, err := w.publisher.PublishPending(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.logger.Error("outbox worker: publish pending failed", "error", err)
		return
	}
	if count > 0 {
		w.logger.Info("outbox worker: published pending events", "count", count)
	}
}
