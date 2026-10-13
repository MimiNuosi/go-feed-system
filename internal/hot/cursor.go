package hot

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
)

// EncodeCursor 把排名游标编码成不透明字符串。
//
// 客户端不需要理解它的内容，只负责原样回传。
// 用不透明游标而不是直接暴露 score/videoID，是为了以后能换实现
// （比如加签名、换成分页键）而不破坏接口契约。
func EncodeCursor(cursor Cursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode hot cursor: marshal: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// DecodeCursor 解析客户端回传的游标。
//
// 空字符串表示第一页，返回 (nil, nil)。
//
// 注意：游标是客户端可控的输入，所以要当成不可信数据处理：
// 解析失败一律按 invalid input 拒绝，不能让脏数据进到查询里。
func DecodeCursor(raw string) (*Cursor, error) {
	if raw == "" {
		return nil, nil
	}

	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode hot cursor: base64: %w", ErrInvalidInput)
	}

	var cursor Cursor
	if err := json.Unmarshal(data, &cursor); err != nil {
		return nil, fmt.Errorf("decode hot cursor: json: %w", ErrInvalidInput)
	}

	// videoID = 0 说明游标来自别处或者被改过；NaN/Inf 不能参与分数比较。
	if cursor.VideoID == 0 ||
		math.IsNaN(cursor.Score) ||
		math.IsInf(cursor.Score, 0) {
		return nil, fmt.Errorf("decode hot cursor: invalid position: %w", ErrInvalidInput)
	}

	return &cursor, nil
}
