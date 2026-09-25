package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go-feed-system/pkg/password"
	"go-feed-system/pkg/token"
)

// PasswordHasher 是 Service 需要的最小密码能力。
//
// Go 接口采用隐式实现：pkg/password.BcryptHasher 不需要显式声明
// implements PasswordHasher，只要方法集合匹配即可。
type PasswordHasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) error
}

type TokenManager interface {
	Issue(userID uint64) (token.AccessToken, error)
}

type Service struct {
	users     Repository
	passwords PasswordHasher
	tokens    TokenManager
}

func NewService(users Repository, passwords PasswordHasher, tokens TokenManager) *Service {
	return &Service{
		users:     users,
		passwords: passwords,
		tokens:    tokens,
	}
}

func (s *Service) Register(ctx context.Context, input RegisterInput) (*User, error) {
	// 不要在这里先查询是否重复。并发安全最终由数据库唯一索引保证。
	username := strings.TrimSpace(input.Username)
	email := strings.ToLower(strings.TrimSpace(input.Email))
	password := string(input.Password)

	hashed, err := s.passwords.Hash(password)
	if err != nil {
		return nil, fmt.Errorf("register user: hash password: %w", err)
	}

	user := &User{Username: username, Email: email, PasswordHash: hashed}

	if err := s.users.Create(ctx, user); err != nil {
		if errors.Is(err, ErrConflict) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("register user: %w", err)
	}

	return user, nil
}

func (s *Service) Login(ctx context.Context, input LoginInput) (token.AccessToken, error) {
	// 不要把“邮箱不存在”和“密码错误”区分给客户端，避免账号枚举。
	email := strings.ToLower(strings.TrimSpace(input.Email))
	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return token.AccessToken{}, ErrInvalidCredentials // 隐藏用户不存在的事实
		}
		return token.AccessToken{}, fmt.Errorf("login user: find user: %w", err)
	}

	err = s.passwords.Compare(user.PasswordHash, input.Password)
	if err != nil {
		if errors.Is(err, password.ErrMismatch) { // 这里要引入 password 包的错误
			return token.AccessToken{}, ErrInvalidCredentials // 隐藏密码错误的事实
		}
		return token.AccessToken{}, fmt.Errorf("login user: compare password: %w", err)
	}

	accessToken, err := s.tokens.Issue(user.ID)
	if err != nil {
		return token.AccessToken{}, fmt.Errorf("login user: issue token: %w", err)
	}

	return accessToken, nil
}

func (s *Service) GetByID(ctx context.Context, id uint64) (*User, error) {
	user, err := s.users.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}

	return user, nil
}
