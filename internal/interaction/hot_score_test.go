package interaction

import (
	"context"
	"testing"
	"time"

	"go-feed-system/internal/video"
)

type hotScoreCall struct {
	videoID uint64
	delta   int64
}

type fakeHotScoreRecorder struct {
	calls []hotScoreCall
}

func (f *fakeHotScoreRecorder) AddScore(
	ctx context.Context,
	videoID uint64,
	delta int64,
	at time.Time,
) {
	f.calls = append(f.calls, hotScoreCall{videoID: videoID, delta: delta})
}

func TestLikeService_HotScoreDeltas(t *testing.T) {
	t.Run("点赞成功增加三分", func(t *testing.T) {
		recorder := &fakeHotScoreRecorder{}
		repo := &fakeLikeRepository{}
		service := NewLikeService(
			repo,
			&fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return &video.Video{ID: id}, nil
				},
			},
			recorder,
		)

		if err := service.Like(context.Background(), 1, 10); err != nil {
			t.Fatalf("like: %v", err)
		}
		assertHotScoreCalls(t, recorder, []hotScoreCall{{videoID: 10, delta: 3}})
	})

	t.Run("重复点赞不增加分数", func(t *testing.T) {
		recorder := &fakeHotScoreRecorder{}
		repo := &fakeLikeRepository{
			CreateFunc: func(ctx context.Context, userID, videoID uint64) error {
				return ErrAlreadyLiked
			},
		}
		service := NewLikeService(
			repo,
			&fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return &video.Video{ID: id}, nil
				},
			},
			recorder,
		)

		if err := service.Like(context.Background(), 1, 10); err != nil {
			t.Fatalf("like: %v", err)
		}
		assertHotScoreCalls(t, recorder, nil)
	})

	t.Run("取消实际存在的点赞扣三分", func(t *testing.T) {
		recorder := &fakeHotScoreRecorder{}
		repo := &fakeLikeRepository{
			DeleteFunc: func(ctx context.Context, userID, videoID uint64) (bool, error) {
				return true, nil
			},
		}
		service := NewLikeService(repo, nil, recorder)

		if err := service.Unlike(context.Background(), 1, 10); err != nil {
			t.Fatalf("unlike: %v", err)
		}
		assertHotScoreCalls(t, recorder, []hotScoreCall{{videoID: 10, delta: -3}})
	})

	t.Run("取消不存在的点赞不扣分", func(t *testing.T) {
		recorder := &fakeHotScoreRecorder{}
		repo := &fakeLikeRepository{
			DeleteFunc: func(ctx context.Context, userID, videoID uint64) (bool, error) {
				return false, nil
			},
		}
		service := NewLikeService(repo, nil, recorder)

		if err := service.Unlike(context.Background(), 1, 10); err != nil {
			t.Fatalf("unlike: %v", err)
		}
		assertHotScoreCalls(t, recorder, nil)
	})
}

func TestCommentService_HotScoreDeltas(t *testing.T) {
	t.Run("发表评论增加五分", func(t *testing.T) {
		recorder := &fakeHotScoreRecorder{}
		service := NewCommentService(
			&fakeCommentRepository{},
			&fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return &video.Video{ID: id}, nil
				},
			},
			nil,
			recorder,
		)

		if _, err := service.Create(context.Background(), 1, 10, "hello"); err != nil {
			t.Fatalf("create comment: %v", err)
		}
		assertHotScoreCalls(t, recorder, []hotScoreCall{{videoID: 10, delta: 5}})
	})

	t.Run("删除实际存在的评论扣五分", func(t *testing.T) {
		recorder := &fakeHotScoreRecorder{}
		repo := &fakeCommentRepository{
			FindByIDFunc: func(ctx context.Context, id uint64) (*Comment, error) {
				return &Comment{ID: id, VideoID: 10, UserID: 1}, nil
			},
			DeleteFunc: func(ctx context.Context, commentID, userID uint64) (bool, error) {
				return true, nil
			},
		}
		service := NewCommentService(repo, nil, nil, recorder)

		if err := service.Delete(context.Background(), 1, 100); err != nil {
			t.Fatalf("delete comment: %v", err)
		}
		assertHotScoreCalls(t, recorder, []hotScoreCall{{videoID: 10, delta: -5}})
	})

	t.Run("删除未命中记录不扣分", func(t *testing.T) {
		recorder := &fakeHotScoreRecorder{}
		repo := &fakeCommentRepository{
			FindByIDFunc: func(ctx context.Context, id uint64) (*Comment, error) {
				return &Comment{ID: id, VideoID: 10, UserID: 1}, nil
			},
			DeleteFunc: func(ctx context.Context, commentID, userID uint64) (bool, error) {
				return false, nil
			},
		}
		service := NewCommentService(repo, nil, nil, recorder)

		if err := service.Delete(context.Background(), 1, 100); err != nil {
			t.Fatalf("delete comment: %v", err)
		}
		assertHotScoreCalls(t, recorder, nil)
	})
}

func assertHotScoreCalls(
	t *testing.T,
	recorder *fakeHotScoreRecorder,
	want []hotScoreCall,
) {
	t.Helper()

	if len(recorder.calls) != len(want) {
		t.Fatalf("expected hot score calls %v, got %v", want, recorder.calls)
	}
	for i := range want {
		if recorder.calls[i] != want[i] {
			t.Fatalf("expected hot score calls %v, got %v", want, recorder.calls)
		}
	}
}
