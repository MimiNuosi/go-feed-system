package interaction

import "time"

// Like 表示“某个用户点赞了某个视频”。
//
// 与关注关系一样，点赞也是单向、幂等的关系：
//
//	userID -> videoID
//
// 一个用户对同一个视频只应该保留一条记录，数据库唯一索引是最终保证。
type Like struct {
	ID        uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	UserID    uint64    `gorm:"column:user_id;not null"`
	VideoID   uint64    `gorm:"column:video_id;not null"`
	CreatedAt time.Time `gorm:"column:created_at;not null"`
}

func (Like) TableName() string {
	return "likes"
}
