package interaction

import (
	"context"
	"errors"
	"testing"

	"go-feed-system/internal/video"
)

// 定义一个通用的模拟数据库错误，用于验证错误包装逻辑
var errDBError = errors.New("database connection lost")

// 1. Fake LikeRepository
type fakeLikeRepository struct {
	CreateFunc              func(ctx context.Context, userID, videoID uint64) error
	DeleteFunc              func(ctx context.Context, userID, videoID uint64) error
	ExistsFunc              func(ctx context.Context, userID, videoID uint64) (bool, error)
	CountByVideoFunc        func(ctx context.Context, videoID uint64) (int64, error)
	CountByVideoIDsFunc     func(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error)
	LikedVideoIDsByUserFunc func(ctx context.Context, userID uint64, videoIDs []uint64) (map[uint64]bool, error)

	LastUserID        uint64
	LastVideoID       uint64
	LastCountVideoID  uint64 // 记录 Count 收到的 videoID
	LastExistsUserID  uint64 // 记录 Exists 收到的 userID
	LastExistsVideoID uint64 // 记录 Exists 收到的 videoID
	LastCountVideoIDs []uint64
	LastLikedUserID   uint64
	LastLikedVideoIDs []uint64

	CreateCallCount          int
	DeleteCallCount          int
	ExistsCallCount          int
	CountCallCount           int
	CountByVideoIDsCallCount int
	LikedVideoIDsCallCount   int
}

func (f *fakeLikeRepository) Create(ctx context.Context, userID, videoID uint64) error {
	f.CreateCallCount++
	f.LastUserID = userID   // 记录
	f.LastVideoID = videoID // 记录
	if f.CreateFunc != nil {
		return f.CreateFunc(ctx, userID, videoID)
	}
	return nil
}

func (f *fakeLikeRepository) Delete(ctx context.Context, userID, videoID uint64) error {
	f.DeleteCallCount++
	f.LastUserID = userID   // 记录
	f.LastVideoID = videoID // 记录
	if f.DeleteFunc != nil {
		return f.DeleteFunc(ctx, userID, videoID)
	}
	return nil
}

func (f *fakeLikeRepository) Exists(ctx context.Context, userID, videoID uint64) (bool, error) {
	f.ExistsCallCount++
	f.LastExistsUserID = userID   // 记录
	f.LastExistsVideoID = videoID // 记录
	if f.ExistsFunc != nil {
		return f.ExistsFunc(ctx, userID, videoID)
	}
	return false, nil
}

func (f *fakeLikeRepository) CountByVideoID(ctx context.Context, videoID uint64) (int64, error) {
	f.CountCallCount++
	f.LastCountVideoID = videoID // 记录
	if f.CountByVideoFunc != nil {
		return f.CountByVideoFunc(ctx, videoID)
	}
	return 0, nil
}

func (f *fakeLikeRepository) CountByVideoIDs(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error) {
	f.CountByVideoIDsCallCount++
	f.LastCountVideoIDs = videoIDs
	if f.CountByVideoIDsFunc != nil {
		return f.CountByVideoIDsFunc(ctx, videoIDs)
	}
	return make(map[uint64]int64), nil
}

func (f *fakeLikeRepository) LikedVideoIDsByUser(
	ctx context.Context,
	userID uint64,
	videoIDs []uint64,
) (map[uint64]bool, error) {
	f.LikedVideoIDsCallCount++
	f.LastLikedUserID = userID
	f.LastLikedVideoIDs = videoIDs
	if f.LikedVideoIDsByUserFunc != nil {
		return f.LikedVideoIDsByUserFunc(ctx, userID, videoIDs)
	}
	return make(map[uint64]bool), nil
}

// 2. Fake VideoReader
type fakeVideoReader struct {
	GetByIDFunc func(ctx context.Context, id uint64) (*video.Video, error)
	CallCount   int
	LastVideoID uint64 // 记录
}

func (f *fakeVideoReader) GetByID(ctx context.Context, id uint64) (*video.Video, error) {
	f.CallCount++
	f.LastVideoID = id // 记录
	if f.GetByIDFunc != nil {
		return f.GetByIDFunc(ctx, id)
	}
	return &video.Video{ID: id}, nil
}

