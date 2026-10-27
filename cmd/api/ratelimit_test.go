package main

import (
	"testing"
	"time"

	"go-feed-system/pkg/config"
)

func TestNewRateLimitHandlers(t *testing.T) {
	validBucket := config.RateLimitBucket{Burst: 100, Interval: time.Millisecond}

	tests := []struct {
		name         string
		cfg          config.RateLimitConfig
		wantDisabled bool
		wantErr      bool
	}{
		{
			name: "启用时返回两层中间件",
			cfg: config.RateLimitConfig{
				Enabled:  true,
				Global:   validBucket,
				Identity: config.RateLimitBucket{Burst: 10, Interval: 100 * time.Millisecond},
			},
		},
		{
			name:         "关闭时两层都不挂载",
			cfg:          config.RateLimitConfig{Enabled: false},
			wantDisabled: true,
		},
		{
			name: "全局额度非法时报错",
			cfg: config.RateLimitConfig{
				Enabled:  true,
				Global:   config.RateLimitBucket{Burst: 0, Interval: time.Millisecond},
				Identity: config.RateLimitBucket{Burst: 10, Interval: time.Millisecond},
			},
			wantErr: true,
		},
		{
			name: "身份额度非法时报错",
			cfg: config.RateLimitConfig{
				Enabled:  true,
				Global:   validBucket,
				Identity: config.RateLimitBucket{Burst: 10, Interval: 0},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			global, identity, err := newRateLimitHandlers(
				tt.cfg,
				nil,
				nil,
				newSupervisorTestLogger(),
			)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantDisabled {
				if global != nil || identity != nil {
					t.Fatal("expected nil handlers when the limiter is disabled")
				}
				return
			}
			if global == nil || identity == nil {
				t.Fatal("expected both rate limit handlers")
			}
		})
	}
}
