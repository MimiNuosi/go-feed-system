package interaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
)

// 1. Fake CommentRepository
type fakeCommentRepository struct {
	CreateFunc        func(ctx context.Context, comment *Comment) error
	FindByIDFunc      func(ctx context.Context, id uint64) (*Comment, error)
	ListByVideoIDFunc func(ctx context.Context, videoID, lastID uint64, limit int) ([]Comment, error)
	DeleteFunc        func(ctx context.Context, commentID, userID uint64) error

	LastUserID          uint64
	LastVideoID         uint64
	LastContent         string
	LastDeleteCommentID uint64
	LastDeleteUserID    uint64
	LastListLimit       int

	CreateCallCount int
	DeleteCallCount int
	ListCallCount   int
}

func (f *fakeCommentRepository) Create(ctx context.Context, comment *Comment) error {
	f.CreateCallCount++
	f.LastUserID = comment.UserID
	f.LastVideoID = comment.VideoID
	f.LastContent = comment.Content
	if f.CreateFunc != nil {
		return f.CreateFunc(ctx, comment)
	}
	return nil
}

func (f *fakeCommentRepository) FindByID(ctx context.Context, id uint64) (*Comment, error) {
	if f.FindByIDFunc != nil {
		return f.FindByIDFunc(ctx, id)
	}
	return nil, ErrInvalidTarget
}

func (f *fakeCommentRepository) ListByVideoID(ctx context.Context, videoID, lastID uint64, limit int) ([]Comment, error) {
	f.ListCallCount++
	f.LastVideoID = videoID
	f.LastListLimit = limit
	if f.ListByVideoIDFunc != nil {
		return f.ListByVideoIDFunc(ctx, videoID, lastID, limit)
	}
	return []Comment{}, nil
}

func (f *fakeCommentRepository) Delete(ctx context.Context, commentID, userID uint64) error {
	f.DeleteCallCount++
	f.LastDeleteCommentID = commentID
	f.LastDeleteUserID = userID
	if f.DeleteFunc != nil {
		return f.DeleteFunc(ctx, commentID, userID)
	}
	return nil
}

// 2. Fake CommentUserReader
type fakeCommentUserReader struct {
	GetByIDsFunc func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error)
	CallCount    int
	LastIDs      []uint64
}

func (f *fakeCommentUserReader) GetByIDs(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
	f.CallCount++
	f.LastIDs = ids
	if f.GetByIDsFunc != nil {
		return f.GetByIDsFunc(ctx, ids)
	}
	return make(map[uint64]*user.User), nil
}

