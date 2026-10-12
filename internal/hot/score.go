package hot

import (
	"context"
	"log/slog"
	"time"
)

// ScoreWriter 只负责把热度变化写入排行榜。
type ScoreWriter interface {
	AddScore(
		ctx context.Context,
		videoID uint64,
		delta int64,
		at time.Time,
	) error
}

// ScoreRecorder 是供业务模块调用的 best-effort 热度记录器。
//
// 点赞、评论等主流程不应该因为排行榜更新失败而失败。
type ScoreRecorder struct {
	writer ScoreWriter
	logger *slog.Logger
}

func NewScoreRecorder(writer ScoreWriter, logger *slog.Logger) *ScoreRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &ScoreRecorder{
		writer: writer,
		logger: logger,
	}
}

// AddScore 更新排行榜；失败时只记录日志，不向调用方返回错误。
func (r *ScoreRecorder) AddScore(
	ctx context.Context,
	videoID uint64,
	delta int64,
	at time.Time,
) {
	if err := r.writer.AddScore(ctx, videoID, delta, at); err != nil {
		r.logger.Warn(
			"hot score update failed",
			"video_id", videoID,
			"delta", delta,
			"error", err,
		)
	}
}
