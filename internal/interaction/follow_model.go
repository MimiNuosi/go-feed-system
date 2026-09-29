package interaction

import "time"

// Follow 表示一条单向关注关系：FollowerID 关注 FolloweeID。
//
// 与 C++ 好友关系不同，不需要同时保存反向记录。互相关注可以通过查询
// 反向关系判断。
type Follow struct {
	ID         uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	FollowerID uint64    `gorm:"column:follower_id;not null"`
	FolloweeID uint64    `gorm:"column:followee_id;not null"`
	CreatedAt  time.Time `gorm:"column:created_at;not null"`
}

func (Follow) TableName() string {
	return "follows"
}