// 3. 测试 Create 方法
func TestCommentService_Create(t *testing.T) {
	validContent := "这是一条测试评论"

	tests := []struct {
		name           string
		userID         uint64
		videoID        uint64
		content        string
		mockVideoErr   error
		mockCreateErr  error
		wantErr        error
		wantVideoCall  int
		wantCreateCall int
		wantContent    string
	}{
		{
			name:           "成功创建评论",
			userID:         1,
			videoID:        1,
			content:        "  " + validContent + "  ",
			wantErr:        nil,
			wantVideoCall:  1,
			wantCreateCall: 1,
			wantContent:    validContent,
		},
		{
			name:           "userID 为 0，返回 ErrInvalidInput",
			userID:         0,
			videoID:        1,
			content:        validContent,
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0,
			wantCreateCall: 0,
		},
		{
			name:           "videoID 为 0，返回 ErrInvalidInput",
			userID:         1,
			videoID:        0,
			content:        validContent,
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0,
			wantCreateCall: 0,
		},
		{
			name:           "内容为空（空格），返回 ErrInvalidInput",
			userID:         1,
			videoID:        1,
			content:        "   ",
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0,
			wantCreateCall: 0,
		},
		{
			name:           "内容超长，返回 ErrInvalidInput",
			userID:         1,
			videoID:        1,
			content:        string(make([]rune, MaxCommentLength+1)), // 生成超长字符串
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0,
			wantCreateCall: 0,
		},
		{
			name:           "视频不存在，返回 ErrInvalidTarget",
			userID:         1,
			videoID:        999,
			content:        validContent,
			mockVideoErr:   video.ErrNotFound, // 注意：这里需要 import "go-feed-system/internal/video"
			wantErr:        ErrInvalidTarget,
			wantVideoCall:  1,
			wantCreateCall: 0,
		},
		{
			name:           "创建发生数据库错误",
			userID:         1,
			videoID:        1,
			content:        validContent,
			mockCreateErr:  errDBError,
			wantErr:        errDBError,
			wantVideoCall:  1,
			wantCreateCall: 1,
			wantContent:    validContent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoReader := &fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return nil, tt.mockVideoErr
				},
			}
			commentRepo := &fakeCommentRepository{
				CreateFunc: func(ctx context.Context, comment *Comment) error {
					return tt.mockCreateErr
				},
			}

			svc := NewCommentService(commentRepo, videoReader, nil)
			_, err := svc.Create(context.Background(), tt.userID, tt.videoID, tt.content)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if videoReader.CallCount != tt.wantVideoCall {
				t.Errorf("expected VideoReader.GetByID called %d times, got %d", tt.wantVideoCall, videoReader.CallCount)
			}
			if commentRepo.CreateCallCount != tt.wantCreateCall {
				t.Errorf("expected CommentRepository.Create called %d times, got %d", tt.wantCreateCall, commentRepo.CreateCallCount)
			}
			if tt.wantCreateCall > 0 && commentRepo.LastContent != tt.wantContent {
				t.Errorf("expected content %q, got %q", tt.wantContent, commentRepo.LastContent)
			}
		})
	}
}

