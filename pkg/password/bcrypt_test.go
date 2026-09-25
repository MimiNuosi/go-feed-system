package password

import (
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestBcryptHasher(t *testing.T) {
	tests := []struct {
		name    string
		cost    int
		wantErr bool
	}{
		{name: "正常 cost", cost: bcrypt.DefaultCost, wantErr: false}, // DefaultCost 通常是 10
		{name: "cost 过小", cost: bcrypt.MinCost - 1, wantErr: true},
		{name: "cost 过大", cost: bcrypt.MaxCost + 1, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBcryptHasher(tt.cost)
			if (err != nil) != tt.wantErr {
				t.Errorf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestBcryptHasher_Hash(t *testing.T) {
	// 为了加快测试速度，使用最小 cost
	hasher, _ := NewBcryptHasher(bcrypt.MinCost)
	password := "my-secret-password"

	hash, err := hasher.Hash(password)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// 断言哈希不能为空
	if hash == "" {
		t.Error("expected hash to be non-empty")
	}

	// 断言哈希不能等于原密码
	if hash == password {
		t.Error("expected hash to be different from password")
	}
}

func TestBcryptHasher_Compare(t *testing.T) {
	hasher, _ := NewBcryptHasher(bcrypt.MinCost)
	password := "my-secret-password"

	// 先预计算一个真实的 hash 用于测试
	validHash, _ := hasher.Hash(password)

	tests := []struct {
		name     string
		hash     string // 你要测试的 hash 输入
		password string // 你要测试的明文输入
		wantErr  error  // 预期错误
	}{
		{name: "成功匹配", hash: validHash, password: password, wantErr: nil},
		{name: "密码错误", hash: validHash, password: "wrong-password", wantErr: ErrMismatch},
		{name: "空 hash", hash: "", password: password, wantErr: ErrInvalidHash},
		{name: "非法 hash 格式", hash: "not-a-valid-hash", password: password, wantErr: ErrInvalidHash},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := hasher.Compare(tt.hash, tt.password)

			if !errors.Is(err, tt.wantErr) {
				t.Errorf("expected err %v, got %v", tt.wantErr, err)
			}
		})
	}
}
