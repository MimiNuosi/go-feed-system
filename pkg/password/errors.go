package password

import "errors"

var (
	ErrMismatch    = errors.New("password does not match")
	ErrInvalidHash = errors.New("invalid password hash")
)
