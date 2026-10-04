package outbox

import "errors"

var (
	ErrInvalidEvent = errors.New("invalid outbox event")
)
