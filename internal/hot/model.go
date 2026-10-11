package hot

type RankedVideo struct {
	VideoID uint64
	Score   float64
}

type Cursor struct {
	Score   float64
	VideoID uint64
}
