package hot

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestRedisRepository_RecordViewIntegration(t *testing.T) {
	client := openHotTestRedis(t)
	repository := NewRedisRepository(client, discardLogger())
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
	repository := NewRedisRepository(client, discardLogger())
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
	repository := NewRedisRepository(client, discardLogger())
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

func TestRedisRepository_AddScoreIntegration(t *testing.T) {
	client := openHotTestRedis(t)
	repository := NewRedisRepository(client, discardLogger())
	ctx := context.Background()
	at := time.Date(2026, 10, 10, 10, 7, 30, 0, time.UTC)

	bucket1h, err := WindowOneHour.CurrentBucketKey(at)
	if err != nil {
		t.Fatalf("build 1h bucket: %v", err)
	}
	bucket24h, err := WindowTwentyFourH.CurrentBucketKey(at)
	if err != nil {
		t.Fatalf("build 24h bucket: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Del(context.Background(), bucket1h, bucket24h).Err()
	})

	tests := []struct {
		name       string
		videoID    uint64
		delta      int64
		at         time.Time
		wantErr    error
		wantScore1 float64
		wantScore2 float64
	}{
		{
			name:       "正分同时更新两个窗口",
			videoID:    100,
			delta:      5,
			at:         at,
			wantScore1: 5,
			wantScore2: 5,
		},
		{
			name:       "负分同时更新两个窗口",
			videoID:    100,
			delta:      -3,
			at:         at,
			wantScore1: 2,
			wantScore2: 2,
		},
		{
			name:    "delta 为零不访问 Redis",
			videoID: 200,
			delta:   0,
			at:      at,
		},
		{
			name:    "videoID 为零",
			delta:   5,
			at:      at,
			wantErr: ErrInvalidInput,
		},
		{
			name:    "时间为零",
			videoID: 100,
			delta:   5,
			wantErr: ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repository.AddScore(ctx, tt.videoID, tt.delta, tt.at)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("add score: %v", err)
			}

			if tt.delta == 0 {
				_, err := client.ZScore(ctx, bucket1h, formatVideoID(tt.videoID)).Result()
				if !errors.Is(err, redis.Nil) {
					t.Fatalf("expected no score change for zero delta, got %v", err)
				}
				return
			}

			score1h, err := client.ZScore(ctx, bucket1h, formatVideoID(tt.videoID)).Result()
			if err != nil {
				t.Fatalf("get 1h score: %v", err)
			}
			if score1h != tt.wantScore1 {
				t.Fatalf("expected 1h score %v, got %v", tt.wantScore1, score1h)
			}

			score24h, err := client.ZScore(ctx, bucket24h, formatVideoID(tt.videoID)).Result()
			if err != nil {
				t.Fatalf("get 24h score: %v", err)
			}
			if score24h != tt.wantScore2 {
				t.Fatalf("expected 24h score %v, got %v", tt.wantScore2, score24h)
			}
		})
	}
}

// TestRedisRepository_PendingViewsIntegration 是阶段 9.5 的验收测试。
//
// 它现在必然失败，因为 PendingViews / AckPendingView 还是 TODO 骨架。
// 把这两个方法实现完，这个测试就会变绿。
// 需要本机 Redis 可用，否则会自动跳过。
func TestRedisRepository_PendingViewsIntegration(t *testing.T) {
	client := openHotTestRedis(t)
	repository := NewRedisRepository(client, discardLogger())
	ctx := context.Background()

	t.Cleanup(func() {
		_ = client.Del(context.Background(), PendingViewsKey()).Err()
	})

	// 造三条正常增量，再加一条 field 不是数字的脏数据。
	if err := client.HSet(
		ctx,
		PendingViewsKey(),
		"104", 3,
		"102", 1,
		"103", 2,
	).Err(); err != nil {
		t.Fatalf("seed pending views: %v", err)
	}
	if err := client.HSet(ctx, PendingViewsKey(), "not-a-number", 5).Err(); err != nil {
		t.Fatalf("seed dirty field: %v", err)
	}

	pendings, err := repository.PendingViews(ctx, 10)
	if err != nil {
		t.Fatalf("pending views: %v", err)
	}
	if len(pendings) != 3 {
		t.Fatalf("expected 3 valid pendings, got %d: %v", len(pendings), pendings)
	}
	if pendings[102] != 1 || pendings[103] != 2 || pendings[104] != 3 {
		t.Fatalf("unexpected pendings: %v", pendings)
	}

	limited, err := repository.PendingViews(ctx, 2)
	if err != nil {
		t.Fatalf("pending views with limit: %v", err)
	}
	if len(limited) != 2 {
		t.Fatalf("expected limit to cap result at 2, got %d: %v", len(limited), limited)
	}

	if err := repository.AckPendingView(ctx, 104); err != nil {
		t.Fatalf("ack pending view: %v", err)
	}
	value, err := client.HGet(ctx, PendingViewsKey(), "104").Result()
	if !errors.Is(err, redis.Nil) {
		t.Fatalf("expected field 104 to be deleted, got value=%q err=%v", value, err)
	}

	// 重复 ack 必须幂等：第二条已经没了，再删一次也算成功。
	if err := repository.AckPendingView(ctx, 104); err != nil {
		t.Fatalf("repeat ack should be idempotent, got %v", err)
	}
}

