package feed

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"go-feed-system/internal/user"
)

var errDBError = errors.New("database connection lost")

// 1. Fake Repository
type fakeFeedRepository struct {
	ListFollowingFunc      func(ctx context.Context, followerID uint64, cursor *Cursor, limit int) ([]VideoRecord, error)
	ListVisibleByIDsFunc   func(ctx context.Context, followerID uint64, videoIDs []uint64) ([]VideoRecord, error)
	ListBigAuthorIDsFunc   func(ctx context.Context, followerID uint64, threshold int) ([]uint64, error)
	ListByAuthorIDsFunc    func(ctx context.Context, authorIDs []uint64, cursor *Cursor, limit int) ([]VideoRecord, error)
	CallCount              int
	ListVisibleCallCount   int
	ListBigAuthorCallCount int
	ListByAuthorCallCount  int
	LastFollowerID         uint64
	LastCursor             *Cursor
	LastLimit              int
	LastVisibleFollowerID  uint64
	LastVideoIDs           []uint64
}

func (f *fakeFeedRepository) ListFollowing(ctx context.Context, followerID uint64, cursor *Cursor, limit int) ([]VideoRecord, error) {
	f.CallCount++
	f.LastFollowerID = followerID
	f.LastCursor = cursor
	f.LastLimit = limit
	if f.ListFollowingFunc != nil {
		return f.ListFollowingFunc(ctx, followerID, cursor, limit)
	}
	return nil, nil
}

func (f *fakeFeedRepository) ListVisibleByIDs(ctx context.Context, followerID uint64, videoIDs []uint64) ([]VideoRecord, error) {
	f.ListVisibleCallCount++
	f.LastVisibleFollowerID = followerID
	f.LastVideoIDs = videoIDs
	if f.ListVisibleByIDsFunc != nil {
		return f.ListVisibleByIDsFunc(ctx, followerID, videoIDs)
	}
	return nil, nil
}

func (f *fakeFeedRepository) ListBigAuthorIDs(
	ctx context.Context,
	followerID uint64,
	threshold int,
) ([]uint64, error) {
	f.ListBigAuthorCallCount++
	if f.ListBigAuthorIDsFunc != nil {
		return f.ListBigAuthorIDsFunc(ctx, followerID, threshold)
	}
	return nil, nil
}

func (f *fakeFeedRepository) ListFollowingByAuthorIDs(
	ctx context.Context,
	authorIDs []uint64,
	cursor *Cursor,
	limit int,
) ([]VideoRecord, error) {
	f.ListByAuthorCallCount++
	if f.ListByAuthorIDsFunc != nil {
		return f.ListByAuthorIDsFunc(ctx, authorIDs, cursor, limit)
	}
	return nil, nil
}

// 2. Fake UserReader
type fakeFeedUserReader struct {
	GetByIDsFunc func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
	CallCount    int
	LastIDs      []uint64
}

func (f *fakeFeedUserReader) GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
	f.CallCount++
	f.LastIDs = ids
	if f.GetByIDsFunc != nil {
		return f.GetByIDsFunc(ctx, ids)
	}
	return make(map[uint64]*user.User), nil
}

// 3. Fake LikeReader
type fakeFeedLikeReader struct {
	CountByVideoIDsFunc     func(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error)
	LikedVideoIDsByUserFunc func(ctx context.Context, userID uint64, videoIDs []uint64) (map[uint64]bool, error)
	CountCallCount          int
	LikedCallCount          int
	LastCountVideoIDs       []uint64
	LastLikedUserID         uint64
	LastLikedVideoIDs       []uint64
}

func (f *fakeFeedLikeReader) CountByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error) {
	f.CountCallCount++
	f.LastCountVideoIDs = videoIDs
	if f.CountByVideoIDsFunc != nil {
		return f.CountByVideoIDsFunc(ctx, videoIDs)
	}
	return make(map[uint64]int64), nil
}

func (f *fakeFeedLikeReader) LikedVideoIDsByUser(ctx context.Context, userID uint64, videoIDs []uint64) (map[uint64]bool, error) {
	f.LikedCallCount++
	f.LastLikedUserID = userID
	f.LastLikedVideoIDs = videoIDs
	if f.LikedVideoIDsByUserFunc != nil {
		return f.LikedVideoIDsByUserFunc(ctx, userID, videoIDs)
	}
	return make(map[uint64]bool), nil
}

