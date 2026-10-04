package config

import (
	"testing"
	"time"
)

func TestDurationFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback time.Duration
		want     time.Duration
		wantErr  bool
	}{
		{
			name:     "未配置时使用默认值",
			value:    "",
			fallback: time.Second,
			want:     time.Second,
		},
		{
			name:     "解析合法时长",
			value:    "250ms",
			fallback: time.Second,
			want:     250 * time.Millisecond,
		},
		{
			name:     "非法时长返回错误",
			value:    "soon",
			fallback: time.Second,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("OUTBOX_PUBLISH_INTERVAL", tt.value)

			got, err := durationFromEnv("OUTBOX_PUBLISH_INTERVAL", tt.fallback)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if got != tt.want {
				t.Errorf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
