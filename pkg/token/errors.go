package token

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidToken = errors.New("invalid token")

	// 以下错误都包含 ErrInvalidToken，因此中间件既可以统一映射为 401，
	// 又可以在内部日志中记录具体的失败类别。
	ErrTokenExpired     = fmt.Errorf("%w: token expired", ErrInvalidToken)
	ErrInvalidIssuer    = fmt.Errorf("%w: invalid issuer", ErrInvalidToken)
	ErrInvalidSignature = fmt.Errorf("%w: invalid signature", ErrInvalidToken)
	ErrInvalidSubject   = fmt.Errorf("%w: invalid subject", ErrInvalidToken)
)
