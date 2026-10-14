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

func createBigAuthorSeedData(
	t *testing.T,
	tx *gorm.DB,
) (viewerID, bigAuthorID, smallAuthorID, bigVideoNew, bigVideoOld, smallVideo uint64) {
	t.Helper()

	viewer := &user.User{Username: "viewer", Email: "viewer@example.com", PasswordHash: "hash"}
	bigAuthor := &user.User{Username: "bigAuthor", Email: "big@example.com", PasswordHash: "hash"}
	smallAuthor := &user.User{Username: "smallAuthor", Email: "small@example.com", PasswordHash: "hash"}
	fan := &user.User{Username: "fan", Email: "fan@example.com", PasswordHash: "hash"}
	for _, u := range []*user.User{viewer, bigAuthor, smallAuthor, fan} {
		if err := tx.Create(u).Error; err != nil {
			t.Fatalf("create seed user: %v", err)
		}
	}

	follows := []*interaction.Follow{
		{FollowerID: viewer.ID, FolloweeID: bigAuthor.ID},
		{FollowerID: viewer.ID, FolloweeID: smallAuthor.ID},
		{FollowerID: fan.ID, FolloweeID: bigAuthor.ID},
	}
	for _, follow := range follows {
		if err := tx.Create(follow).Error; err != nil {
			t.Fatalf("create seed follow: %v", err)
		}
	}

	now := time.Now().Truncate(time.Millisecond)
	bigNew := &video.Video{
		AuthorID: bigAuthor.ID, Title: "big new", Description: "desc",
		StorageKey: "test/big-new", OriginalFilename: "big-new.mp4", ContentType: "video/mp4",
		SizeBytes: 100, Status: "ready", CreatedAt: now,
	}
	bigOld := &video.Video{
		AuthorID: bigAuthor.ID, Title: "big old", Description: "desc",
		StorageKey: "test/big-old", OriginalFilename: "big-old.mp4", ContentType: "video/mp4",
		SizeBytes: 100, Status: "ready", CreatedAt: now.Add(-time.Hour),
	}
	small := &video.Video{
		AuthorID: smallAuthor.ID, Title: "small", Description: "desc",
		StorageKey: "test/small", OriginalFilename: "small.mp4", ContentType: "video/mp4",
		SizeBytes: 100, Status: "ready", CreatedAt: now.Add(time.Minute),
	}
	for _, item := range []*video.Video{bigNew, bigOld, small} {
		if err := tx.Create(item).Error; err != nil {
			t.Fatalf("create seed video: %v", err)
		}
	}

	return viewer.ID, bigAuthor.ID, smallAuthor.ID, bigNew.ID, bigOld.ID, small.ID
}

// TestGORMRepository_ListLatest 验证"最新"频道：不依赖关注关系。
//
// 种子里 userA 只关注了 userB，但 userC 的视频也应该出现在最新里 ——
// 这正是"陌生人发现新内容"的场景（也是 666 能不能看到 333 的关键）。
func TestGORMRepository_ListLatest(t *testing.T) {
	tx := openTestDB(t)
	_, userB, userC, video1, video2, video3 := createFeedSeedData(t, tx)
	repository := NewGORMRepository(tx)
	ctx := context.Background()

	records, err := repository.ListLatest(ctx, nil, 10)
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}

	// 注意："最新"是全站的，库里本来就有别的视频（真实数据、其它用例留下的），
	// 所以这里只检查种子视频之间的相对顺序，不能假设结果里只有它们。
	seedIDs := map[uint64]bool{video1: true, video2: true, video3: true}
	seedOrder := make([]uint64, 0, 3)
	authorSet := make(map[uint64]bool)
	for _, r := range records {
		if seedIDs[r.ID] {
			seedOrder = append(seedOrder, r.ID)
			authorSet[r.AuthorID] = true
		}
	}

	// v3 最新；v1 与 v2 时间戳相同，靠 id 兜住顺序（id 大的在前）
	want := []uint64{video3, video2, video1}
	if len(seedOrder) != len(want) {
		t.Fatalf("expected %d seed videos, got %v", len(want), seedOrder)
	}
	for i := range want {
		if seedOrder[i] != want[i] {
			t.Fatalf("expected seed order %v, got %v", want, seedOrder)
		}
	}

	// 跨作者：关注关系不影响最新
	if !authorSet[userB] || !authorSet[userC] {
		t.Fatalf("expected videos from both authors, got %v", authorSet)
	}

	// 逐条翻页：同时间戳的两条不能被漏掉或重复
	var walked []uint64
	var cursor *Cursor
	for i := 0; i < 50; i++ {
		page, err := repository.ListLatest(ctx, cursor, 1)
		if err != nil {
			t.Fatalf("walk page %d: %v", i, err)
		}
		if len(page) == 0 {
			break
		}
		if seedIDs[page[0].ID] {
			walked = append(walked, page[0].ID)
		}
		cursor = &Cursor{CreatedAt: page[0].CreatedAt, ID: page[0].ID}
	}
	if len(walked) != 3 || walked[0] != video3 || walked[1] != video2 || walked[2] != video1 {
		t.Fatalf("expected the same order when paging one by one, got %v", walked)
	}

	// 走到底之后没有更多
	empty, err := repository.ListLatest(ctx, cursor, 1)
	if err != nil {
		t.Fatalf("list after the end: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected no more records, got %v", empty)
	}
}

