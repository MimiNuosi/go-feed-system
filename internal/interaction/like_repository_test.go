package interaction

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go-feed-system/internal/user"
	"go-feed-system/internal/video"

	"gorm.io/gorm"
)

// 辅助函数：为当前子测试创建种子用户和种子视频
// 返回 userID, videoID，如果创建失败直接 t.Fatal 终止测试
func createSeedData(t *testing.T, db *gorm.DB) (uint64, uint64) {
	u := &user.User{
		Username:     "like_user",
		Email:        "like_user@example.com",
		PasswordHash: "hash",
	}
	if err := db.Create(u).Error; err != nil {
		t.Fatalf("failed to create seed user: %v", err)
	}

	v := &video.Video{
		AuthorID:         u.ID,
		Title:            "Test Video",
		Description:      "Test Description",
		StorageKey:       "test/storage/key",
		OriginalFilename: "test.mp4",
		ContentType:      "video/mp4",
		SizeBytes:        1024,
		Status:           "ready",
	}
	if err := db.Create(v).Error; err != nil {
		t.Fatalf("failed to create seed video: %v", err)
	}

	return u.ID, v.ID
}

func TestGORMLikeRepository(t *testing.T) {
	// 我们保持和 FollowRepository 一样的模式：每个 t.Run 独立开启事务

	t.Run("成功点赞", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMLikeRepository(tx)
		userID, videoID := createSeedData(t, tx)

		err := repo.Create(context.Background(), userID, videoID)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("重复点赞，返回 ErrAlreadyLiked", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMLikeRepository(tx)
		userID, videoID := createSeedData(t, tx)

		// 先创建一个点赞记录
		if err := repo.Create(context.Background(), userID, videoID); err != nil {
			t.Fatalf("prepare seed like failed: %v", err)
		}

		// 尝试重复点赞
		err := repo.Create(context.Background(), userID, videoID)
		if !errors.Is(err, ErrAlreadyLiked) {
			t.Fatalf("expected ErrAlreadyLiked, got %v", err)
		}
	})

	t.Run("成功取消点赞", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMLikeRepository(tx)
		userID, videoID := createSeedData(t, tx)

		// 先创建一个点赞记录
		if err := repo.Create(context.Background(), userID, videoID); err != nil {
			t.Fatalf("prepare seed like failed: %v", err)
		}

		// 取消点赞
		err := repo.Delete(context.Background(), userID, videoID)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	t.Run("取消未点赞的视频，应幂等成功", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMLikeRepository(tx)
		userID, videoID := createSeedData(t, tx)

		// 直接取消不存在的点赞记录
		err := repo.Delete(context.Background(), userID, videoID)
		if err != nil {
			t.Fatalf("expected no error (idempotent delete), got %v", err)
		}
	})

	t.Run("检查点赞状态", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMLikeRepository(tx)
		userID, videoID := createSeedData(t, tx)

		// 1. 未点赞时，应为 false
		exists, err := repo.Exists(context.Background(), userID, videoID)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if exists {
			t.Error("expected not liked, got liked")
		}

		// 2. 创建点赞后，应为 true
		if err := repo.Create(context.Background(), userID, videoID); err != nil {
			t.Fatalf("prepare seed like failed: %v", err)
		}
		exists, err = repo.Exists(context.Background(), userID, videoID)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !exists {
			t.Error("expected liked, got not liked")
		}
	})

	t.Run("统计视频点赞数", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMLikeRepository(tx)

		// 创建一个作者和一个视频
		_, videoID := createSeedData(t, tx)

		// 创建三个不同的用户，都对同一个视频点赞
		for i := 1; i <= 3; i++ {
			u := &user.User{
				Username:     fmt.Sprintf("like_user_%d", i),
				Email:        fmt.Sprintf("like_user_%d", i) + "@example.com",
				PasswordHash: "hash",
			}
			if err := tx.Create(u).Error; err != nil {
				t.Fatalf("create user err : %v", err)
			}

			if err := repo.Create(context.Background(), u.ID, videoID); err != nil {
				t.Fatalf("failed to create like for user %d: %v", i, err)
			}
		}

		// 统计点赞数，预期为 3
		count, err := repo.CountByVideoID(context.Background(), videoID)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if count != 3 {
			t.Errorf("expected 3 likes, got %d", count)
		}
	})

	t.Run("视频被删除后点赞，返回 ErrInvalidTarget", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMLikeRepository(tx)
		userID, videoID := createSeedData(t, tx)

		// 物理删除视频，制造外键孤儿
		if err := tx.Exec("DELETE FROM videos WHERE id = ?", videoID).Error; err != nil {
			t.Fatalf("failed to delete video: %v", err)
		}

		// 尝试点赞一个已经不存在的视频
		err := repo.Create(context.Background(), userID, videoID)

		// 断言：必须转换成业务错误 ErrInvalidTarget，而不是直接抛底层 1452 错误
		if !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("expected ErrInvalidTarget, got %v", err)
		}
	})
}
