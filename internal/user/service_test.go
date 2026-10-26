package user

import (
	"context"
	"errors"
	"go-feed-system/pkg/password"
	"go-feed-system/pkg/token"
	"golang.org/x/crypto/bcrypt"
	"testing"
	"time"
)

var errUserRepository = errors.New("user repository error")

// 1. 只 Mock Repository（因为它连数据库）
type fakeRepository struct {
	users        map[string]*User // 用 map 模拟数据库表
	findByIDsErr error
}

func (r *fakeRepository) Create(ctx context.Context, u *User) error {
	if _, ok := r.users[u.Email]; ok {
		return ErrConflict
	}
	u.ID = uint64(len(r.users) + 1)
	r.users[u.Email] = u
	return nil
}
func (r *fakeRepository) FindByEmail(ctx context.Context, email string) (*User, error) {
	if u, ok := r.users[email]; ok {
		return u, nil
	}
	return nil, ErrNotFound
}
func (r *fakeRepository) FindByID(ctx context.Context, id uint64) (*User, error) {
	for _, u := range r.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, ErrNotFound
}

func (r *fakeRepository) UpdatePassword(ctx context.Context, id uint64, passwordHash string) error {
	for _, u := range r.users {
		if u.ID == id {
			u.PasswordHash = passwordHash
			return nil
		}
	}
	return ErrNotFound
}

func (r *fakeRepository) UpdateAvatarKey(ctx context.Context, id uint64, avatarKey string) error {
	for _, u := range r.users {
		if u.ID == id {
			u.AvatarKey = avatarKey
			return nil
		}
	}
	return ErrNotFound
}

type fakeRefreshRepository struct {
	byHash map[string]*RefreshToken
	seq    uint64
}

func newFakeRefreshRepository() *fakeRefreshRepository {
	return &fakeRefreshRepository{byHash: map[string]*RefreshToken{}}
}

func (r *fakeRefreshRepository) Create(ctx context.Context, item *RefreshToken) error {
	r.seq++
	item.ID = r.seq
	copied := *item
	r.byHash[item.TokenHash] = &copied
	return nil
}

func (r *fakeRefreshRepository) FindByHash(ctx context.Context, hash string) (*RefreshToken, error) {
	if item, ok := r.byHash[hash]; ok {
		copied := *item
		return &copied, nil
	}
	return nil, ErrRefreshTokenNotFound
}

func (r *fakeRefreshRepository) Revoke(ctx context.Context, id uint64) error {
	for _, item := range r.byHash {
		if item.ID == id && item.RevokedAt == nil {
			now := time.Now()
			item.RevokedAt = &now
		}
	}
	return nil
}

func (r *fakeRefreshRepository) RevokeAllForUser(ctx context.Context, userID uint64) error {
	now := time.Now()
	for _, item := range r.byHash {
		if item.UserID == userID && item.RevokedAt == nil {
			item.RevokedAt = &now
		}
	}
	return nil
}

func (r *fakeRepository) FindByIDs(ctx context.Context, ids []uint64) ([]User, error) {
	if r.findByIDsErr != nil {
		return nil, r.findByIDsErr
	}

	idSet := make(map[uint64]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}

	result := make([]User, 0, len(ids))
	for _, u := range r.users {
		if idSet[u.ID] {
			result = append(result, *u)
		}
	}
	return result, nil
}

