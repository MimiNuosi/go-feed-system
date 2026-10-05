package feed

import (
	"context"
	"os"
	"testing"
	"time"

	"gorm.io/gorm"

	"go-feed-system/internal/interaction"
	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
	"go-feed-system/pkg/database"
)

// 辅助函数：与 interaction 包一致，连接测试库并开启事务
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN is not set")
	}

	db, err := database.Open(context.Background(), database.MySQLConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}

	tx := db.Begin()
	if tx.Error != nil {
		t.Fatalf("begin transaction: %v", tx.Error)
	}

	t.Cleanup(func() {
		tx.Rollback()
		database.Close(db)
	})

	return tx
}

// 辅助函数：准备 Feed 测试需要的种子数据
func createFeedSeedData(t *testing.T, tx *gorm.DB) (userA, userB, userC uint64, video1, video2, video3 uint64) {
	// 1. 创建三个用户
	uA := &user.User{Username: "userA", Email: "userA@example.com", PasswordHash: "hash"}
	uB := &user.User{Username: "userB", Email: "userB@example.com", PasswordHash: "hash"}
	uC := &user.User{Username: "userC", Email: "userC@example.com", PasswordHash: "hash"}
	for _, u := range []*user.User{uA, uB, uC} {
		if err := tx.Create(u).Error; err != nil {
			t.Fatalf("create seed user: %v", err)
		}
	}

	// 2. 设定时间线（v3 最新，v1 最旧，v1 和 v2 时间相同以便测试 ID 排序）
	now := time.Now().Truncate(time.Millisecond) // 截断到毫秒，与 MySQL DATETIME(3) 精度对齐
	timeV3 := now
	timeV1 := now.Add(-1 * time.Hour)
	timeV2 := timeV1 // 故意让 v1 和 v2 时间完全相同

	// 3. 创建视频
	v1 := &video.Video{
		AuthorID: uB.ID, Title: "Video B1", Description: "desc",
		StorageKey: "test/v1", OriginalFilename: "v1.mp4", ContentType: "video/mp4",
		SizeBytes: 100, Status: "ready", CreatedAt: timeV1,
	}
	v2 := &video.Video{
		AuthorID: uB.ID, Title: "Video B2", Description: "desc",
		StorageKey: "test/v2", OriginalFilename: "v2.mp4", ContentType: "video/mp4",
		SizeBytes: 100, Status: "ready", CreatedAt: timeV2,
	}
	v3 := &video.Video{
		AuthorID: uC.ID, Title: "Video C", Description: "desc",
		StorageKey: "test/v3", OriginalFilename: "v3.mp4", ContentType: "video/mp4",
		SizeBytes: 100, Status: "ready", CreatedAt: timeV3,
	}
	for _, v := range []*video.Video{v1, v2, v3} {
		if err := tx.Create(v).Error; err != nil {
			t.Fatalf("create seed video: %v", err)
		}
	}

	// 4. 建立关注关系：A 关注 B，A 不关注 C
	follow := &interaction.Follow{FollowerID: uA.ID, FolloweeID: uB.ID}
	if err := tx.Create(follow).Error; err != nil {
		t.Fatalf("create seed follow: %v", err)
	}

	return uA.ID, uB.ID, uC.ID, v1.ID, v2.ID, v3.ID
}

func TestGORMRepository_ListFollowing(t *testing.T) {
	t.Run("第一页：只返回关注作者的视频，按时间与 ID 稳定降序", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		uA, _, _, _, v2ID, _ := createFeedSeedData(t, tx)

		// 第一页：cursor 为 nil，不传游标条件
		records, err := repo.ListFollowing(context.Background(), uA, nil, 10)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// 核心断言 1：只返回 A 关注的 B 的视频，不返回 C 的
		if len(records) != 2 {
			t.Fatalf("expected 2 videos from B, got %d", len(records))
		}

		// 核心断言 2：v1 和 v2 时间相同，v2 的 ID 更大，因此排在前面
		if records[0].ID != v2ID {
			t.Errorf("expected first video ID %d, got %d", v2ID, records[0].ID)
		}
	})

	t.Run("第二页：使用复合游标，不重复、不漏数据", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		uA, _, _, v1ID, v2ID, _ := createFeedSeedData(t, tx)

		// 第一页只取 1 条，这样游标就会停在 v2 上
		page1, err := repo.ListFollowing(context.Background(), uA, nil, 1)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(page1) != 1 || page1[0].ID != v2ID {
			t.Fatalf("expected page1 to contain v2 (ID=%d)", v2ID)
		}

		// 构造游标：时间与 ID 需要和 v2 保持一致
		cursor := &Cursor{
			CreatedAt: page1[0].CreatedAt,
			ID:        page1[0].ID,
		}

		// 第二页：带上游标
		page2, err := repo.ListFollowing(context.Background(), uA, cursor, 10)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// 断言：第二页应该只有 v1，且绝不能重复出现 v2
		if len(page2) != 1 {
			t.Fatalf("expected page2 to have 1 video, got %d", len(page2))
		}
		if page2[0].ID != v1ID {
			t.Errorf("expected page2 to contain v1 (ID=%d), got %d", v1ID, page2[0].ID)
		}
		if page2[0].ID == v2ID {
			t.Error("page2 duplicated v2, composite cursor failed")
		}
	})

	t.Run("空 Feed：没有关注任何人时返回空列表", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		// 创建一个没有关注任何人的用户 D
		uD := &user.User{Username: "userD", Email: "userD@example.com", PasswordHash: "hash"}
		tx.Create(uD)

		records, err := repo.ListFollowing(context.Background(), uD.ID, nil, 10)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(records) != 0 {
			t.Errorf("expected empty feed, got %d videos", len(records))
		}
	})
}

func TestGORMRepository_ListVisibleByIDs(t *testing.T) {
	t.Run("只返回当前仍关注的作者视频", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		userA, _, _, _, videoB, videoC := createFeedSeedData(t, tx)

		records, err := repo.ListVisibleByIDs(
			context.Background(),
			userA,
			[]uint64{videoB, videoC},
		)
		if err != nil {
			t.Fatalf("list visible videos: %v", err)
		}
		if len(records) != 1 {
			t.Fatalf("expected one visible video, got %d", len(records))
		}
		if records[0].ID != videoB {
			t.Errorf("expected video %d, got %d", videoB, records[0].ID)
		}
	})

	t.Run("空 ID 切片直接返回空结果", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		records, err := repo.ListVisibleByIDs(context.Background(), 1, nil)
		if err != nil {
			t.Fatalf("list visible videos: %v", err)
		}
		if len(records) != 0 {
			t.Fatalf("expected empty records, got %v", records)
		}
	})
}