type fakeFeedInboxReader struct {
	ListFunc  func(ctx context.Context, userID uint64, cursor uint64, limit int) ([]uint64, error)
	CallCount int
}

func (f *fakeFeedInboxReader) List(ctx context.Context, userID uint64, cursor uint64, limit int) ([]uint64, error) {
	f.CallCount++
	if f.ListFunc != nil {
		return f.ListFunc(ctx, userID, cursor, limit)
	}
	return nil, nil
}

// 4. 表驱动测试
func TestService_ListFollowing(t *testing.T) {
	now := time.Now()
	validUserMap := map[uint64]*user.User{
		10: {ID: 10, Username: "user10"},
		11: {ID: 11, Username: "user11"},
	}
	validLikeCountMap := map[uint64]int64{
		100: 5,
		101: 8,
	}
	validLikedMap := map[uint64]bool{
		100: true,
		101: false,
	}

	tests := []struct {
		name     string
		userID   uint64
		cursor   *Cursor
		pageSize int

		mockVideos    []VideoRecord
		mockVideoErr  error
		mockUsers     map[uint64]*user.User
		mockUserErr   error
		mockLikeCount map[uint64]int64
		mockCountErr  error
		mockLiked     map[uint64]bool
		mockLikedErr  error

		wantErr             error
		wantItemsLen        int
		wantHasMore         bool
		wantNextCursorEmpty bool
		wantUserCall        int
		wantCountCall       int
		wantLikedCall       int
	}{
		{
			name:                "空 Feed，不调用用户和点赞查询",
			userID:              1,
			pageSize:            20,
			mockVideos:          nil,
			wantErr:             nil,
			wantItemsLen:        0,
			wantHasMore:         false,
			wantNextCursorEmpty: true,
			wantUserCall:        0,
			wantCountCall:       0,
			wantLikedCall:       0,
		},
		{
			name:     "正常返回，视频顺序保持不变",
			userID:   1,
			pageSize: 5,
			mockVideos: []VideoRecord{
				{ID: 100, AuthorID: 10, Title: "v1", CreatedAt: now},
				{ID: 101, AuthorID: 11, Title: "v2", CreatedAt: now.Add(-time.Hour)},
			},
			mockUsers:           validUserMap,
			mockLikeCount:       validLikeCountMap,
			mockLiked:           validLikedMap,
			wantErr:             nil,
			wantItemsLen:        2,
			wantHasMore:         false,
			wantNextCursorEmpty: true,
			wantUserCall:        1,
			wantCountCall:       1,
			wantLikedCall:       1,
		},
		{
			name:     "HasMore=true 时裁剪多余数据并生成游标",
			userID:   1,
			pageSize: 2,
			mockVideos: []VideoRecord{
				{ID: 100, AuthorID: 10, Title: "v1", CreatedAt: now},
				{ID: 101, AuthorID: 11, Title: "v2", CreatedAt: now.Add(-time.Hour)},
				{ID: 102, AuthorID: 10, Title: "v3", CreatedAt: now.Add(-2 * time.Hour)},
			},
			mockUsers:           validUserMap,
			mockLikeCount:       validLikeCountMap,
			mockLiked:           validLikedMap,
			wantErr:             nil,
			wantItemsLen:        2, // 核心：裁剪掉多余的 1 条
			wantHasMore:         true,
			wantNextCursorEmpty: false, // 核心：生成游标
			wantUserCall:        1,
			wantCountCall:       1,
			wantLikedCall:       1,
		},
		{
			name:     "作者缺失时填充已注销用户，点赞数缺失填充 0",
			userID:   1,
			pageSize: 5,
			mockVideos: []VideoRecord{
				{ID: 100, AuthorID: 10, Title: "v1", CreatedAt: now},
				{ID: 101, AuthorID: 99, Title: "v2", CreatedAt: now}, // 99 号作者不存在
			},
			mockUsers: map[uint64]*user.User{
				10: {ID: 10, Username: "user10"},
			},
			mockLikeCount:       make(map[uint64]int64), // 没有点赞数据
			mockLiked:           make(map[uint64]bool),
			wantErr:             nil,
			wantItemsLen:        2,
			wantHasMore:         false,
			wantNextCursorEmpty: true,
			wantUserCall:        1,
			wantCountCall:       1,
			wantLikedCall:       1,
		},
		{
			name:          "视频查询失败",
			userID:        1,
			pageSize:      5,
			mockVideoErr:  errDBError,
			wantErr:       errDBError,
			wantItemsLen:  0,
			wantUserCall:  0,
			wantCountCall: 0,
			wantLikedCall: 0,
		},
		{
			name:          "用户批量查询失败",
			userID:        1,
			pageSize:      5,
			mockVideos:    []VideoRecord{{ID: 100, AuthorID: 10, CreatedAt: now}},
			mockUserErr:   errDBError,
			wantErr:       errDBError,
			wantItemsLen:  0,
			wantUserCall:  1,
			wantCountCall: 0, // 用户查询失败，不应该继续查点赞
			wantLikedCall: 0,
		},
		{
			name:          "点赞数查询失败",
			userID:        1,
			pageSize:      5,
			mockVideos:    []VideoRecord{{ID: 100, AuthorID: 10, CreatedAt: now}},
			mockUsers:     validUserMap,
			mockCountErr:  errDBError,
			wantErr:       errDBError,
			wantItemsLen:  0,
			wantUserCall:  1,
			wantCountCall: 1,
			wantLikedCall: 0, // 计数失败，不应继续查点赞状态
		},
		{
			name:          "点赞状态查询失败",
			userID:        1,
			pageSize:      5,
			mockVideos:    []VideoRecord{{ID: 100, AuthorID: 10, CreatedAt: now}},
			mockUsers:     validUserMap,
			mockLikeCount: validLikeCountMap,
			mockLikedErr:  errDBError,
			wantErr:       errDBError,
			wantItemsLen:  0,
			wantUserCall:  1,
			wantCountCall: 1,
			wantLikedCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 Fake 依赖
			videoRepo := &fakeFeedRepository{
				ListFollowingFunc: func(ctx context.Context, followerID uint64, cursor *Cursor, limit int) ([]VideoRecord, error) {
					return tt.mockVideos, tt.mockVideoErr
				},
			}
			userReader := &fakeFeedUserReader{
				GetByIDsFunc: func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
					return tt.mockUsers, tt.mockUserErr
				},
			}
			likeReader := &fakeFeedLikeReader{
				CountByVideoIDsFunc: func(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error) {
					return tt.mockLikeCount, tt.mockCountErr
				},
				LikedVideoIDsByUserFunc: func(ctx context.Context, userID uint64, videoIDs []uint64) (map[uint64]bool, error) {
					return tt.mockLiked, tt.mockLikedErr
				},
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			svc := NewService(videoRepo, userReader, likeReader, nil, 1000, logger)
			page, err := svc.ListFollowing(context.Background(), tt.userID, tt.cursor, tt.pageSize)

			// 1. 断言错误
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// 2. 断言结果
			if tt.wantErr == nil {
				if len(page.Items) != tt.wantItemsLen {
					t.Errorf("expected items len %d, got %d", tt.wantItemsLen, len(page.Items))
				}
				if page.HasMore != tt.wantHasMore {
					t.Errorf("expected HasMore %v, got %v", tt.wantHasMore, page.HasMore)
				}
				if (page.NextCursor == "") != tt.wantNextCursorEmpty {
					t.Errorf("expected NextCursor empty %v, got cursor=%q", tt.wantNextCursorEmpty, page.NextCursor)
				}

				// 额外验证：如果作者缺失，是否填充了 "已注销用户"
				for _, item := range page.Items {
					if item.Author.ID == 99 && item.Author.Username != "已注销用户" {
						t.Errorf("expected deleted user fallback, got %q", item.Author.Username)
					}
				}
			}

			// 3. 断言调用次数和参数传递
			if videoRepo.CallCount != 1 && tt.wantErr == nil {
				t.Errorf("expected VideoRepository called once, got %d", videoRepo.CallCount)
			}
			// 核心断言：传入 Repository 的 limit 必须是 pageSize + 1
			if tt.wantItemsLen > 0 && videoRepo.LastLimit != tt.pageSize+1 {
				t.Errorf("expected Repository limit %d, got %d", tt.pageSize+1, videoRepo.LastLimit)
			}

			if userReader.CallCount != tt.wantUserCall {
				t.Errorf("expected UserReader called %d times, got %d", tt.wantUserCall, userReader.CallCount)
			}
			if likeReader.CountCallCount != tt.wantCountCall {
				t.Errorf("expected LikeReader.Count called %d times, got %d", tt.wantCountCall, likeReader.CountCallCount)
			}
			if likeReader.LikedCallCount != tt.wantLikedCall {
				t.Errorf("expected LikeReader.Liked called %d times, got %d", tt.wantLikedCall, likeReader.LikedCallCount)
			}

			// 验证传给 LikeReader 的 userID 是否正确
			if tt.wantLikedCall > 0 && likeReader.LastLikedUserID != tt.userID {
				t.Errorf("expected LikeReader userID %d, got %d", tt.userID, likeReader.LastLikedUserID)
			}
		})
	}
}

