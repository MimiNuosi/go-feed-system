package httpx

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"go-feed-system/pkg/requestid"
)

// ErrorCode 是稳定的业务错误码。
//
// Go 没有 C++ 的 enum class。这里使用命名字符串类型：
// 1. 编译期仍然有类型约束。
// 2. JSON 中直接显示可读字符串，不依赖魔法数字。
// 3. 新增错误码时不会改变已有错误码的值。
type ErrorCode string

const (
	ErrorCodeInvalidArgument    ErrorCode = "INVALID_ARGUMENT"
	ErrorCodeUnauthorized       ErrorCode = "UNAUTHORIZED"
	ErrorCodeInvalidCredentials ErrorCode = "INVALID_CREDENTIALS"
	ErrorCodeNotFound           ErrorCode = "NOT_FOUND"
	ErrorCodeConflict           ErrorCode = "CONFLICT"
	ErrorCodeInternal           ErrorCode = "INTERNAL"
)

type ErrorBody struct {
	Code      ErrorCode `json:"code"`
	Message   string    `json:"message"`
	RequestID string    `json:"request_id,omitempty"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// Abort 立即终止当前请求，并返回统一的错误 JSON。
//
// 对外只返回稳定的业务错误码和面向用户的 message。
// 数据库错误、JSON 解析错误等原始信息应在服务内部记录。
func Abort(c *gin.Context, status int, code ErrorCode, message string) {
	c.AbortWithStatusJSON(status, ErrorResponse{
		Error: ErrorBody{
			Code:      code,
			Message:   message,
			RequestID: requestid.FromContext(c.Request.Context()),
		},
	})
}

// StatusCode 把业务错误码映射为 HTTP 状态码。
//
// 业务错误码和 HTTP 状态码不是一回事：前者给前端判断具体原因，
// 后者给 HTTP 客户端、网关和监控系统判断请求结果类别。
func StatusCode(code ErrorCode) int {
	switch code {
	case ErrorCodeInvalidArgument:
		return http.StatusBadRequest
	case ErrorCodeUnauthorized, ErrorCodeInvalidCredentials:
		return http.StatusUnauthorized
	case ErrorCodeNotFound:
		return http.StatusNotFound
	case ErrorCodeConflict:
		return http.StatusConflict
	case ErrorCodeInternal:
		fallthrough
	default:
		return http.StatusInternalServerError
	}
}
