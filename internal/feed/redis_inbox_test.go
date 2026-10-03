package feed

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// 连接测试用的 Redis，并注册清理逻辑
func openTestRedis(t *testing.T) *redis.Client {
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

	client := redis.NewClient(&redis.Options{Addr: addr, DB: db})

	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("redis not available at %s: %v", addr, err)
	}

	// 测试结束后，清空本次测试创建的所有 key
	t.Cleanup(func() {
		ctx := context.Background()
		_ = client.FlushDB(ctx).Err()
		_ = client.Close()
	})

	return client
}

func TestRedisInbox_AddAndList(t *testing.T) {
	client := openTestRedis(t)
	inbox := NewRedisInbox(client, 1000)
	ctx := context.Background()

	now := time.Now()

	t.Run("空粉丝列表不访问 Redis", func(t *testing.T) {
		err := inbox.Add(ctx, nil, 1001, now)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("单个用户写入后能正确读取", func(t *testing.T) {
		if err := inbox.Add(ctx, []uint64{1}, 1001, now); err != nil {
			t.Fatalf("add failed: %v", err)
		}

		ids, err := inbox.List(ctx, 1, 0, 10)
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != 1001 {
			t.Errorf("expected [1001], got %v", ids)
		}
	})

	t.Run("多个用户共享同一视频", func(t *testing.T) {
		users := []uint64{2, 3, 4}
		if err := inbox.Add(ctx, users, 2002, now); err != nil {
			t.Fatalf("add failed: %v", err)
		}

		for _, uid := range users {
			ids, err := inbox.List(ctx, uid, 0, 10)
			if err != nil {
				t.Fatalf("list for user %d failed: %v", uid, err)
			}
			if len(ids) != 1 || ids[0] != 2002 {
				t.Errorf("user %d: expected [2002], got %v", uid, ids)
			}
		}
	})

	t.Run("重复写入同一视频是幂等的", func(t *testing.T) {
		if err := inbox.Add(ctx, []uint64{5}, 3003, now); err != nil {
			t.Fatalf("add failed: %v", err)
		}
		// 再写一次，ZSet 会更新 score，不会产生重复 member
		if err := inbox.Add(ctx, []uint64{5}, 3003, now.Add(time.Second)); err != nil {
			t.Fatalf("second add failed: %v", err)
		}

		ids, err := inbox.List(ctx, 5, 0, 10)
		if err != nil {
			t.Fatalf("list failed: %v", err)
		}
		if len(ids) != 1 || ids[0] != 3003 {
			t.Errorf("expected exactly [3003], got %v", ids)
		}
	})
}

func TestRedisInbox_Pagination(t *testing.T) {
	client := openTestRedis(t)
	inbox := NewRedisInbox(client, 1000)
	ctx := context.Background()

	// 写入 5 条视频，时间递增（模拟先后发布）
	baseTime := time.Now().Add(-10 * time.Minute)
	for i := uint64(1); i <= 5; i++ {
		// 时间递增：i 越大，发布越晚，score 越高，在 ZSet 中排名越靠前
		publishedAt := baseTime.Add(time.Duration(i) * time.Minute)
		if err := inbox.Add(ctx, []uint64{10}, i, publishedAt); err != nil {
			t.Fatalf("add video %d failed: %v", i, err)
		}
	}

	// 第一页取 2 条，应该是最新的两条：videoID = 5, 4
	page1, err := inbox.List(ctx, 10, 0, 2)
	if err != nil {
		t.Fatalf("page1 failed: %v", err)
	}
	if len(page1) != 2 || page1[0] != 5 || page1[1] != 4 {
		t.Fatalf("page1 expected [5 4], got %v", page1)
	}

	// 第二页：游标是 page1 最后一条 = 4，应该拿到 3, 2
	page2, err := inbox.List(ctx, 10, page1[len(page1)-1], 2)
	if err != nil {
		t.Fatalf("page2 failed: %v", err)
	}
	if len(page2) != 2 || page2[0] != 3 || page2[1] != 2 {
		t.Fatalf("page2 expected [3 2], got %v", page2)
	}

	// 第三页：游标是 page2 最后一条 = 2，应该拿到 1
	page3, err := inbox.List(ctx, 10, page2[len(page2)-1], 2)
	if err != nil {
		t.Fatalf("page3 failed: %v", err)
	}
	if len(page3) != 1 || page3[0] != 1 {
		t.Fatalf("page3 expected [1], got %v", page3)
	}

	// 不存在的游标：应返回空列表，不应报错
	page4, err := inbox.List(ctx, 10, 9999, 2)
	if err != nil {
		t.Fatalf("page4 failed: %v", err)
	}
	if len(page4) != 0 {
		t.Fatalf("page4 expected empty, got %v", page4)
	}
}

func TestRedisInbox_SameTimestampStableOrder(t *testing.T) {
	client := openTestRedis(t)
	inbox := NewRedisInbox(client, 1000)
	ctx := context.Background()

	// 关键：三条视频的发布时间完全相同（score 相同）
	sameTime := time.Now()
	if err := inbox.Add(ctx, []uint64{20}, 100, sameTime); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if err := inbox.Add(ctx, []uint64{20}, 300, sameTime); err != nil {
		t.Fatalf("add failed: %v", err)
	}
	if err := inbox.Add(ctx, []uint64{20}, 200, sameTime); err != nil {
		t.Fatalf("add failed: %v", err)
	}

	ids, err := inbox.List(ctx, 20, 0, 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	// 期望按 videoID 倒序（字典序 = 数值序，因为补零了）
	if len(ids) != 3 || ids[0] != 300 || ids[1] != 200 || ids[2] != 100 {
		t.Fatalf("expected [300 200 100], got %v", ids)
	}
}

func TestRedisInbox_Trim(t *testing.T) {
	client := openTestRedis(t)
	// 收件箱最大保留 3 条
	inbox := NewRedisInbox(client, 3)
	ctx := context.Background()

	baseTime := time.Now().Add(-10 * time.Minute)
	// 写入 5 条视频
	for i := uint64(1); i <= 5; i++ {
		publishedAt := baseTime.Add(time.Duration(i) * time.Minute)
		if err := inbox.Add(ctx, []uint64{30}, i, publishedAt); err != nil {
			t.Fatalf("add video %d failed: %v", i, err)
		}
	}

	// 执行裁剪
	if err := inbox.Trim(ctx, 30); err != nil {
		t.Fatalf("trim failed: %v", err)
	}

	// 裁剪后只剩下最新的 3 条：videoID = 5, 4, 3
	ids, err := inbox.List(ctx, 30, 0, 10)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 items after trim, got %d: %v", len(ids), ids)
	}
	if ids[0] != 5 || ids[1] != 4 || ids[2] != 3 {
		t.Fatalf("expected [5 4 3], got %v", ids)
	}
}