// 2. 编写测试
func TestService_Register(t *testing.T) {
	// 初始化真实的 Hasher 和 TokenManager（确保真实逻辑没问题）
	realHasher, err := password.NewBcryptHasher(bcrypt.MinCost)
	if err != nil {
		t.Fatalf("failed to create real hasher: %v", err)
	}
	realTokenManager, err := token.NewManager("test-secret-is-long-enough-for-the-test", "test-issuer", 2*time.Hour)
	if err != nil {
		t.Fatalf("failed to create real token manager: %v", err)
	}

	// 开始测试
	tests := []struct {
		name    string
		prepare func(repo *fakeRepository)
		rinput  RegisterInput
		wantErr error
	}{
		{
			name: "注册成功",
			prepare: func(repo *fakeRepository) {

			},
			rinput:  RegisterInput{Username: "u1", Email: "test@example.com", Password: "123"},
			wantErr: nil,
		},
		{
			name: "重复用户",
			prepare: func(repo *fakeRepository) {
				repo.users["test@example.com"] = &User{ID: 1, Username: "u1", Email: "test@example.com"}
			},
			rinput:  RegisterInput{Username: "u1", Email: "test@example.com", Password: "123"},
			wantErr: ErrConflict,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			// 初始化 Fake Repository
			fakeRepo := &fakeRepository{users: make(map[string]*User)}

			if tt.prepare != nil {
				tt.prepare(fakeRepo)
			}

			// 依赖注入
			svc := NewService(fakeRepo, realHasher, realTokenManager, newFakeRefreshRepository(), 0)

			user, err := svc.Register(context.Background(), tt.rinput)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}

			if tt.wantErr == nil && user.ID == 0 {
				t.Error("expected user ID to be backfilled")
			}
		})
	}
}

func TestService_Login(t *testing.T) {
	// 初始化真实的 Hasher 和 TokenManager（确保真实逻辑没问题）
	realHasher, err := password.NewBcryptHasher(bcrypt.MinCost)
	if err != nil {
		t.Fatalf("failed to create real hasher: %v", err)
	}
	realTokenManager, err := token.NewManager("test-secret-is-long-enough-for-the-test", "test-issuer", 2*time.Hour)
	if err != nil {
		t.Fatalf("failed to create real token manager: %v", err)
	}

	//提前准备正确密码
	validHash, err := realHasher.Hash("123")
	if err != nil {
		t.Fatalf("failed to hash password: %v", err)
	}

	// 开始测试
	tests := []struct {
		name    string
		prepare func(repo *fakeRepository)
		linput  LoginInput
		wantErr error
	}{
		{
			name: "登录成功",
			prepare: func(repo *fakeRepository) {
				// 预置一个带有真实哈希的用户
				repo.users["test@example.com"] = &User{ID: 1, Username: "u1", Email: "test@example.com", PasswordHash: validHash}
			},
			linput:  LoginInput{Email: "test@example.com", Password: "123"},
			wantErr: nil,
		},
		{
			name: "错误密码",
			prepare: func(repo *fakeRepository) {
				repo.users["test@example.com"] = &User{Username: "u1", Email: "test@example.com", PasswordHash: validHash}
			},
			linput:  LoginInput{Email: "test@example.com", Password: "321"},
			wantErr: ErrInvalidCredentials,
		},
		{
			name: "用户不存在",
			prepare: func(repo *fakeRepository) {
				repo.users["test@example.com"] = &User{Username: "u1", Email: "test@example.com", PasswordHash: validHash}
			},
			linput:  LoginInput{Email: "notExist@example.com", Password: "123"},
			wantErr: ErrInvalidCredentials,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 初始化 Fake Repository
			fakeRepo := &fakeRepository{users: make(map[string]*User)}

			if tt.prepare != nil {
				tt.prepare(fakeRepo)
			}

			// 依赖注入
			svc := NewService(fakeRepo, realHasher, realTokenManager, newFakeRefreshRepository(), 0)

			session, err := svc.Login(context.Background(), tt.linput)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}

			if tt.wantErr == nil && session.AccessToken.Value == "" {
				t.Error("expected access token to be non-empty")
			}
			if tt.wantErr == nil && session.RefreshToken == "" {
				t.Error("expected refresh token to be non-empty")
			}
		})
	}
}

