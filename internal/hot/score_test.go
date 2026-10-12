package hot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeScoreWriter struct {
	videoID uint64
	delta   int64
	at      time.Time
	err     error
}

func (f *fakeScoreWriter) AddScore(
	ctx context.Context,
	videoID uint64,
	delta int64,
	at time.Time,
) error {
	f.videoID = videoID
	f.delta = delta
	f.at = at
	return f.err
}

func TestScoreRecorder_AddScore(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		writer  *fakeScoreWriter
		videoID uint64
		delta   int64
	}{
		{
			name:    "记录正分",
			writer:  &fakeScoreWriter{},
			videoID: 10,
			delta:   3,
		},
		{
			name:    "记录负分",
			writer:  &fakeScoreWriter{},
			videoID: 10,
			delta:   -3,
		},
		{
			name:    "写入失败不影响调用方",
			writer:  &fakeScoreWriter{err: errors.New("redis failed")},
			videoID: 10,
			delta:   5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewScoreRecorder(
				tt.writer,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)

			recorder.AddScore(context.Background(), tt.videoID, tt.delta, now)

			if tt.writer.videoID != tt.videoID {
				t.Fatalf("expected video ID %d, got %d", tt.videoID, tt.writer.videoID)
			}
			if tt.writer.delta != tt.delta {
				t.Fatalf("expected delta %d, got %d", tt.delta, tt.writer.delta)
			}
			if !tt.writer.at.Equal(now) {
				t.Fatalf("expected at %v, got %v", now, tt.writer.at)
			}
		})
	}
}
