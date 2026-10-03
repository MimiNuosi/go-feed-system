package feed

import (
	"context"
	"time"
)

// Inbox 是 Feed 收件箱的抽象。
//
// 后续 Redis 实现使用 ZSET：
//
//	key:    feed:inbox:<userID>
//	member: videoID
//	score:  publishedAt 的毫秒时间戳
//
// 接口不依赖 Redis 客户端，方便先用 Fake 做 Service 测试。
type Inbox interface {
	// Add 把同一条视频加入多个用户的收件箱。
	Add(ctx context.Context, userIDs []uint64, videoID uint64, publishedAt time.Time) error

	// List 按视频 ID 游标读取收件箱。
	List(ctx context.Context, userID uint64, cursor uint64, limit int) ([]uint64, error)

	// Trim 把单个用户的收件箱裁剪到实现配置的最大长度。
	Trim(ctx context.Context, userID uint64) error
}
