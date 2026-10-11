package hot

import (
	"fmt"
	"strings"
)

const (
	viewDedupKeyPrefix = "view:dedup:"
	pendingViewsKey    = "video:views:pending"
)

func ViewDedupKey(videoID uint64, viewerKey string) string {
	return fmt.Sprintf(
		"%s%d:%s",
		viewDedupKeyPrefix,
		videoID,
		strings.TrimSpace(viewerKey),
	)
}

func PendingViewsKey() string {
	return pendingViewsKey
}
