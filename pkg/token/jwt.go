package token

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	jwt.RegisteredClaims
}

type AccessToken struct {
	Value     string
	ExpiresAt time.Time
}

type Manager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewManager(secret, issuer string, ttl time.Duration) (*Manager, error) {
	if len(secret) == 0 {
		return nil, fmt.Errorf("jwt secret must not be empty")
	}
	if len(secret) < 32 {
		return nil, fmt.Errorf("jwt secret must be at least 32 bytes")
	}
	if issuer == "" {
		return nil, fmt.Errorf("jwt issuer must not be empty")
	}
	if ttl <= 0 {
		return nil, fmt.Errorf("jwt ttl must be positive")
	}

	return &Manager{
		secret: []byte(secret),
		issuer: issuer,
		ttl:    ttl,
	}, nil
}

func (m *Manager) Issue(userID uint64) (AccessToken, error) {
	// 1. 生成唯一 jti（防止重放攻击，后续 Redis 黑名单要用到）
	jtiUUID, err := uuid.NewRandom()
	if err != nil {
		return AccessToken{}, fmt.Errorf("jti err: %w", err)
	}
	jti := jtiUUID.String()

	now := time.Now()
	expiresAt := now.Add(m.ttl)

	// 2. 组装 Claims
	claims := Claims{
		Subject:   strconv.FormatUint(userID, 10), // JWT 标准里 sub 必须是字符串
		Issuer:    m.issuer,
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expiresAt),
		ID:        jti,
	}

	// 3. 创建 Token (指定 HS256 算法)
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// 4. 使用 secret 签名，拿到最终的 JWT 字符串
	signedStr, err := token.SignedString(m.secret)
	if err != nil {
		return AccessToken{}, fmt.Errorf("sign token: %w", err)
	}

	// 5. 返回结果
	return AccessToken{
		Value:     signedStr,
		ExpiresAt: expiresAt,
	}, nil

}

func (m *Manager) Parse(raw string) (uint64, error) {
	// 1. 解析 Token，并强制校验算法为 HS256
	// 为什么必须限制算法？如果不限制，黑客可以把算法改成 "none"，伪造任何身份！
	token, err := jwt.ParseWithClaims(raw, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		return m.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(m.issuer))

	if err != nil {
		return 0, classifyJWTError(err)
	}

	// 2. 类型断言，拿到我们定义的 Claims
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return 0, ErrInvalidToken
	}

	// 3. 校验过期时间 (虽然 jwt 库通常会自动校验，但手动做一次更稳妥)
	if claims.ExpiresAt == nil || claims.ExpiresAt.Time.Before(time.Now()) {
		return 0, ErrTokenExpired
	}

	// 4. 将字符串 sub 转换回 uint64
	userID, err := strconv.ParseUint(claims.Subject, 10, 64)
	if err != nil || userID == 0 {
		return 0, ErrInvalidSubject
	}

	return userID, nil
}

func classifyJWTError(err error) error {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return ErrTokenExpired
	case errors.Is(err, jwt.ErrTokenInvalidIssuer),
		errors.Is(err, jwt.ErrTokenRequiredClaimMissing):
		return ErrInvalidIssuer
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return ErrInvalidSignature
	default:
		return ErrInvalidToken
	}
}
