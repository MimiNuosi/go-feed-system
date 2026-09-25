package user

import "errors"

var (
	ErrNotFound           = errors.New("user not found")
	ErrConflict           = errors.New("user already exists")
	ErrInvalidCredentials = errors.New("invalid email or password")
)