// 4. 测试 List 方法
func TestCommentService_List(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name             string
		videoID          uint64
		cursor           uint64
		pageSize         int
		mockVideoErr     error
		mockListComments []Comment
		mockListErr      error
		mockUsers        map[uint64]*user.User
		mockUsersErr     error

		wantErr        error
		wantItemsLen   int
		wantHasMore    bool
		wantNextCursor string
		wantVideoCall  int
		wantListCall   int
		wantListLimit  int
		wantUsersCall  int
		wantUsernames  []string
	}{
		{
			name:     "首页正常返回，无下一页",
			videoID:  1,
			pageSize: 2,
			mockListComments: []Comment{
				{ID: 1, VideoID: 1, UserID: 10, Content: "c1", CreatedAt: now},
				{ID: 2, VideoID: 1, UserID: 11, Content: "c2", CreatedAt: now},
			},
			mockUsers: map[uint64]*user.User{
				10: {ID: 10, Username: "user10"},
				11: {ID: 11, Username: "user11"},
			},
			wantErr:        nil,
			wantItemsLen:   2,
			wantHasMore:    false,
			wantNextCursor: "",
			wantVideoCall:  1,
			wantListCall:   1,
			wantListLimit:  3,
			wantUsersCall:  1,
			wantUsernames:  []string{"user10", "user11"},
		},
		{
			name:     "存在下一页，返回 NextCursor 并截断多余数据",
			videoID:  1,
			pageSize: 2,
			mockListComments: []Comment{
				{ID: 1, VideoID: 1, UserID: 10, Content: "c1", CreatedAt: now},
				{ID: 2, VideoID: 1, UserID: 11, Content: "c2", CreatedAt: now},
				{ID: 3, VideoID: 1, UserID: 12, Content: "c3", CreatedAt: now}, // 多出的一条
			},
			mockUsers: map[uint64]*user.User{
				10: {ID: 10, Username: "user10"},
				11: {ID: 11, Username: "user11"},
			},
			wantErr:        nil,
			wantItemsLen:   2,
			wantHasMore:    true,
			wantNextCursor: "2", // 应该等于最后一条有效评论的 ID
			wantVideoCall:  1,
			wantListCall:   1,
			wantListLimit:  3,
			wantUsersCall:  1,
			wantUsernames:  []string{"user10", "user11"},
		},
		{
			name:             "空列表不调用 GetByIDs",
			videoID:          1,
			pageSize:         20,
			mockListComments: []Comment{}, // 空列表
			wantErr:          nil,
			wantItemsLen:     0,
			wantHasMore:      false,
			wantNextCursor:   "",
			wantVideoCall:    1,
			wantListCall:     1,
			wantListLimit:    21,
			wantUsersCall:    0, // 核心断言：空列表不应去批量查用户
		},
		{
			name:             "pageSize 为 0 时使用默认值 20",
			videoID:          1,
			pageSize:         0,
			mockListComments: []Comment{},
			wantErr:          nil,
			wantItemsLen:     0,
			wantHasMore:      false,
			wantNextCursor:   "",
			wantVideoCall:    1,
			wantListCall:     1,
			wantListLimit:    21,
			wantUsersCall:    0,
		},
		{
			name:             "pageSize 超过最大值时截断为 100",
			videoID:          1,
			pageSize:         500,
			mockListComments: []Comment{},
			wantErr:          nil,
			wantItemsLen:     0,
			wantHasMore:      false,
			wantNextCursor:   "",
			wantVideoCall:    1,
			wantListCall:     1,
			wantListLimit:    101,
			wantUsersCall:    0,
		},
		{
			name:     "用户缺失时填充‘已注销用户’",
			videoID:  1,
			pageSize: 20,
			mockListComments: []Comment{
				{ID: 1, VideoID: 1, UserID: 10, Content: "c1", CreatedAt: now},
				{ID: 2, VideoID: 1, UserID: 99, Content: "c2", CreatedAt: now}, // 99 号用户缺失
			},
			mockUsers: map[uint64]*user.User{
				10: {ID: 10, Username: "user10"},
			},
			wantErr:        nil,
			wantItemsLen:   2,
			wantHasMore:    false,
			wantNextCursor: "",
			wantVideoCall:  1,
			wantListCall:   1,
			wantListLimit:  21,
			wantUsersCall:  1,
			wantUsernames:  []string{"user10", "已注销用户"},
		},
		{
			name:          "视频不存在，返回 ErrInvalidTarget",
			videoID:       999,
			pageSize:      20,
			mockVideoErr:  video.ErrNotFound,
			wantErr:       ErrInvalidTarget,
			wantVideoCall: 1,
			wantListCall:  0,
			wantUsersCall: 0,
		},
		{
			name:     "用户批量查询失败",
			videoID:  1,
			pageSize: 20,
			mockListComments: []Comment{
				{ID: 1, VideoID: 1, UserID: 10, Content: "c1", CreatedAt: now},
			},
			mockUsersErr:  errDBError,
			wantErr:       errDBError,
			wantVideoCall: 1,
			wantListCall:  1,
			wantListLimit: 21,
			wantUsersCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoReader := &fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return nil, tt.mockVideoErr
				},
			}
			commentRepo := &fakeCommentRepository{
				ListByVideoIDFunc: func(ctx context.Context, videoID, lastID uint64, limit int) ([]Comment, error) {
					return tt.mockListComments, tt.mockListErr
				},
			}
			userReader := &fakeCommentUserReader{
				GetByIDsFunc: func(ctx context.Context, ids []uint64) (map[uint64]*user.User, error) {
					return tt.mockUsers, tt.mockUsersErr
				},
			}

			svc := NewCommentService(commentRepo, videoReader, userReader)
			page, err := svc.List(context.Background(), tt.videoID, tt.cursor, tt.pageSize)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				if len(page.Items) != tt.wantItemsLen {
					t.Errorf("expected items len %d, got %d", tt.wantItemsLen, len(page.Items))
				}
				if page.HasMore != tt.wantHasMore {
					t.Errorf("expected HasMore %v, got %v", tt.wantHasMore, page.HasMore)
				}
				if page.NextCursor != tt.wantNextCursor {
					t.Errorf("expected NextCursor %q, got %q", tt.wantNextCursor, page.NextCursor)
				}
				for i, wantUsername := range tt.wantUsernames {
					if page.Items[i].Username != wantUsername {
						t.Errorf("expected item %d username %q, got %q", i, wantUsername, page.Items[i].Username)
					}
				}
			}

			if videoReader.CallCount != tt.wantVideoCall {
				t.Errorf("expected VideoReader.GetByID called %d times, got %d", tt.wantVideoCall, videoReader.CallCount)
			}
			if commentRepo.ListCallCount != tt.wantListCall {
				t.Errorf("expected ListByVideoID called %d times, got %d", tt.wantListCall, commentRepo.ListCallCount)
			}
			if tt.wantListCall > 0 && commentRepo.LastListLimit != tt.wantListLimit {
				t.Errorf("expected ListByVideoID limit %d, got %d", tt.wantListLimit, commentRepo.LastListLimit)
			}
			if tt.wantUsersCall > 0 && userReader.CallCount != tt.wantUsersCall {
				t.Errorf("expected GetByIDs called %d times, got %d", tt.wantUsersCall, userReader.CallCount)
			}
		})
	}
}

