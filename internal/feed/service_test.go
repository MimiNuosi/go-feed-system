package feed

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-feed-system/internal/user"
)

var errDBError = errors.New("database connection lost")

// 1. Fake Repository
type fakeFeedRepository struct {
	ListFollowingFunc func(ctx context.Context, followerID uint64, cursor *Cursor, limit int) ([]VideoRecord, error)
	CallCount         int
	LastFollowerID    uint64
	LastCursor        *Cursor
	LastLimit         int
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

			svc := NewService(videoRepo, userReader, likeReader)
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
