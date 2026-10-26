package message

import "time"

const (
	DefaultPageSize          = 30
	MaxPageSize              = 100
	DefaultConversationLimit = 50
	MaxContentLength         = 2000
)

type Message struct {
	ID         uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	SenderID   uint64     `gorm:"column:sender_id;not null"`
	ReceiverID uint64     `gorm:"column:receiver_id;not null"`
	Content    string     `gorm:"column:content;size:2000;not null"`
	ReadAt     *time.Time `gorm:"column:read_at"`
	CreatedAt  time.Time  `gorm:"column:created_at;not null"`
}

func (Message) TableName() string {
	return "messages"
}
