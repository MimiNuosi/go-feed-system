package video

import "time"

const (
	StatusReady = "ready"
)

// Video 对应 MySQL 中的 videos 表，只保存元数据，不保存视频二进制。
type Video struct {
	ID               uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	AuthorID         uint64    `gorm:"column:author_id;not null;index:idx_videos_author_created_id,priority:1"`
	Title            string    `gorm:"column:title;size:100;not null"`
	Description      string    `gorm:"column:description;size:1000;not null"`
	StorageKey       string    `gorm:"column:storage_key;size:512;not null;uniqueIndex:uk_videos_storage_key"`
	OriginalFilename string    `gorm:"column:original_filename;size:255;not null"`
	ContentType      string    `gorm:"column:content_type;size:100;not null"`
	SizeBytes        int64     `gorm:"column:size_bytes;not null"`
	Status           string    `gorm:"column:status;size:20;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;index:idx_videos_author_created_id,priority:2"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null"`
}

func (Video) TableName() string {
	return "videos"
}

// AuthorInfo 是作者信息的摘要
type AuthorInfo struct {
	ID       uint64 `json:"id"`
	Username string `json:"username"`
}

// VideoDetail 是视频详情接口返回给前端的完整数据结构
type VideoDetail struct {
	ID          uint64     `json:"id"`
	Author      AuthorInfo `json:"author"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	ContentType string     `json:"content_type"`
	SizeBytes   int64      `json:"size_bytes"`
	Status      string     `json:"status"`
	LikeCount   int64      `json:"like_count"`
	IsLikedBy   bool       `json:"is_liked_by"`
}
