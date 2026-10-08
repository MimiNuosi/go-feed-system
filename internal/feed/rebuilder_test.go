package feed

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	errRebuildBigAuthors = errors.New("big authors failed")
	errRebuildFollowing  = errors.New("list following failed")
	errRebuildAdd        = errors.New("inbox add failed")
	errRebuildTrim       = errors.New("inbox trim failed")
)

type rebuildAddCall struct {
	userIDs     []uint64
	videoID     uint64
	publishedAt time.Time
}

type fakeRebuildRepository struct {
	bigAuthorIDs []uint64
	bigAuthorErr error

	pages        [][]VideoRecord
	followingErr error

	followingCalls int
	cursors        []*Cursor
	thresholds     []int
}

func (f *fakeRebuildRepository) ListBigAuthorIDs(
	ctx context.Context,
	followerID uint64,
	threshold int,
) ([]uint64, error) {
	f.thresholds = append(f.thresholds, threshold)
	if f.bigAuthorErr != nil {
		return nil, f.bigAuthorErr
	}
	return f.bigAuthorIDs, nil
}

func (f *fakeRebuildRepository) ListFollowing(
	ctx context.Context,
	followerID uint64,
	cursor *Cursor,
	limit int,
) ([]VideoRecord, error) {
	f.followingCalls++

	var cursorCopy *Cursor
	if cursor != nil {
		value := *cursor
		cursorCopy = &value
	}
	f.cursors = append(f.cursors, cursorCopy)

	if f.followingErr != nil {
		return nil, f.followingErr
	}

	pageIndex := f.followingCalls - 1
	if pageIndex >= len(f.pages) {
		return []VideoRecord{}, nil
	}
	return f.pages[pageIndex], nil
}

type fakeRebuildInbox struct {
	addErr    error
	trimErr   error
	addCalls  []rebuildAddCall
	trimUsers []uint64
}

func (f *fakeRebuildInbox) Add(
	ctx context.Context,
	userIDs []uint64,
	videoID uint64,
	publishedAt time.Time,
) error {
	clonedUserIDs := append([]uint64(nil), userIDs...)
	f.addCalls = append(f.addCalls, rebuildAddCall{
		userIDs:     clonedUserIDs,
		videoID:     videoID,
		publishedAt: publishedAt,
	})
	return f.addErr
}

func (f *fakeRebuildInbox) Trim(ctx context.Context, userID uint64) error {
	f.trimUsers = append(f.trimUsers, userID)
	return f.trimErr
}

func TestNormalizeRebuildOptions(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		options RebuildOptions
		want    RebuildOptions
		wantErr bool
	}{
		{
			name: "填充默认批量大小并去重",
			options: RebuildOptions{
				UserIDs: []uint64{2, 1, 2},
				Since:   now,
			},
			want: RebuildOptions{
				UserIDs:   []uint64{2, 1},
				Since:     now,
				BatchSize: DefaultRebuildBatchSize,
			},
		},
		{
			name:    "没有用户",
			options: RebuildOptions{Since: now},
			wantErr: true,
		},
		{
			name: "时间为零",
			options: RebuildOptions{
				UserIDs: []uint64{1},
			},
			wantErr: true,
		},
		{
			name: "用户 ID 为零",
			options: RebuildOptions{
				UserIDs: []uint64{0},
				Since:   now,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeRebuildOptions(tt.options)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalize options: %v", err)
			}
			if got.BatchSize != tt.want.BatchSize {
				t.Fatalf("expected batch size %d, got %d", tt.want.BatchSize, got.BatchSize)
			}
			if !got.Since.Equal(tt.want.Since) {
				t.Fatalf("expected since %v, got %v", tt.want.Since, got.Since)
			}
			if len(got.UserIDs) != len(tt.want.UserIDs) {
				t.Fatalf("expected user IDs %v, got %v", tt.want.UserIDs, got.UserIDs)
			}
			for i := range tt.want.UserIDs {
				if got.UserIDs[i] != tt.want.UserIDs[i] {
					t.Fatalf("expected user IDs %v, got %v", tt.want.UserIDs, got.UserIDs)
				}
			}
		})
	}
}

