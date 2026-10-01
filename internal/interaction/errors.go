package interaction

import "errors"

var (
	ErrInvalidInput     = errors.New("invalid interaction input")
	ErrInvalidTarget    = errors.New("invalid interaction target")
	ErrForbidden        = errors.New("interaction forbidden")
	ErrAlreadyFollowing = errors.New("already following")
	ErrAlreadyLiked     = errors.New("already liked")
)
