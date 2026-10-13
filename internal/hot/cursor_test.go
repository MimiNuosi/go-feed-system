package hot

import (
	"encoding/base64"
	"errors"
	"testing"
)

func TestCursorRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		cursor Cursor
	}{
		{name: "普通位置", cursor: Cursor{Score: 20, VideoID: 103}},
		{name: "小数分数", cursor: Cursor{Score: 12.5, VideoID: 7}},
		{name: "负分（取消点赞后可能出现）", cursor: Cursor{Score: -3, VideoID: 1}},
		{name: "很大的 videoID", cursor: Cursor{Score: 1, VideoID: 1 << 40}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := EncodeCursor(tt.cursor)
			if err != nil {
				t.Fatalf("encode cursor: %v", err)
			}

			decoded, err := DecodeCursor(encoded)
			if err != nil {
				t.Fatalf("decode cursor: %v", err)
			}
			if decoded == nil || *decoded != tt.cursor {
				t.Fatalf("round trip mismatch: got %+v, want %+v", decoded, tt.cursor)
			}
		})
	}
}

func TestDecodeCursor(t *testing.T) {
	encode := func(raw string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(raw))
	}

	tests := []struct {
		name    string
		raw     string
		wantNil bool
		want    Cursor
		wantErr error
	}{
		{name: "空串表示第一页", raw: "", wantNil: true},
		{
			name: "正常游标",
			raw:  encode(`{"score":20,"video_id":103}`),
			want: Cursor{Score: 20, VideoID: 103},
		},
		{name: "不是 base64", raw: "!!!not-base64!!!", wantErr: ErrInvalidInput},
		{
			name:    "base64 里不是 JSON",
			raw:     encode("hello"),
			wantErr: ErrInvalidInput,
		},
		{
			name:    "video_id 为 0",
			raw:     encode(`{"score":20,"video_id":0}`),
			wantErr: ErrInvalidInput,
		},
		{
			name:    "缺少 video_id",
			raw:     encode(`{"score":20}`),
			wantErr: ErrInvalidInput,
		},
		{
			name:    "分数超出 float64 范围",
			raw:     encode(`{"score":1e999,"video_id":5}`),
			wantErr: ErrInvalidInput,
		},
		{
			name:    "字段类型不对",
			raw:     encode(`{"score":"20","video_id":103}`),
			wantErr: ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cursor, err := DecodeCursor(tt.raw)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("decode cursor: %v", err)
			}
			if tt.wantNil {
				if cursor != nil {
					t.Fatalf("expected nil cursor, got %+v", cursor)
				}
				return
			}
			if cursor == nil || *cursor != tt.want {
				t.Fatalf("got %+v, want %+v", cursor, tt.want)
			}
		})
	}
}