func TestRebuilder_Rebuild(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	since := now.Add(-24 * time.Hour)
	old := since.Add(-time.Second)

	tests := []struct {
		name string

		bigAuthorIDs []uint64
		pages        [][]VideoRecord
		options      RebuildOptions

		wantResult         RebuildResult
		wantFollowingCalls int
		wantAddedVideoIDs  []uint64
		wantTrimUsers      []uint64
		wantSecondCursorID uint64
	}{
		{
			name:         "dry-run 过滤大 V 且不写 Redis",
			bigAuthorIDs: []uint64{20},
			pages: [][]VideoRecord{
				{
					{ID: 1, AuthorID: 10, CreatedAt: now},
					{ID: 2, AuthorID: 20, CreatedAt: now},
				},
			},
			options: RebuildOptions{
				UserIDs:   []uint64{1},
				Since:     since,
				BatchSize: 10,
				Execute:   false,
			},
			wantResult: RebuildResult{
				UserID:   1,
				Scanned:  2,
				Eligible: 1,
				Written:  0,
				DryRun:   true,
			},
			wantFollowingCalls: 1,
			wantAddedVideoIDs:  nil,
			wantTrimUsers:      nil,
		},
		{
			name:         "execute 写入普通作者视频并执行 Trim",
			bigAuthorIDs: []uint64{20},
			pages: [][]VideoRecord{
				{
					{ID: 1, AuthorID: 10, CreatedAt: now},
					{ID: 2, AuthorID: 20, CreatedAt: now},
				},
			},
			options: RebuildOptions{
				UserIDs:   []uint64{1},
				Since:     since,
				BatchSize: 10,
				Execute:   true,
			},
			wantResult: RebuildResult{
				UserID:   1,
				Scanned:  2,
				Eligible: 1,
				Written:  1,
				DryRun:   false,
			},
			wantFollowingCalls: 1,
			wantAddedVideoIDs:  []uint64{1},
			wantTrimUsers:      []uint64{1},
		},
		{
			name: "完整页包含 since 之前的数据时立即停止分页",
			pages: [][]VideoRecord{
				{
					{ID: 1, AuthorID: 10, CreatedAt: now},
					{ID: 2, AuthorID: 10, CreatedAt: old},
				},
				{
					{ID: 3, AuthorID: 10, CreatedAt: now.Add(-time.Hour)},
				},
			},
			options: RebuildOptions{
				UserIDs:   []uint64{1},
				Since:     since,
				BatchSize: 2,
				Execute:   true,
			},
			wantResult: RebuildResult{
				UserID:   1,
				Scanned:  2,
				Eligible: 1,
				Written:  1,
				DryRun:   false,
			},
			wantFollowingCalls: 1,
			wantAddedVideoIDs:  []uint64{1},
			wantTrimUsers:      []uint64{1},
		},
		{
			name: "多页分页推进 cursor 并写入所有普通作者视频",
			pages: [][]VideoRecord{
				{
					{ID: 3, AuthorID: 10, CreatedAt: now},
					{ID: 2, AuthorID: 10, CreatedAt: now.Add(-time.Minute)},
				},
				{
					{ID: 1, AuthorID: 10, CreatedAt: now.Add(-2 * time.Minute)},
				},
			},
			options: RebuildOptions{
				UserIDs:   []uint64{1},
				Since:     since,
				BatchSize: 2,
				Execute:   true,
			},
			wantResult: RebuildResult{
				UserID:   1,
				Scanned:  3,
				Eligible: 3,
				Written:  3,
				DryRun:   false,
			},
			wantFollowingCalls: 2,
			wantAddedVideoIDs:  []uint64{3, 2, 1},
			wantTrimUsers:      []uint64{1},
			wantSecondCursorID: 2,
		},
		{
			name: "无效记录不参与统计和写入",
			pages: [][]VideoRecord{
				{
					{ID: 0, AuthorID: 10, CreatedAt: now},
					{ID: 1, AuthorID: 10},
				},
			},
			options: RebuildOptions{
				UserIDs:   []uint64{1},
				Since:     since,
				BatchSize: 10,
				Execute:   true,
			},
			wantResult: RebuildResult{
				UserID:   1,
				Scanned:  0,
				Eligible: 0,
				Written:  0,
				DryRun:   false,
			},
			wantFollowingCalls: 1,
			wantAddedVideoIDs:  nil,
			wantTrimUsers:      []uint64{1},
		},
		{
			name: "空页立即结束",
			pages: [][]VideoRecord{
				{},
			},
			options: RebuildOptions{
				UserIDs:   []uint64{1},
				Since:     since,
				BatchSize: 10,
				Execute:   false,
			},
			wantResult: RebuildResult{
				UserID: 1,
				DryRun: true,
			},
			wantFollowingCalls: 1,
			wantAddedVideoIDs:  nil,
			wantTrimUsers:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &fakeRebuildRepository{
				bigAuthorIDs: tt.bigAuthorIDs,
				pages:        tt.pages,
			}
			inbox := &fakeRebuildInbox{}
			rebuilder := NewRebuilder(repository, inbox, DefaultFanoutFollowerThreshold, nil)

			got, err := rebuilder.Rebuild(context.Background(), tt.options)
			if err != nil {
				t.Fatalf("rebuild: %v", err)
			}
			if len(got) != 1 {
				t.Fatalf("expected one result, got %v", got)
			}
			if got[0] != tt.wantResult {
				t.Fatalf("expected result %+v, got %+v", tt.wantResult, got[0])
			}
			if repository.followingCalls != tt.wantFollowingCalls {
				t.Fatalf(
					"expected %d ListFollowing calls, got %d",
					tt.wantFollowingCalls,
					repository.followingCalls,
				)
			}

			gotAddedVideoIDs := make([]uint64, 0, len(inbox.addCalls))
			for _, call := range inbox.addCalls {
				gotAddedVideoIDs = append(gotAddedVideoIDs, call.videoID)
				if len(call.userIDs) != 1 || call.userIDs[0] != tt.options.UserIDs[0] {
					t.Fatalf("unexpected Add user IDs: %v", call.userIDs)
				}
			}
			if !equalUint64Slices(gotAddedVideoIDs, tt.wantAddedVideoIDs) {
				t.Fatalf(
					"expected added video IDs %v, got %v",
					tt.wantAddedVideoIDs,
					gotAddedVideoIDs,
				)
			}
			if !equalUint64Slices(inbox.trimUsers, tt.wantTrimUsers) {
				t.Fatalf("expected trim users %v, got %v", tt.wantTrimUsers, inbox.trimUsers)
			}

			if tt.wantSecondCursorID != 0 {
				if len(repository.cursors) < 2 || repository.cursors[1] == nil {
					t.Fatalf("expected second page cursor, got %v", repository.cursors)
				}
				if repository.cursors[1].ID != tt.wantSecondCursorID {
					t.Fatalf(
						"expected second cursor ID %d, got %d",
						tt.wantSecondCursorID,
						repository.cursors[1].ID,
					)
				}
			}
		})
	}
}