// 3. 测试 Like 方法
func TestLikeService_Like(t *testing.T) {
	tests := []struct {
		name           string
		userID         uint64
		videoID        uint64
		mockVideoErr   error
		mockCreateErr  error
		wantErr        error
		wantVideoCall  int
		wantCreateCall int
	}{
		{
			name:           "成功点赞",
			userID:         1,
			videoID:        1,
			wantErr:        nil,
			wantVideoCall:  1,
			wantCreateCall: 1,
		},
		{
			name:           "userID 为 0，返回 ErrInvalidInput",
			userID:         0,
			videoID:        1,
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0, // 参数校验失败，不应该查视频
			wantCreateCall: 0,
		},
		{
			name:           "videoID 为 0，返回 ErrInvalidInput",
			userID:         1,
			videoID:        0,
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0,
			wantCreateCall: 0,
		},
		{
			name:           "视频不存在，返回 ErrInvalidTarget",
			userID:         1,
			videoID:        999,
			mockVideoErr:   video.ErrNotFound,
			wantErr:        ErrInvalidTarget,
			wantVideoCall:  1,
			wantCreateCall: 0, // 视频不存在，不应该写点赞记录
		},
		{
			name:           "查询视频发生内部错误",
			userID:         1,
			videoID:        1,
			mockVideoErr:   errDBError,
			wantErr:        errDBError, // 期望被包装后依然能被 errors.Is 找到
			wantVideoCall:  1,
			wantCreateCall: 0,
		},
		{
			name:           "重复点赞（幂等），返回 nil",
			userID:         1,
			videoID:        1,
			mockCreateErr:  ErrAlreadyLiked,
			wantErr:        nil, // 核心：重复点赞视为成功
			wantVideoCall:  1,
			wantCreateCall: 1,
		},
		{
			name:           "并发删除导致外键错误，返回 ErrInvalidTarget",
			userID:         1,
			videoID:        1,
			mockCreateErr:  ErrInvalidTarget, // Repository 转换后传上来的
			wantErr:        ErrInvalidTarget,
			wantVideoCall:  1,
			wantCreateCall: 1,
		},
		{
			name:           "创建点赞发生普通数据库错误",
			userID:         1,
			videoID:        1,
			mockCreateErr:  errDBError,
			wantErr:        errDBError,
			wantVideoCall:  1,
			wantCreateCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 Fake
			videoReader := &fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return nil, tt.mockVideoErr
				},
			}
			likeRepo := &fakeLikeRepository{
				CreateFunc: func(ctx context.Context, userID, videoID uint64) error {
					return tt.mockCreateErr
				},
			}

			svc := NewLikeService(likeRepo, videoReader)
			err := svc.Like(context.Background(), tt.userID, tt.videoID)

			// 断言错误
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			// 断言传给 Repository 的参数是否与预期一致
			if tt.wantCreateCall > 0 && likeRepo.LastUserID != tt.userID {
				t.Errorf("expected userID %d, got %d", tt.userID, likeRepo.LastUserID)
			}
			if tt.wantCreateCall > 0 && likeRepo.LastVideoID != tt.videoID {
				t.Errorf("expected videoID %d, got %d", tt.videoID, likeRepo.LastVideoID)
			}

			// 断言调用次数（验证业务分支是否正确拦截，避免不必要的 I/O）
			if videoReader.CallCount != tt.wantVideoCall {
				t.Errorf("expected VideoReader.GetByID called %d times, got %d", tt.wantVideoCall, videoReader.CallCount)
			}
			if likeRepo.CreateCallCount != tt.wantCreateCall {
				t.Errorf("expected LikeRepository.Create called %d times, got %d", tt.wantCreateCall, likeRepo.CreateCallCount)
			}
		})
	}
}

