package token

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// 1. 测试签发和解析的“正常流程”
func TestManager_Issue(t *testing.T) {
	secret := "this-is-a-32-byte-test-secret-key-123"
	issuer := "test-issuer"
	ttl := 2 * time.Hour
	manager, err := NewManager(secret, issuer, ttl)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}

	userID := uint64(12345)

	//调用 manager.Issue(userID)
	token, err := manager.Issue(userID)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	//断言 token.Value 非空
	if token.Value == "" {
		t.Error("expected token value to be non-empty")
	}

	// 断言 token.ExpiresAt 在未来（大于当前时间）
	if token.ExpiresAt.Before(time.Now()) {
		t.Error("expected token to be valid in the future")
	}

}

// 2. 测试解析的“各种异常与正常情况”
func TestManager_Parse(t *testing.T) {
	secret := "this-is-a-32-byte-test-secret-key-123"
	issuer := "test-issuer"
	ttl := 2 * time.Hour
	manager, err := NewManager(secret, issuer, ttl)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	userID := uint64(12345)

	// 生成一个合法的 Token 用于基础测试
	validToken, err := manager.Issue(userID)
	if err != nil {
		t.Fatalf("failed to issue valid token: %v", err)
	}

	// 生成一个错误密钥的 Manager 用于测试篡改
	otherManager, err := NewManager("wrong-secret-that-is-at-least-32-bytes-long", issuer, ttl)
	if err != nil {
		t.Fatalf("failed to create other manager: %v", err)
	}

	wrongSecretToken, err := otherManager.Issue(userID)
	if err != nil {
		t.Fatalf("failed to issue wrongSecretToken: %v", err)
	}

	// 生成一个 Issuer 不匹配的 Token
	wrongIssuerManager, err := NewManager(secret, "wrong-issuer", ttl)
	if err != nil {
		t.Fatalf("failed to create wrong issuer manager: %v", err)
	}
	wrongIssuerToken, err := wrongIssuerManager.Issue(userID)
	if err != nil {
		t.Fatalf("failed to issue wrong issuer token: %v", err)
	}

	// 生成一个缺少 sub 的 Token
	noSubClaims := Claims{
		Issuer:    issuer,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	noSubToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, noSubClaims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to issue noSubToken : %v", err)
	}

	// 生成一个 alg=none 的 Token（经典攻击手段）
	// 注意：必须显式传入 jwt.UnsafeAllowNoneSignatureType，且不能有签名值
	noneClaims := Claims{
		Subject:   strconv.FormatUint(userID, 10),
		Issuer:    issuer,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	noneToken, err := jwt.NewWithClaims(jwt.SigningMethodNone, noneClaims).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("failed to issue noneToken : %v", err)
	}

	// 生成一个 sub = "0" 的 Token
	zeroSubClaims := Claims{
		Subject:   "0",
		Issuer:    issuer,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	zeroSubToken, err := jwt.NewWithClaims(jwt.SigningMethodHS256, zeroSubClaims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("failed to issue zeroSubToken : %v", err)
	}

	// 生成一个过期 Token
	expiredManager := &Manager{
		secret: []byte(secret),
		issuer: issuer,
		ttl:    -1 * time.Hour,
	}
	expiredToken, err := expiredManager.Issue(userID)
	if err != nil {
		t.Fatalf("failed to issue expired token: %v", err)
	}

	tests := []struct {
		name       string
		raw        string // 你要测试的 Token 输入
		wantUserID uint64 // 期望解析出的用户 ID（只有成功时才有意义）
		wantErr    error  // 预期错误
	}{
		{name: "正常解析", raw: validToken.Value, wantUserID: userID, wantErr: nil},
		{name: "签名错误", raw: wrongSecretToken.Value, wantErr: ErrInvalidSignature},
		{name: "Token 过期", raw: expiredToken.Value, wantErr: ErrTokenExpired},
		{name: "篡改 Token", raw: validToken.Value[:len(validToken.Value)-5] + "abcde", wantErr: ErrInvalidToken},
		{name: "空 Token", raw: "", wantErr: ErrInvalidToken},
		{name: "Issuer 不匹配", raw: wrongIssuerToken.Value, wantErr: ErrInvalidIssuer},
		{name: "缺少 sub", raw: noSubToken, wantErr: ErrInvalidSubject},
		{name: "alg=none 攻击", raw: noneToken, wantErr: ErrInvalidToken},
		{name: "sub 为 0", raw: zeroSubToken, wantErr: ErrInvalidSubject},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, err := manager.Parse(tt.raw)

			// 1. 断言错误
			if err != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}

				if !errors.Is(err, ErrInvalidToken) {
					t.Errorf("expected error to wrap ErrInvalidToken, got %v", err)
				}
			}

			// 2. 如果期望没有错误，必须校验返回的用户 ID！
			if tt.wantErr == nil {
				if gotID != tt.wantUserID {
					t.Errorf("expected userID %d, got %d", tt.wantUserID, gotID)
				}
			}
		})
	}
}

func TestNewManager_ShortSecret(t *testing.T) {
	// 这里的 secret 只有 11 个字符，小于 32
	_, err := NewManager("short-secret", "test-issuer", 2*time.Hour)
	if err == nil {
		t.Error("expected error for short secret, got nil")
	}

	// 也可以顺便测一下空 issuer 和负 ttl
	_, err = NewManager("this-is-a-32-byte-test-secret-key-123", "", 2*time.Hour)
	if err == nil {
		t.Error("expected error for empty issuer, got nil")
	}
}
