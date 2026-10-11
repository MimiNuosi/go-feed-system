package hot

import (
	"testing"
	"time"
)

func TestParseWindow(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Window
		wantErr bool
	}{
		{name: "一小时", raw: "1h", want: WindowOneHour},
		{name: "二十四小时", raw: "24h", want: WindowTwentyFourH},
		{name: "空字符串", raw: "", wantErr: true},
		{name: "不支持", raw: "7d", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseWindow(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse window: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

func TestWindowBucketKeys(t *testing.T) {
	now := time.Date(2026, 10, 10, 10, 7, 30, 0, time.UTC)

	tests := []struct {
		name      string
		window    Window
		wantCount int
		wantFirst string
	}{
		{
			name:      "一小时窗口",
			window:    WindowOneHour,
			wantCount: 60,
			wantFirst: "hot:video:1h:1791626820",
		},
		{
			name:      "二十四小时窗口",
			window:    WindowTwentyFourH,
			wantCount: 288,
			wantFirst: "hot:video:24h:1791626700",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keys, err := tt.window.BucketKeys(now)
			if err != nil {
				t.Fatalf("bucket keys: %v", err)
			}
			if len(keys) != tt.wantCount {
				t.Fatalf("expected %d keys, got %d", tt.wantCount, len(keys))
			}
			if keys[0] != tt.wantFirst {
				t.Fatalf("expected first key %q, got %q", tt.wantFirst, keys[0])
			}
		})
	}
}