// assertVideoIDs 按顺序比较排名结果里的 videoID。
func assertVideoIDs(t *testing.T, label string, got []RankedVideo, want []uint64) {
	t.Helper()

	ids := make([]uint64, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.VideoID)
	}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("%s: expected video IDs %v, got %v", label, want, ids)
	}
}

// TestRedisRepository_TopVideoIDsPaginationIntegration 是阶段 9.2 游标分页的验收测试。
//
// 它专门造出"同分"的场景，因为同一个分数上的分页是最容易出错的地方：
// 只按"分数严格小于游标"翻页，会整段漏掉同分的其余成员。
//
// 现在是红的（rangeFromCursor 的游标分支还是 TODO），实现完就会变绿。
func TestRedisRepository_TopVideoIDsPaginationIntegration(t *testing.T) {
	client := openHotTestRedis(t)
	repository := NewRedisRepository(client, discardLogger())
	ctx := context.Background()
	at := time.Date(2026, 10, 13, 10, 7, 30, 0, time.UTC)

	bucket1h, err := WindowOneHour.CurrentBucketKey(at)
	if err != nil {
		t.Fatalf("build bucket key: %v", err)
	}

	t.Cleanup(func() {
		_ = client.Del(context.Background(), bucket1h).Err()
	})

	// 分数刻意安排成 101(30) > 103(20) == 102(20) > 104(10)。
	// 同分时 member 按字典序降序，所以 103 排在 102 前面
	// （member 是 20 位补零的 videoID）。
	seeds := map[uint64]float64{101: 30, 102: 20, 103: 20, 104: 10}
	for videoID, score := range seeds {
		if err := client.ZAdd(ctx, bucket1h, redis.Z{
			Score:  score,
			Member: formatVideoID(videoID),
		}).Err(); err != nil {
			t.Fatalf("seed score: %v", err)
		}
	}

	page1, err := repository.TopVideoIDs(ctx, WindowOneHour, at, nil, 2)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	assertVideoIDs(t, "第一页", page1, []uint64{101, 103})

	// 第二页必须从同分的 102 接上：只查"分数小于 20"就会漏掉它。
	cursor := &Cursor{Score: page1[1].Score, VideoID: page1[1].VideoID}
	page2, err := repository.TopVideoIDs(ctx, WindowOneHour, at, cursor, 2)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	assertVideoIDs(t, "第二页", page2, []uint64{102, 104})

	next := &Cursor{Score: page2[1].Score, VideoID: page2[1].VideoID}
	page3, err := repository.TopVideoIDs(ctx, WindowOneHour, at, next, 2)
	if err != nil {
		t.Fatalf("third page: %v", err)
	}
	if len(page3) != 0 {
		t.Fatalf("expected an empty third page, got %v", page3)
	}

	// 逐条翻页：每页一条，拿到的顺序必须和整体顺序完全一致，且不重不漏。
	var (
		all    []uint64
		walker *Cursor
	)
	for i := 0; i < 10; i++ {
		page, err := repository.TopVideoIDs(ctx, WindowOneHour, at, walker, 1)
		if err != nil {
			t.Fatalf("walk page %d: %v", i, err)
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page[0].VideoID)
		walker = &Cursor{Score: page[0].Score, VideoID: page[0].VideoID}
	}
	if !reflect.DeepEqual(all, []uint64{101, 103, 102, 104}) {
		t.Fatalf("逐条翻页的顺序不对: %v", all)
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