func TestService_GetByIDs(t *testing.T) {
	tests := []struct {
		name        string
		ids         []uint64
		prepare     func(repo *fakeRepository)
		wantErr     error
		wantUserIDs []uint64
	}{
		{
			name: "空 ID 返回空 map",
			ids:  nil,
		},
		{
			name: "批量查询并忽略缺失 ID",
			ids:  []uint64{1, 3, 999},
			prepare: func(repo *fakeRepository) {
				repo.users["u1@example.com"] = &User{ID: 1, Username: "u1", Email: "u1@example.com"}
				repo.users["u2@example.com"] = &User{ID: 2, Username: "u2", Email: "u2@example.com"}
				repo.users["u3@example.com"] = &User{ID: 3, Username: "u3", Email: "u3@example.com"}
			},
			wantUserIDs: []uint64{1, 3},
		},
		{
			name: "Repository 错误被包装",
			ids:  []uint64{1},
			prepare: func(repo *fakeRepository) {
				repo.findByIDsErr = errUserRepository
			},
			wantErr: errUserRepository,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepository{users: make(map[string]*User)}
			if tt.prepare != nil {
				tt.prepare(repo)
			}

			svc := NewService(repo, nil, nil, newFakeRefreshRepository(), 0)
			users, err := svc.GetByIDs(context.Background(), tt.ids)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}
			if err != nil {
				return
			}

			if len(users) != len(tt.wantUserIDs) {
				t.Fatalf("expected %d users, got %d", len(tt.wantUserIDs), len(users))
			}
			for _, id := range tt.wantUserIDs {
				if users[id] == nil {
					t.Errorf("expected user %d in result", id)
				}
			}
		})
	}
}

func TestService_Refresh_RotatesTokens(t *testing.T) {
	hasher, err := password.NewBcryptHasher(bcrypt.MinCost)
	if err != nil {
		t.Fatalf("create hasher: %v", err)
	}
	manager, err := token.NewManager("test-secret-is-long-enough-for-the-test", "test-issuer", 2*time.Hour)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}
	hash, err := hasher.Hash("password123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	repo := &fakeRepository{users: map[string]*User{
		"a@example.com": {ID: 1, Email: "a@example.com", PasswordHash: hash},
	}}
	refreshRepo := newFakeRefreshRepository()
	svc := NewService(repo, hasher, manager, refreshRepo, 0)

	session, err := svc.Login(context.Background(), LoginInput{Email: "a@example.com", Password: "password123"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	rotated, err := svc.Refresh(context.Background(), session.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if rotated.RefreshToken == session.RefreshToken {
		t.Fatal("expected refresh token rotation")
	}
	if _, err := svc.Refresh(context.Background(), session.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("expected old refresh token to be revoked, got %v", err)
	}
}

func TestService_ChangePassword(t *testing.T) {
	hasher, err := password.NewBcryptHasher(bcrypt.MinCost)
	if err != nil {
		t.Fatalf("create hasher: %v", err)
	}
	manager, err := token.NewManager("test-secret-is-long-enough-for-the-test", "test-issuer", 2*time.Hour)
	if err != nil {
		t.Fatalf("create token manager: %v", err)
	}
	hash, err := hasher.Hash("password123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}

	account := &User{ID: 1, Email: "a@example.com", PasswordHash: hash}
	repo := &fakeRepository{users: map[string]*User{"a@example.com": account}}
	refreshRepo := newFakeRefreshRepository()
	svc := NewService(repo, hasher, manager, refreshRepo, 0)

	session, err := svc.Login(context.Background(), LoginInput{Email: "a@example.com", Password: "password123"})
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	if err := svc.ChangePassword(context.Background(), 1, "wrong-password", "newpassword123"); !errors.Is(err, ErrInvalidOldPassword) {
		t.Fatalf("expected ErrInvalidOldPassword, got %v", err)
	}
	if err := svc.ChangePassword(context.Background(), 1, "password123", "newpassword123"); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if err := hasher.Compare(account.PasswordHash, "newpassword123"); err != nil {
		t.Fatalf("new password was not stored: %v", err)
	}
	// 改密后旧 refresh token 应被吊销。
	if _, err := svc.Refresh(context.Background(), session.RefreshToken); !errors.Is(err, ErrInvalidRefreshToken) {
		t.Fatalf("expected refresh token revoked after password change, got %v", err)
	}
}
