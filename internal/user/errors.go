package user

import "errors"

var (
	ErrNotFound            = errors.New("user not found")
	ErrConflict            = errors.New("user already exists")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrInvalidRefreshToken = errors.New("invalid refresh token")
	ErrInvalidOldPassword  = errors.New("old password is incorrect")
)