func TestService_ListFollowing_MergeInbox(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string

		cursor       *Cursor
		pageSize     int
		mysqlRecords []VideoRecord
		pushIDs      []uint64
		pushRecords  []VideoRecord
		inboxErr     error
		visibleErr   error

		wantIDs            []uint64
		wantHasMore        bool
		wantVisibleCalls   int
		wantFullMySQLCalls int
	}{
		{
			name:     "Redis 补充独立视频并参与排序",
			pageSize: 10,
			mysqlRecords: []VideoRecord{
				{ID: 100, AuthorID: 10, Title: "mysql", CreatedAt: now.Add(-time.Hour)},
			},
			pushIDs: []uint64{200},
			pushRecords: []VideoRecord{
				{ID: 200, AuthorID: 20, Title: "redis", CreatedAt: now},
			},
			wantIDs:            []uint64{200, 100},
			wantVisibleCalls:   1,
			wantFullMySQLCalls: 0,
		},
		{
			name:     "重复视频优先保留 MySQL 数据",
			pageSize: 10,
			mysqlRecords: []VideoRecord{
				{ID: 100, AuthorID: 10, Title: "mysql", CreatedAt: now},
			},
			pushIDs: []uint64{100},
			pushRecords: []VideoRecord{
				{ID: 100, AuthorID: 10, Title: "redis", CreatedAt: now},
			},
			wantIDs:            []uint64{100},
			wantVisibleCalls:   1,
			wantFullMySQLCalls: 0,
		},
		{
			name:     "第二页过滤比游标更新的 Redis 记录",
			cursor:   &Cursor{CreatedAt: now, ID: 150},
			pageSize: 10,
			mysqlRecords: []VideoRecord{
				{ID: 140, AuthorID: 10, CreatedAt: now.Add(-time.Minute)},
			},
			pushIDs: []uint64{200, 130},
			pushRecords: []VideoRecord{
				{ID: 200, AuthorID: 20, CreatedAt: now.Add(time.Minute)},
				{ID: 130, AuthorID: 20, CreatedAt: now.Add(-2 * time.Minute)},
			},
			wantIDs:            []uint64{140, 130},
			wantVisibleCalls:   1,
			wantFullMySQLCalls: 0,
		},
		{
			name:     "Redis 读取失败时降级为 MySQL",
			pageSize: 10,
			mysqlRecords: []VideoRecord{
				{ID: 100, AuthorID: 10, CreatedAt: now},
			},
			inboxErr:           errDBError,
			wantIDs:            []uint64{100},
			wantFullMySQLCalls: 1,
		},
		{
			name:     "Redis 视频元数据查询失败时降级为 MySQL",
			pageSize: 10,
			mysqlRecords: []VideoRecord{
				{ID: 100, AuthorID: 10, CreatedAt: now},
			},
			pushIDs:            []uint64{200},
			visibleErr:         errDBError,
			wantIDs:            []uint64{100},
			wantVisibleCalls:   1,
			wantFullMySQLCalls: 1,
		},
		{
			name:     "合并后仍能正确生成下一页",
			pageSize: 2,
			mysqlRecords: []VideoRecord{
				{ID: 100, AuthorID: 10, CreatedAt: now.Add(-time.Hour)},
				{ID: 90, AuthorID: 10, CreatedAt: now.Add(-2 * time.Hour)},
			},
			pushIDs: []uint64{200},
			pushRecords: []VideoRecord{
				{ID: 200, AuthorID: 20, CreatedAt: now},
			},
			wantIDs:            []uint64{200, 100},
			wantHasMore:        true,
			wantVisibleCalls:   1,
			wantFullMySQLCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeFeedRepository{
				ListFollowingFunc: func(ctx context.Context, followerID uint64, cursor *Cursor, limit int) ([]VideoRecord, error) {
					return tt.mysqlRecords, nil
				},
				ListBigAuthorIDsFunc: func(ctx context.Context, followerID uint64, threshold int) ([]uint64, error) {
					return []uint64{10}, nil
				},
				ListByAuthorIDsFunc: func(ctx context.Context, authorIDs []uint64, cursor *Cursor, limit int) ([]VideoRecord, error) {
					return tt.mysqlRecords, nil
				},
				ListVisibleByIDsFunc: func(ctx context.Context, followerID uint64, videoIDs []uint64) ([]VideoRecord, error) {
					return tt.pushRecords, tt.visibleErr
				},
			}
			inbox := &fakeFeedInboxReader{
				ListFunc: func(ctx context.Context, userID uint64, cursor uint64, limit int) ([]uint64, error) {
					return tt.pushIDs, tt.inboxErr
				},
			}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			svc := NewService(
				repo,
				&fakeFeedUserReader{},
				&fakeFeedLikeReader{},
				inbox,
				1000,
				logger,
			)

			page, err := svc.ListFollowing(context.Background(), 1, tt.cursor, tt.pageSize)
			if err != nil {
				t.Fatalf("list following: %v", err)
			}

			gotIDs := make([]uint64, 0, len(page.Items))
			for _, item := range page.Items {
				gotIDs = append(gotIDs, item.ID)
			}
			if len(gotIDs) != len(tt.wantIDs) {
				t.Fatalf("expected IDs %v, got %v", tt.wantIDs, gotIDs)
			}
			for i := range tt.wantIDs {
				if gotIDs[i] != tt.wantIDs[i] {
					t.Fatalf("expected IDs %v, got %v", tt.wantIDs, gotIDs)
				}
			}
			if page.HasMore != tt.wantHasMore {
				t.Errorf("expected HasMore %t, got %t", tt.wantHasMore, page.HasMore)
			}
			if repo.ListVisibleCallCount != tt.wantVisibleCalls {
				t.Errorf("expected ListVisible calls %d, got %d", tt.wantVisibleCalls, repo.ListVisibleCallCount)
			}
			if repo.CallCount != tt.wantFullMySQLCalls {
				t.Errorf("expected full MySQL fallback calls %d, got %d", tt.wantFullMySQLCalls, repo.CallCount)
			}
			if repo.ListBigAuthorCallCount != 1 {
				t.Errorf("expected one big author query, got %d", repo.ListBigAuthorCallCount)
			}
			if repo.ListByAuthorCallCount != 1 {
				t.Errorf("expected one big author video query, got %d", repo.ListByAuthorCallCount)
			}
		})
	}
}

func TestService_LoadCandidateRecords_MySQLFailures(t *testing.T) {
	tests := []struct {
		name         string
		bigAuthorErr error
		byAuthorErr  error
		wantErr      error
	}{
		{
			name:         "大 V ID 查询失败",
			bigAuthorErr: errDBError,
			wantErr:      errDBError,
		},
		{
			name:        "大 V 视频查询失败",
			byAuthorErr: errDBError,
			wantErr:     errDBError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeFeedRepository{
				ListBigAuthorIDsFunc: func(ctx context.Context, followerID uint64, threshold int) ([]uint64, error) {
					return []uint64{10}, tt.bigAuthorErr
				},
				ListByAuthorIDsFunc: func(ctx context.Context, authorIDs []uint64, cursor *Cursor, limit int) ([]VideoRecord, error) {
					return nil, tt.byAuthorErr
				},
			}
			svc := NewService(
				repo,
				&fakeFeedUserReader{},
				&fakeFeedLikeReader{},
				&fakeFeedInboxReader{},
				1000,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)

			_, err := svc.loadCandidateRecords(context.Background(), 1, nil, 10)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}
