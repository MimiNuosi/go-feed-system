package video

import "errors"

var (
	ErrNotFound             = errors.New("video not found")
	ErrInvalidInput         = errors.New("invalid video input")
	ErrFileTooLarge         = errors.New("video file is too large")
	ErrUnsupportedMediaType = errors.New("unsupported video media type")
	ErrObjectNotFound       = errors.New("video object not found")
	ErrConflict             = errors.New("video already exists")
)
