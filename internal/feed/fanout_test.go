package feed

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	errFanoutFollowers = errors.New("follower repository failed")
	errFanoutAdd       = errors.New("inbox add failed")
	errFanoutTrim      = errors.New("inbox trim failed")
)

// 1. Fake FollowerReader
type fakeFollowerReader struct {
	ListFollowerIDsFunc func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error)

	CallCount    int
	LastAuthorID uint64
	LastAfterID  uint64
	LastLimit    int
}

func (f *fakeFollowerReader) ListFollowerIDs(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error) {
	f.CallCount++
	f.LastAuthorID = authorID
	f.LastAfterID = afterID
	f.LastLimit = limit
	if f.ListFollowerIDsFunc != nil {
		return f.ListFollowerIDsFunc(ctx, authorID, afterID, limit)
	}
	return nil, nil
}

// 2. Fake Inbox
type fakeInbox struct {
	AddFunc  func(ctx context.Context, userIDs []uint64, videoID uint64, publishedAt time.Time) error
	TrimFunc func(ctx context.Context, userID uint64) error

	AddCallCount     int
	TrimCallCount    int
	TrimmedUserIDs   []uint64
	LastAddedUserIDs []uint64
	LastAddedVideoID uint64
	LastAddedAt      time.Time
}

func (f *fakeInbox) Add(ctx context.Context, userIDs []uint64, videoID uint64, publishedAt time.Time) error {
	f.AddCallCount++
	f.LastAddedUserIDs = userIDs
	f.LastAddedVideoID = videoID
	f.LastAddedAt = publishedAt
	if f.AddFunc != nil {
		return f.AddFunc(ctx, userIDs, videoID, publishedAt)
	}
	return nil
}

func (f *fakeInbox) Trim(ctx context.Context, userID uint64) error {
	f.TrimCallCount++
	f.TrimmedUserIDs = append(f.TrimmedUserIDs, userID)
	if f.TrimFunc != nil {
		return f.TrimFunc(ctx, userID)
	}
	return nil
}

// 为了满足 Inbox 接口的 List 方法（测试不需要用到）
func (f *fakeInbox) List(ctx context.Context, userID uint64, cursor uint64, limit int) ([]uint64, error) {
	return nil, nil
}

