package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

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
	users      Repository
	passwords  PasswordHasher
	tokens     TokenManager
	refresh    RefreshTokenRepository
	refreshTTL time.Duration
}

const DefaultRefreshTTL = 30 * 24 * time.Hour

func NewService(
	users Repository,
	passwords PasswordHasher,
	tokens TokenManager,
	refresh RefreshTokenRepository,
	refreshTTL time.Duration,
) *Service {
	if refreshTTL <= 0 {
		refreshTTL = DefaultRefreshTTL
	}
	return &Service{
		users:      users,
		passwords:  passwords,
		tokens:     tokens,
		refresh:    refresh,
		refreshTTL: refreshTTL,
	}
}

// Session 是登录/刷新成功后的令牌组合。
type Session struct {
	AccessToken      token.AccessToken
	RefreshToken     string
	RefreshExpiresAt time.Time
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

func (s *Service) Login(ctx context.Context, input LoginInput) (Session, error) {
	// 不要把“邮箱不存在”和“密码错误”区分给客户端，避免账号枚举。
	email := strings.ToLower(strings.TrimSpace(input.Email))
	user, err := s.users.FindByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Session{}, ErrInvalidCredentials // 隐藏用户不存在的事实
		}
		return Session{}, fmt.Errorf("login user: find user: %w", err)
	}

	err = s.passwords.Compare(user.PasswordHash, input.Password)
	if err != nil {
		if errors.Is(err, password.ErrMismatch) { // 这里要引入 password 包的错误
			return Session{}, ErrInvalidCredentials // 隐藏密码错误的事实
		}
		return Session{}, fmt.Errorf("login user: compare password: %w", err)
	}

	session, err := s.issueSession(ctx, user.ID)
	if err != nil {
		return Session{}, fmt.Errorf("login user: issue token: %w", err)
	}

	return session, nil
}

// Refresh 校验刷新令牌并轮换（旧的立即吊销）。
func (s *Service) Refresh(ctx context.Context, raw string) (Session, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Session{}, ErrInvalidRefreshToken
	}

	record, err := s.refresh.FindByHash(ctx, hashRefreshToken(raw))
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			return Session{}, ErrInvalidRefreshToken
		}
		return Session{}, fmt.Errorf("refresh: find token: %w", err)
	}
	if record.RevokedAt != nil || time.Now().After(record.ExpiresAt) {
		return Session{}, ErrInvalidRefreshToken
	}

	if err := s.refresh.Revoke(ctx, record.ID); err != nil {
		return Session{}, fmt.Errorf("refresh: revoke old token: %w", err)
	}
	session, err := s.issueSession(ctx, record.UserID)
	if err != nil {
		return Session{}, fmt.Errorf("refresh: issue new session: %w", err)
	}
	return session, nil
}

// Logout 吊销刷新令牌；令牌不存在也按成功处理，保证幂等。
func (s *Service) Logout(ctx context.Context, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	record, err := s.refresh.FindByHash(ctx, hashRefreshToken(raw))
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			return nil
		}
		return fmt.Errorf("logout: find token: %w", err)
	}
	return s.refresh.Revoke(ctx, record.ID)
}

// ChangePassword 校验旧密码、更新哈希，并吊销该用户所有刷新令牌。
func (s *Service) ChangePassword(ctx context.Context, userID uint64, oldPassword, newPassword string) error {
	if userID == 0 {
		return ErrInvalidCredentials
	}
	if utf8.RuneCountInString(newPassword) < 8 || len(newPassword) > 72 {
		return ErrInvalidCredentials
	}

	account, err := s.users.FindByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("change password: find user: %w", err)
	}
	if err := s.passwords.Compare(account.PasswordHash, oldPassword); err != nil {
		if errors.Is(err, password.ErrMismatch) {
			return ErrInvalidOldPassword
		}
		return fmt.Errorf("change password: compare old password: %w", err)
	}

	hashed, err := s.passwords.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("change password: hash: %w", err)
	}
	if err := s.users.UpdatePassword(ctx, userID, hashed); err != nil {
		return fmt.Errorf("change password: update: %w", err)
	}
	if err := s.refresh.RevokeAllForUser(ctx, userID); err != nil {
		return fmt.Errorf("change password: revoke tokens: %w", err)
	}
	return nil
}

func (s *Service) issueSession(ctx context.Context, userID uint64) (Session, error) {
	access, err := s.tokens.Issue(userID)
	if err != nil {
		return Session{}, err
	}

	raw, hash, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	record := &RefreshToken{
		UserID:    userID,
		TokenHash: hash,
		ExpiresAt: now.Add(s.refreshTTL),
		CreatedAt: now,
	}
	if err := s.refresh.Create(ctx, record); err != nil {
		return Session{}, err
	}
	return Session{AccessToken: access, RefreshToken: raw, RefreshExpiresAt: record.ExpiresAt}, nil
}

func newRefreshToken() (string, string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate refresh token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(buf)
	return raw, hashRefreshToken(raw), nil
}

func hashRefreshToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Service) GetByID(ctx context.Context, id uint64) (*User, error) {
	user, err := s.users.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("get user by id: %w", err)
	}

	return user, nil
}

func (s *Service) GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*User, error) {
	if len(ids) == 0 {
		return make(map[uint64]*User), nil
	}

	users, err := s.users.FindByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("get users by ids: %w", err)
	}

	result := make(map[uint64]*User, len(users))
	for i := range users {
		result[users[i].ID] = &users[i]
	}
	return result, nil
}