func TestRebuilder_RebuildErrors(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name string

		bigAuthorErr error
		followingErr error
		addErr       error
		trimErr      error

		wantErr error
	}{
		{
			name:         "查询大 V 失败",
			bigAuthorErr: errRebuildBigAuthors,
			wantErr:      errRebuildBigAuthors,
		},
		{
			name:         "查询关注 Feed 失败",
			followingErr: errRebuildFollowing,
			wantErr:      errRebuildFollowing,
		},
		{
			name:    "写入 Inbox 失败",
			addErr:  errRebuildAdd,
			wantErr: errRebuildAdd,
		},
		{
			name:    "Trim 失败",
			trimErr: errRebuildTrim,
			wantErr: errRebuildTrim,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &fakeRebuildRepository{
				bigAuthorErr: tt.bigAuthorErr,
				followingErr: tt.followingErr,
				pages: [][]VideoRecord{
					{
						{ID: 1, AuthorID: 10, CreatedAt: now},
					},
				},
			}
			inbox := &fakeRebuildInbox{
				addErr:  tt.addErr,
				trimErr: tt.trimErr,
			}
			rebuilder := NewRebuilder(repository, inbox, DefaultFanoutFollowerThreshold, nil)

			_, err := rebuilder.Rebuild(context.Background(), RebuildOptions{
				UserIDs:   []uint64{1},
				Since:     now.Add(-time.Hour),
				BatchSize: 10,
				Execute:   true,
			})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

func equalUint64Slices(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
