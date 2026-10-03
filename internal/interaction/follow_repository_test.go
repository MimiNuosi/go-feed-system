package interaction

import (
	"context"
	"errors"
	"fmt"
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

// 在 TestGORMFollowRepository 下方新增一个独立测试函数
func TestGORMFollowRepository_ListFollowerIDs(t *testing.T) {
	t.Run("没有粉丝时返回空切片", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMFollowRepository(tx)

		// 创建作者和粉丝
		author := &user.User{Username: "author", Email: "author@example.com", PasswordHash: "hash"}
		if err := tx.Create(author).Error; err != nil {
			t.Fatalf("create author: %v", err)
		}

		ids, err := repo.ListFollowerIDs(context.Background(), author.ID, 0, 10)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("expected empty, got %v", ids)
		}
	})

	t.Run("多个粉丝按 ID 升序返回并支持游标分页", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMFollowRepository(tx)

		author := &user.User{Username: "author", Email: "author@example.com", PasswordHash: "hash"}
		if err := tx.Create(author).Error; err != nil {
			t.Fatalf("create author: %v", err)
		}

		// 创建 5 个粉丝
		var fans []*user.User
		for i := 1; i <= 5; i++ {
			f := &user.User{
				Username:     fmt.Sprintf("fan%d", i),
				Email:        fmt.Sprintf("fan%d@example.com", i),
				PasswordHash: "hash",
			}
			if err := tx.Create(f).Error; err != nil {
				t.Fatalf("create fan %d: %v", i, err)
			}
			fans = append(fans, f)
			// 让每个粉丝都关注作者
			if err := tx.Create(&Follow{FollowerID: f.ID, FolloweeID: author.ID}).Error; err != nil {
				t.Fatalf("create follow for fan %d: %v", i, err)
			}
		}

		// 第一页：取 2 条
		page1, err := repo.ListFollowerIDs(context.Background(), author.ID, 0, 2)
		if err != nil {
			t.Fatalf("page1 failed: %v", err)
		}
		if len(page1) != 2 || page1[0] != fans[0].ID || page1[1] != fans[1].ID {
			t.Fatalf("page1 expected [%d %d], got %v", fans[0].ID, fans[1].ID, page1)
		}

		// 第二页：游标是 page1 最后一条
		page2, err := repo.ListFollowerIDs(context.Background(), author.ID, page1[len(page1)-1], 2)
		if err != nil {
			t.Fatalf("page2 failed: %v", err)
		}
		if len(page2) != 2 || page2[0] != fans[2].ID || page2[1] != fans[3].ID {
			t.Fatalf("page2 expected [%d %d], got %v", fans[2].ID, fans[3].ID, page2)
		}

		// 第三页：只剩 1 条
		page3, err := repo.ListFollowerIDs(context.Background(), author.ID, page2[len(page2)-1], 2)
		if err != nil {
			t.Fatalf("page3 failed: %v", err)
		}
		if len(page3) != 1 || page3[0] != fans[4].ID {
			t.Fatalf("page3 expected [%d], got %v", fans[4].ID, page3)
		}
	})
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
