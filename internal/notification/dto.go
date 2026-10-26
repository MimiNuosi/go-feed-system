package notification

import "time"

// Actor 是通知响应里展示的操作者信息，避免前端再为每条通知查一次用户。
type Actor struct {
	ID        uint64 `json:"id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

// Item 是通知模块对外的稳定响应结构。
// 数据库模型可以演进，但前端只依赖这里定义的 JSON 字段。
type Item struct {
	ID        uint64    `json:"id"`
	UserID    uint64    `json:"-"`
	Actor     Actor     `json:"actor"`
	Kind      string    `json:"kind"`
	VideoID   *uint64   `json:"video_id,omitempty"`
	Content   string    `json:"content,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Read      bool      `json:"read"`
}

type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}
