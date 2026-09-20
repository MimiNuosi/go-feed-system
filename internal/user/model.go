package user

import "time"

// User 对应 MySQL 中的 users 表。
//
// GORM 会使用字段名推断列名，但这里仍显式写出 column，
// 避免以后改 Go 字段名时意外改变数据库列名。
type User struct {
	ID           uint64    `gorm:"column:id;primaryKey;autoIncrement"`
	Username     string    `gorm:"column:username;size:32;not null"`
	Email        string    `gorm:"column:email;size:254;not null"`
	PasswordHash string    `gorm:"column:password_hash;size:255;not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (User) TableName() string {
	return "users"
}
