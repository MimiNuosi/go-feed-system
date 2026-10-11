package hot

import (
	"fmt"
	"strings"
	"time"
)

type Window string

const (
	WindowOneHour     Window = "1h"
	WindowTwentyFourH Window = "24h"
)

type WindowConfig struct {
	Duration       time.Duration
	BucketDuration time.Duration
}

var windowConfigs = map[Window]WindowConfig{
	WindowOneHour: {
		Duration:       time.Hour,
		BucketDuration: time.Minute,
	},
	WindowTwentyFourH: {
		Duration:       24 * time.Hour,
		BucketDuration: 5 * time.Minute,
	},
}

func ParseWindow(raw string) (Window, error) {
	window := Window(strings.TrimSpace(raw))
	if _, ok := windowConfigs[window]; !ok {
		return "", fmt.Errorf("unsupported hot window %q", raw)
	}
	return window, nil
}

func (w Window) Config() (WindowConfig, error) {
	config, ok := windowConfigs[w]
	if !ok {
		return WindowConfig{}, fmt.Errorf("unsupported hot window %q", w)
	}
	return config, nil
}

func (w Window) CurrentBucketKey(at time.Time) (string, error) {
	config, err := w.Config()
	if err != nil {
		return "", err
	}
	bucketStart := at.UTC().Truncate(config.BucketDuration).Unix()
	return fmt.Sprintf("hot:video:%s:%d", w, bucketStart), nil
}

func (w Window) BucketKeys(at time.Time) ([]string, error) {
	config, err := w.Config()
	if err != nil {
		return nil, err
	}

	bucketCount := int(config.Duration / config.BucketDuration)
	keys := make([]string, 0, bucketCount)
	currentBucket := at.UTC().Truncate(config.BucketDuration)

	for i := 0; i < bucketCount; i++ {
		bucketStart := currentBucket.Add(-time.Duration(i) * config.BucketDuration)
		keys = append(
			keys,
			fmt.Sprintf("hot:video:%s:%d", w, bucketStart.Unix()),
		)
	}
	return keys, nil
}
