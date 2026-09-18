package requestid

import "context"

// Header 是服务之间传递请求标识时使用的标准请求头。
const Header = "X-Request-ID"

type contextKey struct{}

// WithContext 把请求 ID 放进 context，供后续日志和业务代码读取。
//
// 不要使用普通 string 作为 context key，否则不同的包可能使用同名 key
// 覆盖彼此的数据。未导出的空结构体类型可以避免这种冲突。
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}
