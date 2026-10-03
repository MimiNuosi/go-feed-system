package interaction

import (
	"context"
	"errors"
	"testing"

	"go-feed-system/internal/user"
)

// 1. Mock UserReader
type fakeUserReader struct {
	GetByIDFunc func(ctx context.Context, id uint64) (*user.User, error)
}

func (f *fakeUserReader) GetByID(ctx context.Context, id uint64) (*user.User, error) {
	if f.GetByIDFunc != nil {
		return f.GetByIDFunc(ctx, id)
	}
	return &user.User{ID: id, Username: "test-user"}, nil
}

// 2. Mock FollowRepository
type fakeFollowRepository struct {
	CreateFunc      func(ctx context.Context, followerID, followeeID uint64) error
	DeleteFunc      func(ctx context.Context, followerID, followeeID uint64) error
	ExistsFunc      func(ctx context.Context, followerID, followeeID uint64) (bool, error)
	CreateCallCount int
	DeleteCallCount int
}

func (f *fakeFollowRepository) Create(ctx context.Context, followerID, followeeID uint64) error {
	f.CreateCallCount++
	if f.CreateFunc != nil {
		return f.CreateFunc(ctx, followerID, followeeID)
	}
	return nil
}

func (f *fakeFollowRepository) Delete(ctx context.Context, followerID, followeeID uint64) error {
	f.DeleteCallCount++
	if f.DeleteFunc != nil {
		return f.DeleteFunc(ctx, followerID, followeeID)
	}
	return nil
}

func (f *fakeFollowRepository) Exists(ctx context.Context, followerID, followeeID uint64) (bool, error) {
	if f.ExistsFunc != nil {
		return f.ExistsFunc(ctx, followerID, followeeID)
	}
	return false, nil
}

// 实现剩余接口方法，保证编译通过（测试中未使用）
func (f *fakeFollowRepository) CountFollowers(ctx context.Context, userID uint64) (int64, error) {
	return 0, nil
}
func (f *fakeFollowRepository) CountFollowing(ctx context.Context, userID uint64) (int64, error) {
	return 0, nil
}
func (f *fakeFollowRepository) ListFollowerIDs(
	ctx context.Context,
	followeeID uint64,
	afterID uint64,
	limit int,
) ([]uint64, error) {
	// 如果你的 Service 测试不需要用到粉丝列表，直接返回空切片即可
	return []uint64{}, nil
}

// 3. 表驱动测试
func TestFollowService_Follow(t *testing.T) {
	tests := []struct {
		name            string
		followerID      uint64
		followeeID      uint64
		mockUserErr     error
		mockCreateErr   error
		wantErr         error
		wantCreateCalls int
	}{
		{
			name:            "成功关注",
			followerID:      1,
			followeeID:      2,
			mockUserErr:     nil,
			mockCreateErr:   nil,
			wantErr:         nil,
			wantCreateCalls: 1,
		},
		{
			name:            "关注自己，返回 ErrInvalidInput",
			followerID:      1,
			followeeID:      1,
			wantErr:         ErrInvalidInput,
			wantCreateCalls: 0,
		},
		{
			name:            "ID 为 0，返回 ErrInvalidInput",
			followerID:      0,
			followeeID:      2,
			wantErr:         ErrInvalidInput,
			wantCreateCalls: 0,
		},
		{
			name:            "目标用户不存在，返回 ErrInvalidTarget",
			followerID:      1,
			followeeID:      999,
			mockUserErr:     user.ErrNotFound,
			wantErr:         ErrInvalidTarget,
			wantCreateCalls: 0,
		},
		{
			name:            "重复关注（幂等），返回 nil",
			followerID:      1,
			followeeID:      2,
			mockUserErr:     nil,
			mockCreateErr:   ErrAlreadyFollowing, // 模拟底层报重复
			wantErr:         nil,                 // Service 必须把它转换成 nil
			wantCreateCalls: 1,
		},
		{
			name:            "底层数据库报错",
			followerID:      1,
			followeeID:      2,
			mockUserErr:     nil,
			mockCreateErr:   errors.New("db error"),
			wantErr:         errors.New("db error"), // 必须包装并返回
			wantCreateCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 Fake 依赖
			userReader := &fakeUserReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*user.User, error) {
					return nil, tt.mockUserErr
				},
			}
			repo := &fakeFollowRepository{
				CreateFunc: func(ctx context.Context, followerID, followeeID uint64) error {
					return tt.mockCreateErr
				},
			}

			svc := NewFollowService(repo, userReader)

			// 执行
			err := svc.Follow(context.Background(), tt.followerID, tt.followeeID)

			// 断言错误
			if tt.wantErr != nil {
				// 如果是我们主动构造的普通 error，用字符串比较，否则用 errors.Is
				if tt.wantErr.Error() == "db error" {
					if err == nil {
						t.Fatal("expected error, got nil")
					}
				} else if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			// 断言调用次数（验证业务分支是否正确拦截）
			if repo.CreateCallCount != tt.wantCreateCalls {
				t.Errorf("expected Create called %d times, got %d", tt.wantCreateCalls, repo.CreateCallCount)
			}
		})
	}
}

func TestFollowService_Unfollow(t *testing.T) {
	tests := []struct {
		name            string
		followerID      uint64
		followeeID      uint64
		mockDeleteErr   error
		wantErr         error
		wantDeleteCalls int
	}{
		{
			name:            "取消关注成功",
			followerID:      1,
			followeeID:      2,
			wantErr:         nil,
			wantDeleteCalls: 1,
		},
		{
			name:            "取消未关注的人（幂等）",
			followerID:      1,
			followeeID:      2,
			mockDeleteErr:   nil, // Repository 层已保证不存在也返回 nil
			wantErr:         nil,
			wantDeleteCalls: 1,
		},
		{
			name:            "取消关注自己，返回 ErrInvalidInput",
			followerID:      1,
			followeeID:      1,
			wantErr:         ErrInvalidInput,
			wantDeleteCalls: 0,
		},
		{
			name:            "底层数据库报错",
			followerID:      1,
			followeeID:      2,
			mockDeleteErr:   errors.New("db error"),
			wantErr:         errors.New("db error"),
			wantDeleteCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeFollowRepository{
				DeleteFunc: func(ctx context.Context, followerID, followeeID uint64) error {
					return tt.mockDeleteErr
				},
			}
			svc := NewFollowService(repo, &fakeUserReader{})

			err := svc.Unfollow(context.Background(), tt.followerID, tt.followeeID)

			if tt.wantErr != nil {
				if tt.wantErr.Error() == "db error" {
					if err == nil {
						t.Fatal("expected error, got nil")
					}
				} else if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			if repo.DeleteCallCount != tt.wantDeleteCalls {
				t.Errorf("expected Delete called %d times, got %d", tt.wantDeleteCalls, repo.DeleteCallCount)
			}
		})
	}
}
