package hot

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisRepository_RecordViewIntegration(t *testing.T) {
	client := openHotTestRedis(t)
	repository := NewRedisRepository(client)
	ctx := context.Background()
	occurredAt := time.Date(2026, 10, 10, 10, 7, 30, 0, time.UTC)
	bucket1h, err := WindowOneHour.CurrentBucketKey(occurredAt)
	if err != nil {
		t.Fatalf("build cleanup 1h bucket: %v", err)
	}
	bucket24h, err := WindowTwentyFourH.CurrentBucketKey(occurredAt)
	if err != nil {
		t.Fatalf("build cleanup 24h bucket: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Del(
			context.Background(),
			ViewDedupKey(101, "user:1"),
			ViewDedupKey(202, "user:2"),
			PendingViewsKey(),
			bucket1h,
			bucket24h,
		).Err()
	})

	t.Run("首次浏览写入全部计数", func(t *testing.T) {
		const videoID = uint64(101)
		const viewerKey = "user:1"

		recorded, err := repository.RecordView(ctx, videoID, viewerKey, occurredAt)
		if err != nil {
			t.Fatalf("record first view: %v", err)
		}
		if !recorded {
			t.Fatal("expected first view to be recorded")
		}

		recorded, err = repository.RecordView(ctx, videoID, viewerKey, occurredAt)
		if err != nil {
			t.Fatalf("record duplicate view: %v", err)
		}
		if recorded {
			t.Fatal("expected duplicate view to be ignored")
		}

		pending, err := client.HGet(ctx, PendingViewsKey(), "101").Result()
		if err != nil {
			t.Fatalf("get pending views: %v", err)
		}
		if pending != "1" {
			t.Fatalf("expected pending views 1, got %s", pending)
		}

		score1h, err := client.ZScore(ctx, bucket1h, formatVideoID(videoID)).Result()
		if err != nil {
			t.Fatalf("get 1h score: %v", err)
		}
		if score1h != 1 {
			t.Fatalf("expected 1h score 1, got %v", score1h)
		}

		score24h, err := client.ZScore(ctx, bucket24h, formatVideoID(videoID)).Result()
		if err != nil {
			t.Fatalf("get 24h score: %v", err)
		}
		if score24h != 1 {
			t.Fatalf("expected 24h score 1, got %v", score24h)
		}
	})

	t.Run("去重 TTL 到期后可以再次记录", func(t *testing.T) {
		const videoID = uint64(202)
		const viewerKey = "user:2"
		repository.viewDedupTTL = 20 * time.Millisecond

		recorded, err := repository.RecordView(ctx, videoID, viewerKey, occurredAt)
		if err != nil {
			t.Fatalf("record first view: %v", err)
		}
		if !recorded {
			t.Fatal("expected first view to be recorded")
		}

		time.Sleep(30 * time.Millisecond)

		recorded, err = repository.RecordView(ctx, videoID, viewerKey, occurredAt)
		if err != nil {
			t.Fatalf("record view after TTL: %v", err)
		}
		if !recorded {
			t.Fatal("expected view after TTL to be recorded")
		}
	})
}

func TestRedisRepository_TopVideoIDsIntegration(t *testing.T) {
	client := openHotTestRedis(t)
	repository := NewRedisRepository(client)
	ctx := context.Background()
	at := time.Date(2026, 10, 10, 10, 7, 30, 0, time.UTC)

	currentBucket, err := WindowOneHour.CurrentBucketKey(at)
	if err != nil {
		t.Fatalf("build current bucket: %v", err)
	}
	previousBucket, err := WindowOneHour.CurrentBucketKey(at.Add(-time.Minute))
	if err != nil {
		t.Fatalf("build previous bucket: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Del(context.Background(), currentBucket, previousBucket).Err()
	})

	if err := client.ZAdd(ctx, currentBucket,
		redis.Z{Score: 3, Member: formatVideoID(1)},
		redis.Z{Score: 2, Member: formatVideoID(2)},
		redis.Z{Score: 5, Member: formatVideoID(100)},
	).Err(); err != nil {
		t.Fatalf("seed current bucket: %v", err)
	}
	if err := client.ZAdd(ctx, previousBucket,
		redis.Z{Score: 2, Member: formatVideoID(1)},
		redis.Z{Score: 5, Member: formatVideoID(3)},
	).Err(); err != nil {
		t.Fatalf("seed previous bucket: %v", err)
	}

	tests := []struct {
		name      string
		window    Window
		at        time.Time
		cursor    *Cursor
		limit     int
		wantIDs   []uint64
		wantScore []float64
		wantErr   error
	}{
		{
			name:      "合并多个 bucket 并按分数和 videoID 降序",
			window:    WindowOneHour,
			at:        at,
			limit:     10,
			wantIDs:   []uint64{100, 3, 1, 2},
			wantScore: []float64{5, 5, 5, 2},
		},
		{
			name:      "limit 截断结果",
			window:    WindowOneHour,
			at:        at,
			limit:     2,
			wantIDs:   []uint64{100, 3},
			wantScore: []float64{5, 5},
		},
		{
			name:    "cursor 暂不支持",
			window:  WindowOneHour,
			at:      at,
			cursor:  &Cursor{},
			limit:   10,
			wantErr: ErrInvalidInput,
		},
		{
			name:    "window 为空",
			at:      at,
			limit:   10,
			wantErr: ErrInvalidInput,
		},
		{
			name:    "limit 非正数",
			window:  WindowOneHour,
			at:      at,
			wantErr: ErrInvalidInput,
		},
		{
			name:    "时间为零",
			window:  WindowOneHour,
			limit:   10,
			wantErr: ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := repository.TopVideoIDs(
				ctx,
				tt.window,
				tt.at,
				tt.cursor,
				tt.limit,
			)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("top video IDs: %v", err)
			}
			if len(got) != len(tt.wantIDs) {
				t.Fatalf("expected %d results, got %v", len(tt.wantIDs), got)
			}
			for i := range tt.wantIDs {
				if got[i].VideoID != tt.wantIDs[i] {
					t.Fatalf("expected IDs %v, got %v", tt.wantIDs, got)
				}
				if got[i].Score != tt.wantScore[i] {
					t.Fatalf("expected scores %v, got %v", tt.wantScore, got)
				}
			}
		})
	}
}

func TestRedisRepository_RecordViewRejectsInvalidInput(t *testing.T) {
	client := openHotTestRedis(t)
	repository := NewRedisRepository(client)
	ctx := context.Background()

	tests := []struct {
		name       string
		videoID    uint64
		viewerKey  string
		occurredAt time.Time
	}{
		{
			name:       "视频 ID 为零",
			viewerKey:  "user:1",
			occurredAt: time.Now(),
		},
		{
			name:       "viewer key 为空",
			videoID:    1,
			occurredAt: time.Now(),
		},
		{
			name:      "时间为零",
			videoID:   1,
			viewerKey: "user:1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repository.RecordView(
				ctx,
				tt.videoID,
				tt.viewerKey,
				tt.occurredAt,
			)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

func openHotTestRedis(t *testing.T) *redis.Client {
	t.Helper()

	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	db := 15
	if rawDB := os.Getenv("TEST_REDIS_DB"); rawDB != "" {
		parsed, err := strconv.Atoi(rawDB)
		if err != nil {
			t.Fatalf("invalid TEST_REDIS_DB=%q: %v", rawDB, err)
		}
		db = parsed
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("TEST_REDIS_PASSWORD"),
		DB:       db,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis not available at %s: %v", addr, err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})

	return client
}
