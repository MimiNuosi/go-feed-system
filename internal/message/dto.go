package message

import "time"

type Item struct {
	ID         uint64    `json:"id"`
	SenderID   uint64    `json:"sender_id"`
	ReceiverID uint64    `json:"receiver_id"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
	Read       bool      `json:"read"`
}

type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

type Peer struct {
	ID        uint64 `json:"id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type LastMessage struct {
	Content   string    `json:"content"`
	SenderID  uint64    `json:"sender_id"`
	CreatedAt time.Time `json:"created_at"`
}

type Conversation struct {
	Peer        Peer        `json:"peer"`
	LastMessage LastMessage `json:"last_message"`
	UnreadCount int64       `json:"unread_count"`
}

type ConversationList struct {
	Items []Conversation `json:"items"`
}
