package interaction

import (
	"context"
	"errors"
	"os"
	"testing"

	"gorm.io/gorm"

	"go-feed-system/internal/user"
	"go-feed-system/pkg/database"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}

	db, err := database.Open(context.Background(), database.MySQLConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin transaction: %v", tx.Error)
	}

	t.Cleanup(func() {
		tx.Rollback()
		database.Close(db)
	})

	return tx
}

func TestGORMFollowRepository(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, repo *GORMFollowRepository) (uint64, uint64) // 返回 u1.ID, u2.ID
		action  func(ctx context.Context, repo *GORMFollowRepository, u1ID, u2ID uint64) error
		wantErr error
	}{
		{
			name: "成功关注",
			prepare: func(t *testing.T, repo *GORMFollowRepository) (uint64, uint64) {
				u1, u2 := createSeedUsers(t, repo) // 封装的辅助函数
				return u1, u2
			},
			action: func(ctx context.Context, repo *GORMFollowRepository, u1ID, u2ID uint64) error {
				return repo.Create(ctx, u1ID, u2ID)
			},
			wantErr: nil,
		},
		{
			name: "重复关注，返回 ErrAlreadyFollowing",
			prepare: func(t *testing.T, repo *GORMFollowRepository) (uint64, uint64) {
				u1, u2 := createSeedUsers(t, repo)
				// 提前创建关注关系
				repo.Create(context.Background(), u1, u2)
				return u1, u2
			},
			action: func(ctx context.Context, repo *GORMFollowRepository, u1ID, u2ID uint64) error {
				return repo.Create(ctx, u1ID, u2ID)
			},
			wantErr: ErrAlreadyFollowing,
		},
		// ... 其他用例同理，都调用 createSeedUsers
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ✅ 核心修复：每个用例独立开启事务
			tx := openTestDB(t)
			repo := NewGORMFollowRepository(tx)

			// 准备种子数据，返回真实的用户 ID
			u1ID, u2ID := tt.prepare(t, repo)

			// 执行
			err := tt.action(context.Background(), repo, u1ID, u2ID)

			// 断言
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}
		})
	}
}

// 辅助函数：创建种子用户并检查错误
func createSeedUsers(t *testing.T, repo *GORMFollowRepository) (uint64, uint64) {
	u1 := &user.User{Username: "u1", Email: "u1@example.com", PasswordHash: "hash"}
	u2 := &user.User{Username: "u2", Email: "u2@example.com", PasswordHash: "hash"}

	if err := repo.db.Create(u1).Error; err != nil { // 注意：需暴露 repo.db 或使用 repo.Create（这里我们用 raw tx 会更好）
		t.Fatalf("failed to create u1: %v", err)
	}
	if err := repo.db.Create(u2).Error; err != nil {
		t.Fatalf("failed to create u2: %v", err)
	}
	return u1.ID, u2.ID
}
