package interaction

import (
	"context"
	"testing"

	"go-feed-system/internal/user"
	"go-feed-system/internal/video"
)

type recordingNotifier struct {
	calls       int
	recipientID uint64
	actorID     uint64
	kind        string
	videoID     uint64
	content     string
}

func (n *recordingNotifier) Notify(
	ctx context.Context,
	recipientID uint64,
	actorID uint64,
	kind string,
	videoID uint64,
	content string,
) error {
	n.calls++
	n.recipientID = recipientID
	n.actorID = actorID
	n.kind = kind
	n.videoID = videoID
	n.content = content
	return nil
}

func TestInteractionServicesNotifyAfterSuccessfulWrite(t *testing.T) {
	ctx := context.Background()

	t.Run("点赞通知视频作者", func(t *testing.T) {
		notifier := &recordingNotifier{}
		service := NewLikeService(
			&fakeLikeRepository{},
			&fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return &video.Video{ID: id, AuthorID: 2}, nil
				},
			},
			nil,
		)
		service.SetNotifier(notifier)

		if err := service.Like(ctx, 1, 7); err != nil {
			t.Fatalf("Like() error = %v", err)
		}
		assertNotification(t, notifier, 2, 1, NotifyLike, 7)
	})

	t.Run("评论通知视频作者并携带摘要", func(t *testing.T) {
		notifier := &recordingNotifier{}
		service := NewCommentService(
			&fakeCommentRepository{},
			&fakeVideoReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
					return &video.Video{ID: id, AuthorID: 2}, nil
				},
			},
			nil,
			nil,
		)
		service.SetNotifier(notifier)

		if _, err := service.Create(ctx, 1, 7, "第一条评论"); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		assertNotification(t, notifier, 2, 1, NotifyComment, 7)
		if notifier.content != "第一条评论" {
			t.Errorf("notification content = %q, want comment excerpt", notifier.content)
		}
	})

	t.Run("关注通知被关注用户", func(t *testing.T) {
		notifier := &recordingNotifier{}
		service := NewFollowService(
			&fakeFollowRepository{},
			&fakeUserReader{
				GetByIDFunc: func(ctx context.Context, id uint64) (*user.User, error) {
					return &user.User{ID: id, Username: "bob"}, nil
				},
			},
			nil,
			nil,
		)
		service.SetNotifier(notifier)

		if err := service.Follow(ctx, 1, 2); err != nil {
			t.Fatalf("Follow() error = %v", err)
		}
		assertNotification(t, notifier, 2, 1, NotifyFollow, 0)
	})
}

func TestLikeServiceDoesNotNotifyForIdempotentRepeat(t *testing.T) {
	notifier := &recordingNotifier{}
	service := NewLikeService(
		&fakeLikeRepository{
			CreateFunc: func(ctx context.Context, userID, videoID uint64) error {
				return ErrAlreadyLiked
			},
		},
		&fakeVideoReader{
			GetByIDFunc: func(ctx context.Context, id uint64) (*video.Video, error) {
				return &video.Video{ID: id, AuthorID: 2}, nil
			},
		},
		nil,
	)
	service.SetNotifier(notifier)

	if err := service.Like(context.Background(), 1, 7); err != nil {
		t.Fatalf("Like() error = %v", err)
	}
	if notifier.calls != 0 {
		t.Fatalf("repeat like sent %d notifications, want 0", notifier.calls)
	}
}

func assertNotification(
	t *testing.T,
	notifier *recordingNotifier,
	recipientID, actorID uint64,
	kind string,
	videoID uint64,
) {
	t.Helper()
	if notifier.calls != 1 {
		t.Fatalf("notification calls = %d, want 1", notifier.calls)
	}
	if notifier.recipientID != recipientID ||
		notifier.actorID != actorID ||
		notifier.kind != kind ||
		notifier.videoID != videoID {
		t.Fatalf(
			"notification = recipient:%d actor:%d kind:%s video:%d, want recipient:%d actor:%d kind:%s video:%d",
			notifier.recipientID,
			notifier.actorID,
			notifier.kind,
			notifier.videoID,
			recipientID,
			actorID,
			kind,
			videoID,
		)
	}
}
