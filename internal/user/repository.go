package user

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"
)

var (
	ErrNotFound = errors.New("user not found")
	ErrConflict = errors.New("user already exists")
)

// Repository 定义用户领域需要的数据库能力。
//
// Handler 和 Service 只依赖这个接口，不直接依赖 GORM。
// 这相当于 C++ 中让业务代码依赖 MysqlDAO 抽象，
// 而不是到处直接调用 MysqlManager 单例。
type Repository interface {
	Create(ctx context.Context, user *User) error
	FindByEmail(ctx context.Context, email string) (*User, error)
	FindByID(ctx context.Context, id uint64) (*User, error)
}

type GORMRepository struct {
	db *gorm.DB
}

func NewGORMRepository(db *gorm.DB) *GORMRepository {
	return &GORMRepository{
		db: db,
	}
}

func (r *GORMRepository) Create(ctx context.Context, user *User) error {
	// 使用 GORM 插入用户。
	if err := r.db.WithContext(ctx).Create(user).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return ErrConflict
		}
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

func (r *GORMRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	// 根据 email 查询单个用户。
	var user User

	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return &user, nil
}

func (r *GORMRepository) FindByID(ctx context.Context, id uint64) (*User, error) {
	// 根据主键 id 查询单个用户。

	var user User

	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return &user, nil
}
