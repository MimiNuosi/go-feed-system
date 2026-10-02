package feed

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// encodeCursor 把复合游标编码成不透明字符串。
func encodeCursor(cursor Cursor) (string, error) {
	// 1. JSON 序列化
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode cursor: marshal: %w", err)
	}
	// 2. Base64 URL 编码
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// decodeCursor 解析客户端回传的游标。
func decodeCursor(raw string) (Cursor, error) {
	if raw == "" {
		return Cursor{}, nil // 空字符串表示第一页，返回空游标
	}

	// 1. Base64 URL 解码
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, ErrInvalidInput
	}

	// 2. JSON 反序列化
	var cursor Cursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return Cursor{}, ErrInvalidInput
	}

	// 3. 校验关键字段是否为零值
	if cursor.CreatedAt.IsZero() || cursor.ID == 0 {
		return Cursor{}, ErrInvalidInput
	}

	return cursor, nil
}
