package message

import "errors"

var (
	ErrInvalidInput      = errors.New("invalid message input")
	ErrRecipientNotFound = errors.New("message recipient not found")
)
