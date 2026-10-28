package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultReconnectMinBackoff = time.Second
	defaultReconnectMaxBackoff = 30 * time.Second
)

type Handler func(ctx context.Context, event Envelope) error

type SubscriberMetrics interface {
	IncRealtimeReceive(channel string, result string)
	IncRealtimeReconnect(result string)
	SetRealtimeSubscriberConnected(connected bool)
}

type RedisSubscriber struct {
	client         *redis.Client
	logger         *slog.Logger
	metrics        SubscriberMetrics
	minBackoff     time.Duration
	maxBackoff     time.Duration
	healthInterval time.Duration
	healthTimeout  time.Duration

	mu       sync.RWMutex
	handlers map[EventType]Handler
}

func NewRedisSubscriber(
	client *redis.Client,
	logger *slog.Logger,
	metrics SubscriberMetrics,
) *RedisSubscriber {
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisSubscriber{
		client:         client,
		logger:         logger,
		metrics:        metrics,
		minBackoff:     defaultReconnectMinBackoff,
		maxBackoff:     defaultReconnectMaxBackoff,
		healthInterval: 5 * time.Second,
		healthTimeout:  time.Second,
		handlers:       make(map[EventType]Handler),
	}
}

func (s *RedisSubscriber) Register(eventType EventType, handler Handler) {
	s.mu.Lock()
	s.handlers[eventType] = handler
	s.mu.Unlock()
}

// Run 保持 Redis 订阅，直到 ctx 取消。Redis 短暂断开时自行退避重连。
func (s *RedisSubscriber) Run(ctx context.Context) error {
	backoff := s.minBackoff
	for {
		connected, err := s.consume(ctx)
		s.setConnected(false)
		if ctx.Err() != nil {
			return nil
		}
		if connected {
			backoff = s.minBackoff
		}

		s.incReconnect("disconnected")
		s.logger.Warn("realtime subscriber disconnected", "error", err, "retry_in", backoff)
		if !waitBackoff(ctx, backoff) {
			return nil
		}
		backoff *= 2
		if backoff > s.maxBackoff {
			backoff = s.maxBackoff
		}
	}
}

func (s *RedisSubscriber) consume(ctx context.Context) (bool, error) {
	if s.client == nil {
		return false, fmt.Errorf("realtime redis subscriber is not configured")
	}

	pubsub := s.client.Subscribe(ctx, allChannels...)
	defer func() {
		_ = pubsub.Close()
	}()

	// Receive 等待服务器的订阅确认，确认后才标记连接就绪。
	if _, err := pubsub.Receive(ctx); err != nil {
		return false, fmt.Errorf("subscribe realtime channels: %w", err)
	}
	s.setConnected(true)
	s.incReconnect("success")

	messages := pubsub.Channel()
	healthTicker := time.NewTicker(s.healthInterval)
	defer healthTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return true, nil
		case <-healthTicker.C:
			healthCtx, cancel := context.WithTimeout(ctx, s.healthTimeout)
			err := s.client.Ping(healthCtx).Err()
			cancel()
			if err != nil {
				s.setConnected(false)
				s.incReconnect("health_error")
				s.logger.Warn("realtime redis health check failed", "error", err)
				continue
			}
			s.setConnected(true)
		case message, ok := <-messages:
			if !ok {
				if ctx.Err() != nil {
					return true, nil
				}
				return true, fmt.Errorf("realtime subscription channel closed")
			}
			s.handleMessage(ctx, message.Channel, message.Payload)
		}
	}
}

func (s *RedisSubscriber) handleMessage(ctx context.Context, channel string, raw string) {
	var event Envelope
	if err := json.Unmarshal([]byte(raw), &event); err != nil {
		s.recordReceive(channel, "decode_error")
		s.logger.Warn("decode realtime event", "channel", channel, "error", err)
		return
	}
	if err := event.Validate(); err != nil {
		s.recordReceive(channel, "invalid")
		s.logger.Warn("validate realtime event", "channel", channel, "event_id", event.ID, "error", err)
		return
	}
	expectedChannel, _ := ChannelFor(event.Type)
	if expectedChannel != channel {
		s.recordReceive(channel, "channel_mismatch")
		s.logger.Warn(
			"realtime event channel mismatch",
			"channel", channel,
			"event_type", event.Type,
			"event_id", event.ID,
		)
		return
	}

	s.mu.RLock()
	handler := s.handlers[event.Type]
	s.mu.RUnlock()
	if handler == nil {
		s.recordReceive(channel, "unhandled")
		s.logger.Warn("no realtime handler", "event_type", event.Type, "event_id", event.ID)
		return
	}
	if err := handler(ctx, event); err != nil {
		s.recordReceive(channel, "handler_error")
		s.logger.Warn(
			"handle realtime event",
			"event_type", event.Type,
			"event_id", event.ID,
			"recipient_id", event.RecipientID,
			"error", err,
		)
		return
	}
	s.recordReceive(channel, "success")
}

func (s *RedisSubscriber) setConnected(connected bool) {
	if s.metrics != nil {
		s.metrics.SetRealtimeSubscriberConnected(connected)
	}
}

func (s *RedisSubscriber) incReconnect(result string) {
	if s.metrics != nil {
		s.metrics.IncRealtimeReconnect(result)
	}
}

func (s *RedisSubscriber) recordReceive(channel string, result string) {
	if s.metrics != nil {
		s.metrics.IncRealtimeReceive(channel, result)
	}
}

func waitBackoff(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