// TestGORMRepository_ListLatest_ExcludesNotReady 验证只有可播放的视频进最新。
func TestGORMRepository_ListLatest_ExcludesNotReady(t *testing.T) {
	tx := openTestDB(t)
	userA, _, _, video1, video2, video3 := createFeedSeedData(t, tx)

	pending := &video.Video{
		AuthorID:         userA,
		Title:            "pending video",
		StorageKey:       "test/pending",
		OriginalFilename: "pending.mp4",
		ContentType:      "video/mp4",
		SizeBytes:        10,
		Status:           "pending",
		CreatedAt:        time.Now().Truncate(time.Millisecond),
	}
	if err := tx.Create(pending).Error; err != nil {
		t.Fatalf("create pending video: %v", err)
	}

	repository := NewGORMRepository(tx)
	records, err := repository.ListLatest(context.Background(), nil, 10)
	if err != nil {
		t.Fatalf("list latest: %v", err)
	}

	for _, r := range records {
		if r.ID == pending.ID {
			t.Fatal("a non-ready video must not appear in the latest feed")
		}
	}

	// 种子里的三条 ready 视频依然应该在
	found := map[uint64]bool{}
	for _, r := range records {
		found[r.ID] = true
	}
	if !found[video1] || !found[video2] || !found[video3] {
		t.Fatal("expected the ready seed videos to remain in the latest feed")
	}
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

func TestGORMRepository_ListBigAuthorIDs(t *testing.T) {
	t.Run("只返回当前用户关注且达到阈值的大 V", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		viewerID, bigAuthorID, _, _, _, _ := createBigAuthorSeedData(t, tx)

		authorIDs, err := repo.ListBigAuthorIDs(context.Background(), viewerID, 2)
		if err != nil {
			t.Fatalf("list big author IDs: %v", err)
		}
		if len(authorIDs) != 1 || authorIDs[0] != bigAuthorID {
			t.Fatalf("expected big author %d, got %v", bigAuthorID, authorIDs)
		}
	})

	t.Run("阈值为一返回所有被关注作者", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		viewerID, bigAuthorID, smallAuthorID, _, _, _ := createBigAuthorSeedData(t, tx)

		authorIDs, err := repo.ListBigAuthorIDs(context.Background(), viewerID, 1)
		if err != nil {
			t.Fatalf("list big author IDs: %v", err)
		}
		if len(authorIDs) != 2 {
			t.Fatalf("expected two authors, got %v", authorIDs)
		}
		seen := map[uint64]bool{}
		for _, id := range authorIDs {
			seen[id] = true
		}
		if !seen[bigAuthorID] || !seen[smallAuthorID] {
			t.Fatalf("expected authors %d and %d, got %v", bigAuthorID, smallAuthorID, authorIDs)
		}
	})
}

func TestGORMRepository_ListFollowingByAuthorIDs(t *testing.T) {
	t.Run("按作者查询并保持复合游标分页", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		_, bigAuthorID, smallAuthorID, bigNewID, bigOldID, _ := createBigAuthorSeedData(t, tx)

		page1, err := repo.ListFollowingByAuthorIDs(context.Background(), []uint64{bigAuthorID}, nil, 1)
		if err != nil {
			t.Fatalf("page1: %v", err)
		}
		if len(page1) != 1 || page1[0].ID != bigNewID {
			t.Fatalf("expected page1 video %d, got %v", bigNewID, page1)
		}

		page2, err := repo.ListFollowingByAuthorIDs(
			context.Background(),
			[]uint64{bigAuthorID},
			&Cursor{CreatedAt: page1[0].CreatedAt, ID: page1[0].ID},
			10,
		)
		if err != nil {
			t.Fatalf("page2: %v", err)
		}
		if len(page2) != 1 || page2[0].ID != bigOldID {
			t.Fatalf("expected page2 video %d, got %v", bigOldID, page2)
		}

		smallRecords, err := repo.ListFollowingByAuthorIDs(
			context.Background(),
			[]uint64{smallAuthorID},
			nil,
			10,
		)
		if err != nil {
			t.Fatalf("list small author videos: %v", err)
		}
		if len(smallRecords) != 1 || smallRecords[0].AuthorID != smallAuthorID {
			t.Fatalf("expected small author video, got %v", smallRecords)
		}
	})

	t.Run("空作者列表返回空结果", func(t *testing.T) {
		tx := openTestDB(t)
		repo := NewGORMRepository(tx)

		records, err := repo.ListFollowingByAuthorIDs(context.Background(), nil, nil, 10)
		if err != nil {
			t.Fatalf("list following by authors: %v", err)
		}
		if len(records) != 0 {
			t.Fatalf("expected empty records, got %v", records)
		}
	})
}
