package storage

import "errors"

var (
	ErrObjectNotFound = errors.New("storage object not found")
	ErrInvalidInput   = errors.New("invalid storage input")
)