// 4. 测试 Unlike 方法
func TestLikeService_Unlike(t *testing.T) {
	tests := []struct {
		name           string
		userID         uint64
		videoID        uint64
		mockDeleteErr  error
		wantErr        error
		wantVideoCall  int
		wantDeleteCall int
	}{
		{
			name:           "成功取消点赞",
			userID:         1,
			videoID:        1,
			wantErr:        nil,
			wantVideoCall:  0, // 核心：Unlike 不应该查视频
			wantDeleteCall: 1,
		},
		{
			name:           "取消不存在的点赞记录（幂等）",
			userID:         1,
			videoID:        1,
			wantErr:        nil,
			wantVideoCall:  0,
			wantDeleteCall: 1,
		},
		{
			name:           "userID 为 0，返回 ErrInvalidInput",
			userID:         0,
			videoID:        1,
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0,
			wantDeleteCall: 0,
		},
		{
			name:           "videoID 为 0，返回 ErrInvalidInput",
			userID:         1,
			videoID:        0,
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0,
			wantDeleteCall: 0,
		},
		{
			name:           "删除时发生数据库错误",
			userID:         1,
			videoID:        1,
			mockDeleteErr:  errDBError,
			wantErr:        errDBError,
			wantVideoCall:  0,
			wantDeleteCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoReader := &fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return nil, nil // 这里不重要，因为 Unlike 不应该调用它
				},
			}
			likeRepo := &fakeLikeRepository{
				DeleteFunc: func(ctx context.Context, userID, videoID uint64) error {
					return tt.mockDeleteErr
				},
			}

			svc := NewLikeService(likeRepo, videoReader)
			err := svc.Unlike(context.Background(), tt.userID, tt.videoID)

			// 断言错误
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			}

			// 断言传给 Repository 的参数是否与预期一致
			if tt.wantDeleteCall > 0 && likeRepo.LastUserID != tt.userID {
				t.Errorf("expected userID %d, got %d", tt.userID, likeRepo.LastUserID)
			}
			if tt.wantDeleteCall > 0 && likeRepo.LastVideoID != tt.videoID {
				t.Errorf("expected videoID %d, got %d", tt.videoID, likeRepo.LastVideoID)
			}

			// 断言调用次数
			if videoReader.CallCount != tt.wantVideoCall {
				t.Errorf("expected VideoReader.GetByID called %d times, got %d", tt.wantVideoCall, videoReader.CallCount)
			}
			if likeRepo.DeleteCallCount != tt.wantDeleteCall {
				t.Errorf("expected LikeRepository.Delete called %d times, got %d", tt.wantDeleteCall, likeRepo.DeleteCallCount)
			}
		})
	}
}

