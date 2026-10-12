package hot

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

const (
	// DefaultViewWritebackInterval 是浏览量回写的轮询间隔。
	DefaultViewWritebackInterval = 5 * time.Second
	// DefaultViewWritebackBatchSize 是单轮最多回写的视频数。
	DefaultViewWritebackBatchSize = 1000
)

// PendingViewStore 提供待回写浏览量的读取与确认能力（Redis 侧）。
//
// 它刻意做成"读取 + 单独 ack 两个动作"，而不是"读取即删除"：
// 只有 MySQL 写成功后才 ack，失败时增量还留在 Redis 里，下一轮会被重新读到。
// 于是 Redis Hash 本身就是重试队列，不需要额外的失败清单。
type PendingViewStore interface {
	// PendingViews 返回最多 limit 条待回写增量（videoID -> 增量）。
	PendingViews(ctx context.Context, limit int) (map[uint64]int64, error)
	// AckPendingView 在增量成功落库后确认（删除）该条。
	AckPendingView(ctx context.Context, videoID uint64) error
}

// ViewCountWriter 把浏览量增量累加进视频元数据（MySQL 侧）。
type ViewCountWriter interface {
	AddViewCount(ctx context.Context, videoID uint64, delta int64) error
}

// ViewWritebackMetrics 是回写阶段需要的窄指标接口。
type ViewWritebackMetrics interface {
	IncViewWriteback(result string)
}

// ViewWriteback 负责把 Redis 里的浏览量增量回写到 MySQL。
type ViewWriteback struct {
	pending   PendingViewStore
	writer    ViewCountWriter
	batchSize int
	logger    *slog.Logger
	metrics   ViewWritebackMetrics
}

func NewViewWriteback(
	pending PendingViewStore,
	writer ViewCountWriter,
	batchSize int,
	logger *slog.Logger,
	metrics ViewWritebackMetrics,
) *ViewWriteback {
	if batchSize <= 0 {
		batchSize = DefaultViewWritebackBatchSize
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &ViewWriteback{
		pending:   pending,
		writer:    writer,
		batchSize: batchSize,
		logger:    logger,
		metrics:   metrics,
	}
}

// WritebackPending 回写一批增量，返回成功落库并已确认的条目数。
//
// 顺序很关键，必须是"先写 MySQL、后删 Redis 增量"：
// 反过来的话，进程一旦死在"删完增量、还没写库"之间，这批浏览就永久丢了。
// 现在的顺序只会多算，不会少算，符合"浏览量宁可多算"的取舍。
func (w *ViewWriteback) WritebackPending(ctx context.Context) (int, error) {
	pendings, err := w.pending.PendingViews(ctx, w.batchSize)
	if err != nil {
		return 0, fmt.Errorf("view writeback: list pending: %w", err)
	}
	if len(pendings) == 0 {
		return 0, nil
	}

	// 排序只是为了处理顺序稳定，方便日志排查和测试断言。
	videoIDs := make([]uint64, 0, len(pendings))
	for videoID := range pendings {
		videoIDs = append(videoIDs, videoID)
	}
	slices.Sort(videoIDs)

	written := 0
	for _, videoID := range videoIDs {
		if err := ctx.Err(); err != nil {
			return written, fmt.Errorf("view writeback: context canceled: %w", err)
		}

		delta := pendings[videoID]
		if delta <= 0 {
			// 正常路径只会写正数（RecordView 的 Lua 里是 HINCRBY +1）。
			// 出现非正数说明是脏数据，直接 ack 掉并告警，避免它每轮都卡在这。
			w.logger.Warn(
				"view writeback: skipping non-positive delta",
				"video_id", videoID,
				"delta", delta,
			)
			if err := w.ack(ctx, videoID); err != nil {
				return written, err
			}
			continue
		}

		if err := w.writer.AddViewCount(ctx, videoID, delta); err != nil {
			w.incMetric("write_error")
			w.logger.Error(
				"view writeback: add view count failed",
				"video_id", videoID,
				"delta", delta,
				"error", err,
			)
			// 与 Outbox 发布保持一致的策略：遇到错误立即停手。
			// 这一条没有 ack，下一轮还会被读到，所以不会丢数据。
			return written, fmt.Errorf(
				"view writeback: add view count for video %d: %w",
				videoID,
				err,
			)
		}

		if err := w.ack(ctx, videoID); err != nil {
			return written, err
		}

		w.incMetric("success")
		written++
	}

	return written, nil
}

// ack 在增量落库成功后才调用。ack 失败会让下一轮重复累加一次，
// 这是本设计明确接受的误差（宁可多算，不可少算）。
func (w *ViewWriteback) ack(ctx context.Context, videoID uint64) error {
	if err := w.pending.AckPendingView(ctx, videoID); err != nil {
		w.incMetric("ack_error")
		w.logger.Error(
			"view writeback: ack pending view failed",
			"video_id", videoID,
			"error", err,
		)
		return fmt.Errorf("view writeback: ack video %d: %w", videoID, err)
	}
	return nil
}

func (w *ViewWriteback) incMetric(result string) {
	if w.metrics != nil {
		w.metrics.IncViewWriteback(result)
	}
}

// PendingWriteback 提供一次待回写处理能力。
//
// Worker 只依赖这个窄接口，测试时可以用 Fake 精确控制每次调用的结果。
type PendingWriteback interface {
	WritebackPending(ctx context.Context) (int, error)
}

// ViewWritebackWorker 周期性触发浏览量回写。
type ViewWritebackWorker struct {
	writeback PendingWriteback
	interval  time.Duration
	logger    *slog.Logger
}

func NewViewWritebackWorker(
	writeback PendingWriteback,
	interval time.Duration,
	logger *slog.Logger,
) *ViewWritebackWorker {
	if interval <= 0 {
		interval = DefaultViewWritebackInterval
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &ViewWritebackWorker{
		writeback: writeback,
		interval:  interval,
		logger:    logger,
	}
}

// Run 启动后台调度循环，直到 ctx 被取消。
func (w *ViewWritebackWorker) Run(ctx context.Context) error {
	w.writebackOnce(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			w.writebackOnce(ctx)
		case <-ctx.Done():
			return nil
		}
	}
}

func (w *ViewWritebackWorker) writebackOnce(ctx context.Context) {
	count, err := w.writeback.WritebackPending(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		w.logger.Error(
			"view writeback worker: writeback failed",
			"written", count,
			"error", err,
		)
		return
	}

	if count > 0 {
		w.logger.Info("view writeback worker: flushed view counts", "videos", count)
		return
	}

	// 空批次降到 Debug：既能确认 worker 还活着，又不会每 5 秒刷一屏日志。
	w.logger.Debug("view writeback worker: nothing to flush")
}
