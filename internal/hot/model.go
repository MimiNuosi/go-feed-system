package hot

type RankedVideo struct {
	VideoID uint64
	Score   float64
}

// Cursor 是热榜的复合游标。
//
// 热榜按 (score DESC, videoID DESC) 排序，所以游标必须同时带上分数和
// videoID：只带分数会在"同分"处漏掉或重复数据。
type Cursor struct {
	Score   float64 `json:"score"`
	VideoID uint64  `json:"video_id"`
}

// Page 是一页排名结果。
//
// 它只包含排名信息，视频详情（作者、点赞数等）由 Handler 补齐，
// 这样热榜的排名查询和跨领域聚合保持各自独立。
type Page struct {
	Videos     []RankedVideo
	NextCursor string
	HasMore    bool
}
