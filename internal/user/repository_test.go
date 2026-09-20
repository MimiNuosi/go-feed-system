package user

import (
	"context"
	"errors"
	"os"
	"testing"

	"gorm.io/gorm"

	"go-feed-system/pkg/database"
)

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}

	db, err := database.Open(context.Background(), database.MySQLConfig{
		DSN: dsn,
	})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin transaction: %v", tx.Error)
	}

	t.Cleanup(func() {
		if err := tx.Rollback().Error; err != nil {
			t.Errorf("rollback transaction: %v", err)
		}
		if err := database.Close(db); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})

	return tx
}

func TestGORMRepositoryCreate(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, repo *GORMRepository)  // 预置数据或操作
		action  func(repo *GORMRepository, u *User) error // 执行的操作
		user    *User
		wantErr error
		checkFn func(*testing.T, *User) // 针对成功的断言
	}{
		{
			name:    "成功创建用户",
			prepare: nil,
			action: func(repo *GORMRepository, u *User) error {
				return repo.Create(context.Background(), u)
			},
			user:    &User{Username: "u1", Email: "test@example.com"},
			wantErr: nil,
			checkFn: func(t *testing.T, u *User) {
				if u.ID == 0 {
					t.Error("expected id backfilled")
				}
			},
		},
		{
			name: "重复邮箱冲突",
			prepare: func(t *testing.T, repo *GORMRepository) {
				if err := repo.Create(context.Background(), &User{Email: "test@example.com", Username: "u1"}); err != nil {
					t.Fatalf("prepare seed user failed: %v", err)
				}
			},
			action: func(repo *GORMRepository, u *User) error {
				return repo.Create(context.Background(), u)
			},
			user:    &User{Username: "u2", Email: "test@example.com"},
			wantErr: ErrConflict,
		},
		{
			name: "重复用户名冲突",
			prepare: func(t *testing.T, repo *GORMRepository) {
				if err := repo.Create(context.Background(), &User{Email: "test@example.com", Username: "u1"}); err != nil {
					t.Fatalf("prepare seed user failed: %v", err)
				}
			},
			action: func(repo *GORMRepository, u *User) error {
				return repo.Create(context.Background(), u)
			},
			user:    &User{Username: "u1", Email: "test2@example.com"},
			wantErr: ErrConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 获取测试库连接
			// 开启事务用于测试隔离
			tx := openTestDB(t)

			repo := NewGORMRepository(tx) // 用事务作为 Repository 的 db

			// 准备用户数据
			if tt.prepare != nil {
				tt.prepare(t, repo)
			}

			// 执行
			err := tt.action(repo, tt.user)

			// 断言错误 (牢记使用 errors.Is)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}
			// 断言成功后的状态
			if tt.checkFn != nil {
				tt.checkFn(t, tt.user)
			}
		})
	}
}

func TestGORMRepositoryFindByEmail(t *testing.T) {
	tests := []struct {
		name    string
		email   func(seed *User) string
		wantErr error
	}{
		{name: "found", email: func(seed *User) string { return seed.Email }, wantErr: nil},
		{name: "not found", email: func(*User) string { return "nonexistent@example.com" }, wantErr: ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 获取测试库连接
			// 开启事务用于测试隔离
			tx := openTestDB(t)

			repo := NewGORMRepository(tx) // 用事务作为 Repository 的 db

			seed := &User{Username: "seed_user", Email: "seed@example.com"}
			// 准备用户数据
			// 执行
			if err := repo.Create(context.Background(), seed); err != nil {
				t.Fatalf("failed to create seed user: %v", err)
			}

			found, err := repo.FindByEmail(context.Background(), tt.email(seed))

			// 断言错误 (牢记使用 errors.Is)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}

			if tt.wantErr == nil {
				if found.ID != seed.ID {
					t.Errorf("expected ID %d, got %d", seed.ID, found.ID)
				}
				if found.Username != seed.Username {
					t.Errorf("expected Username %s, got %s", seed.Username, found.Username)
				}
				if found.Email != seed.Email {
					t.Errorf("expected Email %s, got %s", seed.Email, found.Email)
				}
			}
		})
	}
}

func TestGORMRepositoryFindByID(t *testing.T) {
	tests := []struct {
		name    string
		id      func(seed *User) uint64
		wantErr error
	}{
		{name: "found", id: func(seed *User) uint64 { return seed.ID }, wantErr: nil},
		{name: "not found", id: func(*User) uint64 { return 999999 }, wantErr: ErrNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 获取测试库连接
			// 开启事务用于测试隔离
			tx := openTestDB(t)

			repo := NewGORMRepository(tx) // 用事务作为 Repository 的 db

			seed := &User{Username: "seed_user", Email: "seed@example.com"}
			// 准备用户数据
			// 执行
			if err := repo.Create(context.Background(), seed); err != nil {
				t.Fatalf("failed to create seed user: %v", err)
			}

			queryID := tt.id(seed)
			found, err := repo.FindByID(context.Background(), queryID)

			// 断言错误 (牢记使用 errors.Is)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}

			if tt.wantErr == nil {
				if found.ID != seed.ID {
					t.Errorf("expected ID %d, got %d", seed.ID, found.ID)
				}
				if found.Username != seed.Username {
					t.Errorf("expected Username %s, got %s", seed.Username, found.Username)
				}
				if found.Email != seed.Email {
					t.Errorf("expected Email %s, got %s", seed.Email, found.Email)
				}
			}
		})
	}
}
