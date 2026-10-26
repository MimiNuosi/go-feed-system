package upload

import "time"

const (
	// DefaultChunkSize 与参考项目保持一致：5MB。
	DefaultChunkSize = 5 << 20
	// MinChunkSize / MaxChunkSize 防止客户端把分片设得过小或过大。
	MinChunkSize = 1 << 20
	MaxChunkSize = 10 << 20

	StatusUploading = "uploading"
	StatusCompleted = "completed"
)

// Session 是一次分片上传的元数据。
type Session struct {
	ID          string    `gorm:"column:id;primaryKey"`
	AuthorID    uint64    `gorm:"column:author_id;not null"`
	FileName    string    `gorm:"column:file_name;not null"`
	FileSize    int64     `gorm:"column:file_size;not null"`
	FileMD5     string    `gorm:"column:file_md5;not null"`
	ContentType string    `gorm:"column:content_type;not null"`
	ChunkSize   int       `gorm:"column:chunk_size;not null"`
	TotalChunks int       `gorm:"column:total_chunks;not null"`
	Title       string    `gorm:"column:title;not null"`
	Description string    `gorm:"column:description;not null"`
	Status      string    `gorm:"column:status;not null"`
	VideoID     *uint64   `gorm:"column:video_id"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

func (Session) TableName() string {
	return "upload_sessions"
}

// Chunk 是一条"已收到某个分片"的记录。
type Chunk struct {
	UploadID  string    `gorm:"column:upload_id;primaryKey"`
	Index     int       `gorm:"column:chunk_index;primaryKey"`
	SizeBytes int64     `gorm:"column:size_bytes;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (Chunk) TableName() string {
	return "upload_chunks"
}