// 5. 测试 GetState 方法
func TestLikeService_GetState(t *testing.T) {
	tests := []struct {
		name          string
		userID        uint64
		videoID       uint64
		mockVideoErr  error
		mockCountErr  error
		mockExistsErr error
		mockCount     int64
		mockExists    bool

		wantErr        error
		wantCount      int64
		wantIsLikedBy  bool
		wantVideoCall  int
		wantCountCall  int
		wantExistsCall int
	}{
		{
			name:           "视频不存在，返回 ErrInvalidTarget",
			userID:         1,
			videoID:        999,
			mockVideoErr:   video.ErrNotFound,
			wantErr:        ErrInvalidTarget,
			wantVideoCall:  1,
			wantCountCall:  0, // 视频不存在，不应继续查询计数
			wantExistsCall: 0,
		},
		{
			name:           "videoID 为 0，返回 ErrInvalidInput",
			userID:         1,
			videoID:        0,
			wantErr:        ErrInvalidInput,
			wantVideoCall:  0,
			wantCountCall:  0,
			wantExistsCall: 0,
		},
		{
			name:           "查询视频发生内部错误",
			userID:         1,
			videoID:        1,
			mockVideoErr:   errDBError,
			wantErr:        errDBError,
			wantVideoCall:  1,
			wantCountCall:  0,
			wantExistsCall: 0,
		},
		{
			name:           "匿名用户访问，视频存在，返回总数且不查 Exists",
			userID:         0, // 匿名
			videoID:        1,
			mockCount:      100,
			wantErr:        nil,
			wantCount:      100,
			wantIsLikedBy:  false,
			wantVideoCall:  1,
			wantCountCall:  1,
			wantExistsCall: 0, // 核心：匿名用户不应查 Exists
		},
		{
			name:           "登录用户已点赞",
			userID:         1,
			videoID:        1,
			mockCount:      5,
			mockExists:     true,
			wantErr:        nil,
			wantCount:      5,
			wantIsLikedBy:  true,
			wantVideoCall:  1,
			wantCountCall:  1,
			wantExistsCall: 1,
		},
		{
			name:           "登录用户未点赞",
			userID:         1,
			videoID:        1,
			mockCount:      5,
			mockExists:     false,
			wantErr:        nil,
			wantCount:      5,
			wantIsLikedBy:  false,
			wantVideoCall:  1,
			wantCountCall:  1,
			wantExistsCall: 1,
		},
		{
			name:           "CountByVideoID 查询失败",
			userID:         1,
			videoID:        1,
			mockCountErr:   errDBError,
			wantErr:        errDBError,
			wantVideoCall:  1,
			wantCountCall:  1,
			wantExistsCall: 0, // 计数失败，不应继续查 Exists
		},
		{
			name:           "Exists 查询失败",
			userID:         1,
			videoID:        1,
			mockCount:      5,
			mockExistsErr:  errDBError,
			wantErr:        errDBError,
			wantVideoCall:  1,
			wantCountCall:  1,
			wantExistsCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 构造 Fake 依赖
			videoReader := &fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return nil, tt.mockVideoErr
				},
			}
			likeRepo := &fakeLikeRepository{
				CountByVideoFunc: func(ctx context.Context, videoID uint64) (int64, error) {
					return tt.mockCount, tt.mockCountErr
				},
				ExistsFunc: func(ctx context.Context, userID, videoID uint64) (bool, error) {
					return tt.mockExists, tt.mockExistsErr
				},
			}

			svc := NewLikeService(likeRepo, videoReader)
			state, err := svc.GetState(context.Background(), tt.userID, tt.videoID)

			// 断言错误
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				// 无错误时校验返回值
				if state.Count != tt.wantCount {
					t.Errorf("expected count %d, got %d", tt.wantCount, state.Count)
				}
				if state.IsLikedBy != tt.wantIsLikedBy {
					t.Errorf("expected IsLikedBy %v, got %v", tt.wantIsLikedBy, state.IsLikedBy)
				}
			}

			// 断言调用次数（非常关键，验证分支逻辑是否正确短路）
			if videoReader.CallCount != tt.wantVideoCall {
				t.Errorf("expected VideoReader.GetByID called %d times, got %d", tt.wantVideoCall, videoReader.CallCount)
			}
			if likeRepo.CountCallCount != tt.wantCountCall {
				t.Errorf("expected CountByVideoID called %d times, got %d", tt.wantCountCall, likeRepo.CountCallCount)
			}
			if likeRepo.ExistsCallCount != tt.wantExistsCall {
				t.Errorf("expected Exists called %d times, got %d", tt.wantExistsCall, likeRepo.ExistsCallCount)
			}
			// 因为 CountByVideoID 我们没有设计调用次数计数器，这里可以直接通过行为验证：
			// 如果传了 mockCountErr，说明进入了 count 分支，那么 exists 分支不应该被触发（除非 count 成功）
			// 我们用更简单的逻辑：数一下 Exists 有没有被调用
			if tt.wantCountCall > 0 && likeRepo.LastCountVideoID != tt.videoID {
				t.Errorf("expected CountByVideoID received videoID %d, got %d", tt.videoID, likeRepo.LastCountVideoID)
			}
			if tt.wantExistsCall > 0 {
				if likeRepo.LastExistsUserID != tt.userID {
					t.Errorf("expected Exists received userID %d, got %d", tt.userID, likeRepo.LastExistsUserID)
				}
				if likeRepo.LastExistsVideoID != tt.videoID {
					t.Errorf("expected Exists received videoID %d, got %d", tt.videoID, likeRepo.LastExistsVideoID)
				}
			}
			if tt.wantVideoCall > 0 && videoReader.LastVideoID != tt.videoID {
				t.Errorf("expected VideoReader.GetByID received videoID %d, got %d", tt.videoID, videoReader.LastVideoID)
			}
		})
	}
}

