package interaction

import (
	"time"

	"gorm.io/gorm"
)

// Comment 表示视频下的一级评论。
//
// 第一阶段不支持回复树，因此没有 parent_id、root_id 等字段。
// 删除采用 GORM 软删除，普通查询会自动过滤 deleted_at 非空的记录。
type Comment struct {
	ID        uint64         `gorm:"column:id;primaryKey;autoIncrement"`
	VideoID   uint64         `gorm:"column:video_id;not null"`
	UserID    uint64         `gorm:"column:user_id;not null"`
	Content   string         `gorm:"column:content;size:1000;not null"`
	CreatedAt time.Time      `gorm:"column:created_at;not null"`
	UpdatedAt time.Time      `gorm:"column:updated_at;not null"`
	DeletedAt gorm.DeletedAt `gorm:"column:deleted_at;index"`
}

func (Comment) TableName() string {
	return "comments"
}

// CommentItem 是评论列表返回给前端的单条数据。
type CommentItem struct {
	ID        uint64    `json:"id"`
	VideoID   uint64    `json:"video_id"`
	UserID    uint64    `json:"user_id"`
	Username  string    `json:"username"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// CommentPage 是评论列表的游标分页结果。
type CommentPage struct {
	Items      []CommentItem `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}
