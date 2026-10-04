package outbox

import "errors"

var (
	ErrInvalidEvent   = errors.New("invalid outbox event")
	ErrNotImplemented = errors.New("outbox not implemented")
)
