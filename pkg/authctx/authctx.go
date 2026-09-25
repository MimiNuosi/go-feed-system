package authctx

import "context"

type userIDKey struct{}

// WithUserID 把当前认证用户 ID 放进标准 context。
//
// 使用未导出的 userIDKey，而不是字符串 "userID"，避免不同包使用同名 key
// 互相覆盖。这个设计对应 C++ 里为请求上下文定义专用字段，而不是共享字符串表。
func WithUserID(ctx context.Context, userID uint64) context.Context {
	return context.WithValue(ctx, userIDKey{}, userID)
}

func UserID(ctx context.Context) (uint64, bool) {
	userID, ok := ctx.Value(userIDKey{}).(uint64)
	return userID, ok
}
