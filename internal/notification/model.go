package notification

import "time"

type Notification struct {
	ID        uint64     `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint64     `gorm:"column:user_id;not null"`
	ActorID   uint64     `gorm:"column:actor_id;not null"`
	Kind      string     `gorm:"column:kind;size:20;not null"`
	VideoID   *uint64    `gorm:"column:video_id"`
	Content   string     `gorm:"column:content;size:500;not null"`
	ReadAt    *time.Time `gorm:"column:read_at"`
	CreatedAt time.Time  `gorm:"column:created_at;not null"`
}

func (Notification) TableName() string {
	return "notifications"
}