// 3. 表驱动测试
func TestFanoutService_Fanout(t *testing.T) {
	validEvent := VideoPublishedEvent{
		EventID:     "evt-001",
		VideoID:     1001,
		AuthorID:    10,
		PublishedAt: time.Now(),
	}

	tests := []struct {
		name  string
		event VideoPublishedEvent

		mockFollowers     func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error)
		mockInboxAddFunc  func(ctx context.Context, userIDs []uint64, videoID uint64, publishedAt time.Time) error
		mockInboxTrimFunc func(ctx context.Context, userID uint64) error

		wantErr          error
		wantAddCall      int
		wantTrimCall     int
		wantFollowerCall int
		wantLastAfterID  uint64
		wantTrimmedIDs   []uint64
	}{
		{
			name:             "无效事件，直接返回 ErrInvalidInput",
			event:            VideoPublishedEvent{VideoID: 1001}, // 缺少 EventID 等
			wantErr:          ErrInvalidInput,
			wantAddCall:      0,
			wantTrimCall:     0,
			wantFollowerCall: 0,
		},
		{
			name:  "没有粉丝，不调用 Add 和 Trim",
			event: validEvent,
			mockFollowers: func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error) {
				return []uint64{}, nil
			},
			wantErr:          nil,
			wantAddCall:      0,
			wantTrimCall:     0,
			wantFollowerCall: 1,
		},
		{
			name:  "单批粉丝（小于 batch size），写入并裁剪",
			event: validEvent,
			mockFollowers: func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error) {
				return []uint64{1, 2, 3}, nil // 只有 3 个粉丝
			},
			wantErr:          nil,
			wantAddCall:      1,
			wantTrimCall:     3, // 每个粉丝调用一次 Trim
			wantFollowerCall: 1,
			wantTrimmedIDs:   []uint64{1, 2, 3},
		},
		{
			name:  "多批粉丝（达到 batch size），分页读取",
			event: validEvent,
			mockFollowers: func() func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error) {
				callCount := 0
				return func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error) {
					callCount++
					if callCount == 1 {
						// 第一批返回 500 个粉丝（达到 DefaultFanoutBatchSize）
						fans := make([]uint64, 500)
						for i := 0; i < 500; i++ {
							fans[i] = uint64(i + 1)
						}
						return fans, nil
					}
					// 第二批返回 50 个粉丝（小于 batch size，触发退出）
					fans := make([]uint64, 50)
					for i := 0; i < 50; i++ {
						fans[i] = uint64(i + 501)
					}
					return fans, nil
				}
			}(),
			wantErr:          nil,
			wantAddCall:      2, // 第一批 500，第二批 50
			wantTrimCall:     550,
			wantFollowerCall: 2,
			wantLastAfterID:  500,
		},
		{
			name:  "粉丝查询失败，返回错误",
			event: validEvent,
			mockFollowers: func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error) {
				return nil, errFanoutFollowers
			},
			wantErr:          errFanoutFollowers,
			wantAddCall:      0,
			wantTrimCall:     0,
			wantFollowerCall: 1,
		},
		{
			name:  "Inbox.Add 失败，返回错误",
			event: validEvent,
			mockFollowers: func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error) {
				return []uint64{1, 2, 3}, nil
			},
			mockInboxAddFunc: func(ctx context.Context, userIDs []uint64, videoID uint64, publishedAt time.Time) error {
				return errFanoutAdd
			},
			wantErr:          errFanoutAdd,
			wantAddCall:      1, // Add 被调用一次（但失败了）
			wantTrimCall:     0, // Add 失败，不应继续 Trim
			wantFollowerCall: 1,
		},
		{
			name:  "Inbox.Trim 失败，返回错误",
			event: validEvent,
			mockFollowers: func(ctx context.Context, authorID, afterID uint64, limit int) ([]uint64, error) {
				return []uint64{1, 2, 3}, nil
			},
			mockInboxTrimFunc: func(ctx context.Context, userID uint64) error {
				return errFanoutTrim
			},
			wantErr:          errFanoutTrim,
			wantAddCall:      1,
			wantTrimCall:     1, // 第一次 Trim 就失败，中断后续
			wantFollowerCall: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &fakeFollowerReader{ListFollowerIDsFunc: tt.mockFollowers}
			inbox := &fakeInbox{
				AddFunc:  tt.mockInboxAddFunc,
				TrimFunc: tt.mockInboxTrimFunc,
			}

			svc := NewFanoutService(reader, inbox)
			err := svc.Fanout(context.Background(), tt.event)

			// 1. 断言错误
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected err %v, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			// 2. 断言调用次数
			if reader.CallCount != tt.wantFollowerCall {
				t.Errorf("expected ListFollowerIDs called %d times, got %d", tt.wantFollowerCall, reader.CallCount)
			}
			if inbox.AddCallCount != tt.wantAddCall {
				t.Errorf("expected Inbox.Add called %d times, got %d", tt.wantAddCall, inbox.AddCallCount)
			}
			if inbox.TrimCallCount != tt.wantTrimCall {
				t.Errorf("expected Inbox.Trim called %d times, got %d", tt.wantTrimCall, inbox.TrimCallCount)
			}

			// 3. 验证参数透传
			if tt.wantAddCall > 0 {
				if inbox.LastAddedVideoID != tt.event.VideoID {
					t.Errorf("expected videoID %d passed to Add, got %d", tt.event.VideoID, inbox.LastAddedVideoID)
				}
				if !inbox.LastAddedAt.Equal(tt.event.PublishedAt) {
					t.Errorf("expected publishedAt %v passed to Add, got %v", tt.event.PublishedAt, inbox.LastAddedAt)
				}
			}
			if tt.wantFollowerCall > 0 {
				if reader.LastAuthorID != tt.event.AuthorID {
					t.Errorf("expected authorID %d passed to ListFollowerIDs, got %d", tt.event.AuthorID, reader.LastAuthorID)
				}
			}
			if tt.wantLastAfterID > 0 && reader.LastAfterID != tt.wantLastAfterID {
				t.Errorf("expected last afterID %d, got %d", tt.wantLastAfterID, reader.LastAfterID)
			}
			if len(tt.wantTrimmedIDs) > 0 {
				if len(inbox.TrimmedUserIDs) != len(tt.wantTrimmedIDs) {
					t.Fatalf("expected trimmed IDs %v, got %v", tt.wantTrimmedIDs, inbox.TrimmedUserIDs)
				}
				for i, wantID := range tt.wantTrimmedIDs {
					if inbox.TrimmedUserIDs[i] != wantID {
						t.Errorf("expected trimmed ID %d at %d, got %d", wantID, i, inbox.TrimmedUserIDs[i])
					}
				}
			}
		})
	}
}