// 5. 测试 Delete 方法
func TestCommentService_Delete(t *testing.T) {
	tests := []struct {
		name           string
		userID         uint64
		commentID      uint64
		mockComment    *Comment
		mockFindErr    error
		mockDeleteErr  error
		wantErr        error
		wantFindCall   int
		wantDeleteCall int
	}{
		{
			name:           "成功删除自己的评论",
			userID:         1,
			commentID:      100,
			mockComment:    &Comment{ID: 100, UserID: 1, Content: "my comment"},
			wantErr:        nil,
			wantFindCall:   1,
			wantDeleteCall: 1,
		},
		{
			name:           "重复删除（幂等），返回 nil",
			userID:         1,
			commentID:      100,
			mockFindErr:    ErrInvalidTarget, // 已经不存在
			wantErr:        nil,
			wantFindCall:   1,
			wantDeleteCall: 0, // 查不到直接返回，不调用 Delete
		},
		{
			name:           "非作者删除，返回 ErrForbidden",
			userID:         2, // 当前用户是 2
			commentID:      100,
			mockComment:    &Comment{ID: 100, UserID: 1, Content: "my comment"}, // 评论作者是 1
			wantErr:        ErrForbidden,
			wantFindCall:   1,
			wantDeleteCall: 0, // 权限不足，不执行删除
		},
		{
			name:           "userID 为 0，返回 ErrInvalidInput",
			userID:         0,
			commentID:      100,
			wantErr:        ErrInvalidInput,
			wantFindCall:   0,
			wantDeleteCall: 0,
		},
		{
			name:           "commentID 为 0，返回 ErrInvalidInput",
			userID:         1,
			commentID:      0,
			wantErr:        ErrInvalidInput,
			wantFindCall:   0,
			wantDeleteCall: 0,
		},
		{
			name:           "删除时发生数据库错误",
			userID:         1,
			commentID:      100,
			mockComment:    &Comment{ID: 100, UserID: 1, Content: "my comment"},
			mockDeleteErr:  errDBError,
			wantErr:        errDBError,
			wantFindCall:   1,
			wantDeleteCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commentRepo := &fakeCommentRepository{
				FindByIDFunc: func(ctx context.Context, id uint64) (*Comment, error) {
					return tt.mockComment, tt.mockFindErr
				},
				DeleteFunc: func(ctx context.Context, commentID, userID uint64) error {
					return tt.mockDeleteErr
				},
			}

			svc := NewCommentService(commentRepo, nil, nil)
			err := svc.Delete(context.Background(), tt.userID, tt.commentID)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if commentRepo.DeleteCallCount != tt.wantDeleteCall {
				t.Errorf("expected Delete called %d times, got %d", tt.wantDeleteCall, commentRepo.DeleteCallCount)
			}
		})
	}
}
