package realtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
)

type Publisher interface {
	Publish(ctx context.Context, event Envelope) error
}

type PublishMetrics interface {
	IncRealtimePublish(channel string, result string)
}

type RedisPublisher struct {
	client  *redis.Client
	metrics PublishMetrics
}

func NewRedisPublisher(client *redis.Client, metrics PublishMetrics) *RedisPublisher {
	return &RedisPublisher{client: client, metrics: metrics}
}

func (p *RedisPublisher) Publish(ctx context.Context, event Envelope) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("realtime redis publisher is not configured")
	}
	if err := event.Validate(); err != nil {
		p.recordResult("", "invalid")
		return fmt.Errorf("validate realtime event: %w", err)
	}
	channel, _ := ChannelFor(event.Type)
	payload, err := json.Marshal(event)
	if err != nil {
		p.recordResult(channel, "error")
		return fmt.Errorf("marshal realtime event: %w", err)
	}
	if err := p.client.Publish(ctx, channel, payload).Err(); err != nil {
		p.recordResult(channel, "error")
		return fmt.Errorf("publish realtime event: %w", err)
	}
	p.recordResult(channel, "success")
	return nil
}

func (p *RedisPublisher) recordResult(channel string, result string) {
	if p != nil && p.metrics != nil {
		p.metrics.IncRealtimePublish(channel, result)
	}
}
