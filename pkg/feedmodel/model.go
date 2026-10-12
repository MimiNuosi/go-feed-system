package feedmodel

import "time"

type AuthorInfo struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
}

// Item 是 Feed 类接口返回给前端的单条视频数据。
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

// Page 是 Feed 类接口的游标分页结果。
type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}
