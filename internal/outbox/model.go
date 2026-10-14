package outbox

import "time"

const (
	StatusPending   = "pending"
	StatusPublished = "published"

	EventTypeVideoPublished = "video.published"
)

// Event 对应 MySQL 中的 outbox_events 表。
//
// Outbox 的核心思想是：业务数据和待发送事件在同一个数据库事务里落盘。
// 这样只要视频记录存在，对应事件就一定存在，不会出现“视频保存成功但消息丢失”。
type Event struct {
	ID          uint64 `gorm:"column:id;primaryKey;autoIncrement"`
	EventID     string `gorm:"column:event_id;size:36;not null;uniqueIndex:uk_outbox_events_event_id"`
	EventType   string `gorm:"column:event_type;size:100;not null"`
	AggregateID uint64 `gorm:"column:aggregate_id;not null"`
	// Payload 用 string 而不是 []byte。
	//
	// 一方面它语义上就是 JSON 文本，另一方面这是个实打实的驱动陷阱：
	// DSN 开了 interpolateParams 之后，[]byte 参数会被转义成
	// _binary'...' 字面量（binary 字符集），MySQL 会拒绝用它构造 JSON 值
	// （Error 3144: Cannot create a JSON value from a string with
	// CHARACTER SET 'binary'）；string 参数则使用连接字符集，可以正常写入。
	Payload     string     `gorm:"column:payload;type:json;not null"`
	Status      string     `gorm:"column:status;size:20;not null;default:pending"`
	RetryCount  int        `gorm:"column:retry_count;not null;default:0"`
	LastError   string     `gorm:"column:last_error;size:500;not null;default:''"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null"`
	PublishedAt *time.Time `gorm:"column:published_at"`
}

func (Event) TableName() string {
	return "outbox_events"
}

// VideoPublishedPayload 是 video.published 事件的负载。
//
// 消费者只依赖这些字段，不需要反向查询 outbox_events。
type VideoPublishedPayload struct {
	VideoID     uint64    `json:"video_id"`
	AuthorID    uint64    `json:"author_id"`
	PublishedAt time.Time `json:"published_at"`
}
