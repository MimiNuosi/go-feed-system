package password

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// Hasher 定义密码哈希能力。
//
// Service 只依赖这个行为，不直接依赖 bcrypt。以后更换成 Argon2id
// 时，业务代码不需要跟着修改。
type Hasher interface {
	Hash(password string) (string, error)
	Compare(hash, password string) error
}

type BcryptHasher struct {
	cost int
}

func NewBcryptHasher(cost int) (*BcryptHasher, error) {
	if cost < bcrypt.MinCost || cost > bcrypt.MaxCost {
		return nil, fmt.Errorf("bcrypt cost must be between %d and %d", bcrypt.MinCost, bcrypt.MaxCost)
	}

	return &BcryptHasher{
		cost: cost,
	}, nil
}

func (h *BcryptHasher) Hash(password string) (string, error) {
	hashBytes, err := bcrypt.GenerateFromPassword([]byte(password), h.cost)

	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	return string(hashBytes), nil
}

func (h *BcryptHasher) Compare(hash, password string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
			return ErrMismatch
		}
		return fmt.Errorf("compare password: %w", ErrInvalidHash)
	}

	return nil
}
