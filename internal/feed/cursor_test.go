package feed

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	want := Cursor{
		CreatedAt: time.Date(2026, 10, 2, 12, 30, 0, 123000000, time.FixedZone("CST", 8*60*60)),
		ID:        123,
	}

	encoded, err := encodeCursor(want)
	if err != nil {
		t.Fatalf("encode cursor: %v", err)
	}
	if encoded == "" {
		t.Fatal("expected non-empty cursor")
	}

	got, err := decodeCursor(encoded)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if !got.CreatedAt.Equal(want.CreatedAt) || got.ID != want.ID {
		t.Fatalf("expected cursor %+v, got %+v", want, got)
	}
}

func TestDecodeCursorRejectsInvalidValues(t *testing.T) {
	validJSON := `{"created_at":"2026-10-02T12:30:00.123+08:00","id":123}`

	tests := []struct {
		name    string
		raw     string
		wantErr error
	}{
		{
			name:    "空游标表示第一页",
			raw:     "",
			wantErr: nil,
		},
		{
			name:    "非法 Base64",
			raw:     "not-base64!",
			wantErr: ErrInvalidInput,
		},
		{
			name:    "非法 JSON",
			raw:     base64.RawURLEncoding.EncodeToString([]byte(`not-json`)),
			wantErr: ErrInvalidInput,
		},
		{
			name:    "缺少 created_at",
			raw:     base64.RawURLEncoding.EncodeToString([]byte(`{"id":123}`)),
			wantErr: ErrInvalidInput,
		},
		{
			name:    "缺少 id",
			raw:     base64.RawURLEncoding.EncodeToString([]byte(`{"created_at":"2026-10-02T12:30:00.123+08:00"}`)),
			wantErr: ErrInvalidInput,
		},
		{
			name:    "合法 JSON",
			raw:     base64.RawURLEncoding.EncodeToString([]byte(validJSON)),
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cursor, err := decodeCursor(tt.raw)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}
			if tt.wantErr == nil && tt.raw == "" && cursor != (Cursor{}) {
				t.Fatalf("expected zero cursor for first page, got %+v", cursor)
			}
		})
	}
}
