package interaction

import "context"

const (
	NotifyLike    = "like"
	NotifyComment = "comment"
	NotifyFollow  = "follow"
)

// Notifier 是互动模块需要的通知能力。
//
// 定义在使用方，notification 包实现它；nil 表示不启用通知。
type Notifier interface {
	Notify(
		ctx context.Context,
		recipientID uint64,
		actorID uint64,
		kind string,
		videoID uint64,
		content string,
	) error
}
