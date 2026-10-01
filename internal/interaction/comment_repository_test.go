package interaction

import (
	"context"
	"errors"
	"testing"
)

func TestGORMCommentRepository(t *testing.T) {
	t.Run("创建并查询评论", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMCommentRepository(tx)
		userID, videoID := createSeedData(t, tx) // 确保这个辅助函数能拿到真实的 userID 和 videoID

		comment := &Comment{
			VideoID: videoID,
			UserID:  userID,
			Content: "这是一条测试评论",
		}

		// 1. 创建
		if err := repo.Create(context.Background(), comment); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if comment.ID == 0 {
			t.Fatal("expected ID backfilled")
		}

		// 2. 查询
		found, err := repo.FindByID(context.Background(), comment.ID)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if found.Content != "这是一条测试评论" {
			t.Errorf("expected content match, got %s", found.Content)
		}
	})

	t.Run("查询不存在的评论，返回 ErrInvalidTarget", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMCommentRepository(tx)

		_, err := repo.FindByID(context.Background(), 999999)
		if !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("expected ErrInvalidTarget, got %v", err)
		}
	})

	t.Run("游标分页：首页与第二页", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMCommentRepository(tx)
		userID, videoID := createSeedData(t, tx)

		// 插入 5 条评论
		var commentIDs []uint64
		for i := 1; i <= 5; i++ {
			c := &Comment{VideoID: videoID, UserID: userID, Content: "评论"}
			if err := repo.Create(context.Background(), c); err != nil {
				t.Fatalf("failed to create comment %d: %v", i, err)
			}
			commentIDs = append(commentIDs, c.ID)
		}

		// 模拟 Service 传 pageSize+1 (这里取 3+1=4)
		// 1. 首页查询
		page1, err := repo.ListByVideoID(context.Background(), videoID, 0, 4)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(page1) != 4 {
			t.Errorf("expected 4 comments, got %d", len(page1))
		}
		for i := 0; i < len(page1)-1; i++ {
			if page1[i].ID <= page1[i+1].ID {
				t.Fatalf("expected page1 in descending ID order, got %d then %d", page1[i].ID, page1[i+1].ID)
			}
		}

		// 2. 第二页查询（用第一页最后一条的 ID 作为游标）
		lastID := page1[len(page1)-1].ID
		page2, err := repo.ListByVideoID(context.Background(), videoID, lastID, 4)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(page2) != 1 {
			t.Errorf("expected 1 comment on page 2, got %d", len(page2))
		}
		if page2[0].ID >= lastID {
			t.Error("expected page2 items to be older (smaller ID) than lastID")
		}
	})

	t.Run("软删除与幂等删除", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMCommentRepository(tx)
		userID, videoID := createSeedData(t, tx)

		c := &Comment{VideoID: videoID, UserID: userID, Content: "待删除"}
		if err := repo.Create(context.Background(), c); err != nil {
			t.Fatalf("prepare seed comment failed: %v", err)
		}

		// 1. 删除评论
		err := repo.Delete(context.Background(), c.ID, userID)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		// 2. 验证软删除：查询不到
		_, err = repo.FindByID(context.Background(), c.ID)
		if !errors.Is(err, ErrInvalidTarget) {
			t.Fatalf("expected ErrInvalidTarget after soft delete, got %v", err)
		}

		comments, err := repo.ListByVideoID(context.Background(), videoID, 0, 10)
		if err != nil {
			t.Fatalf("list comments after soft delete: %v", err)
		}
		if len(comments) != 0 {
			t.Fatalf("expected soft-deleted comment to be excluded, got %d comments", len(comments))
		}

		// 3. 重复删除（幂等）
		err = repo.Delete(context.Background(), c.ID, userID)
		if err != nil {
			t.Fatalf("expected idempotent delete, got %v", err)
		}
	})

	t.Run("使用错误作者 userID 不会删除评论", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMCommentRepository(tx)
		userID, videoID := createSeedData(t, tx)

		c := &Comment{VideoID: videoID, UserID: userID, Content: "评论"}
		if err := repo.Create(context.Background(), c); err != nil {
			t.Fatalf("prepare seed comment failed: %v", err)
		}

		// 用一个不匹配的 userID 尝试删除
		err := repo.Delete(context.Background(), c.ID, userID+1)
		if err != nil {
			t.Fatalf("expected no error (idempotent), got %v", err)
		}

		// 验证评论依然存在
		found, err := repo.FindByID(context.Background(), c.ID)
		if err != nil {
			t.Fatalf("expected comment still exist, got %v", err)
		}
		if found.ID != c.ID {
			t.Error("expected comment not deleted")
		}
	})
}
