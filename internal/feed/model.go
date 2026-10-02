package feed

import "time"

// Cursor 是 Feed 的复合游标。
//
// created_at 与 id 必须一起使用，id 用于解决同一毫秒内多条视频的排序稳定性。
type Cursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        uint64    `json:"id"`
}

// VideoRecord 是 Feed Repository 查询出的基础视频数据。
//
// 作者信息和点赞状态由 Feed Service 批量补齐，避免在 Repository 中跨领域 JOIN。
type VideoRecord struct {
	ID          uint64    `gorm:"column:id"`
	AuthorID    uint64    `gorm:"column:author_id"`
	Title       string    `gorm:"column:title"`
	Description string    `gorm:"column:description"`
	ContentType string    `gorm:"column:content_type"`
	CreatedAt   time.Time `gorm:"column:created_at"`
}

type AuthorInfo struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
}

// Item 是 Feed 返回给前端的单条数据。
type Item struct {
	ID          uint64     `json:"id"`
	Author      AuthorInfo `json:"author"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	ContentType string     `json:"content_type"`
	CreatedAt   time.Time  `json:"created_at"`
	LikeCount   int64      `json:"like_count"`
	IsLikedBy   bool       `json:"is_liked_by"`
}

// Page 是 Feed 的游标分页结果。
type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}