func TestLikeService_GetLikeState(t *testing.T) {
	tests := []struct {
		name          string
		viewerID      uint64
		videoID       uint64
		mockVideoErr  error
		mockCount     int64
		mockCountErr  error
		mockExists    bool
		mockExistsErr error
		wantErr       error
		wantCount     int64
		wantIsLikedBy bool
	}{
		{
			name:      "匿名用户返回基础状态",
			videoID:   1,
			mockCount: 8,
			wantCount: 8,
		},
		{
			name:          "登录用户返回点赞状态",
			viewerID:      7,
			videoID:       1,
			mockCount:     8,
			mockExists:    true,
			wantCount:     8,
			wantIsLikedBy: true,
		},
		{
			name:         "视频不存在转换为 video.ErrNotFound",
			videoID:      999,
			mockVideoErr: video.ErrNotFound,
			wantErr:      video.ErrNotFound,
		},
		{
			name:         "计数错误保留原始错误链",
			videoID:      1,
			mockCountErr: errDBError,
			wantErr:      errDBError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoReader := &fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return nil, tt.mockVideoErr
				},
			}
			likeRepo := &fakeLikeRepository{
				CountByVideoFunc: func(ctx context.Context, videoID uint64) (int64, error) {
					return tt.mockCount, tt.mockCountErr
				},
				ExistsFunc: func(ctx context.Context, userID, videoID uint64) (bool, error) {
					return tt.mockExists, tt.mockExistsErr
				},
			}

			svc := NewLikeService(likeRepo, videoReader)
			count, isLikedBy, err := svc.GetLikeState(context.Background(), tt.viewerID, tt.videoID)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}
			if err == nil {
				if count != tt.wantCount {
					t.Errorf("expected count %d, got %d", tt.wantCount, count)
				}
				if isLikedBy != tt.wantIsLikedBy {
					t.Errorf("expected isLikedBy %v, got %v", tt.wantIsLikedBy, isLikedBy)
				}
			}
		})
	}
}

func TestLikeService_CountByVideoIDs(t *testing.T) {
	tests := []struct {
		name          string
		videoIDs      []uint64
		mockCounts    map[uint64]int64
		mockErr       error
		wantErr       error
		wantCallCount int
	}{
		{
			name:          "空 ID 不访问 Repository",
			videoIDs:      nil,
			wantCallCount: 0,
		},
		{
			name:          "批量返回点赞数",
			videoIDs:      []uint64{1, 2, 3},
			mockCounts:    map[uint64]int64{1: 2, 3: 5},
			wantCallCount: 1,
		},
		{
			name:          "Repository 错误保留错误链",
			videoIDs:      []uint64{1},
			mockErr:       errDBError,
			wantErr:       errDBError,
			wantCallCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeLikeRepository{
				CountByVideoIDsFunc: func(ctx context.Context, videoIDs []uint64) (map[uint64]int64, error) {
					return tt.mockCounts, tt.mockErr
				},
			}
			svc := NewLikeService(repo, nil)

			counts, err := svc.CountByVideoIDs(context.Background(), tt.videoIDs)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}
			if repo.CountByVideoIDsCallCount != tt.wantCallCount {
				t.Fatalf("expected repository called %d times, got %d", tt.wantCallCount, repo.CountByVideoIDsCallCount)
			}
			if err == nil && len(counts) != len(tt.mockCounts) {
				t.Fatalf("expected %d counts, got %d", len(tt.mockCounts), len(counts))
			}
		})
	}
}

func TestLikeService_LikedVideoIDsByUser(t *testing.T) {
	tests := []struct {
		name          string
		userID        uint64
		videoIDs      []uint64
		mockLiked     map[uint64]bool
		mockErr       error
		wantErr       error
		wantCallCount int
	}{
		{
			name:     "userID 为 0 返回参数错误",
			userID:   0,
			videoIDs: []uint64{1},
			wantErr:  ErrInvalidInput,
		},
		{
			name:          "空 ID 不访问 Repository",
			userID:        1,
			videoIDs:      nil,
			wantCallCount: 0,
		},
		{
			name:          "批量返回点赞状态",
			userID:        1,
			videoIDs:      []uint64{1, 2},
			mockLiked:     map[uint64]bool{1: true},
			wantCallCount: 1,
		},
		{
			name:          "Repository 错误保留错误链",
			userID:        1,
			videoIDs:      []uint64{1},
			mockErr:       errDBError,
			wantErr:       errDBError,
			wantCallCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeLikeRepository{
				LikedVideoIDsByUserFunc: func(
					ctx context.Context,
					userID uint64,
					videoIDs []uint64,
				) (map[uint64]bool, error) {
					return tt.mockLiked, tt.mockErr
				},
			}
			svc := NewLikeService(repo, nil)

			liked, err := svc.LikedVideoIDsByUser(context.Background(), tt.userID, tt.videoIDs)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected err %v, got %v", tt.wantErr, err)
			}
			if repo.LikedVideoIDsCallCount != tt.wantCallCount {
				t.Fatalf("expected repository called %d times, got %d", tt.wantCallCount, repo.LikedVideoIDsCallCount)
			}
			if err == nil && len(liked) != len(tt.mockLiked) {
				t.Fatalf("expected %d liked entries, got %d", len(tt.mockLiked), len(liked))
			}
		})
	}
}
