package interaction

import (
	"context"
	"time"
)

// HotScoreRecorder 是互动模块更新热榜分数的窄接口。
type HotScoreRecorder interface {
	AddScore(
		ctx context.Context,
		videoID uint64,
		delta int64,
		at time.Time,
	)
}
