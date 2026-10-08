package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

const (
	defaultReconnectMinBackoff = time.Second
	defaultReconnectMaxBackoff = 30 * time.Second
)

type managedConsumer interface {
	Consume(ctx context.Context) error
}

type consumerSession interface {
	Consumer() managedConsumer
	Ready(ctx context.Context) error
	Close() error
}

type consumerConnector interface {
	Connect(ctx context.Context) (consumerSession, error)
}

type consumerSupervisorMetrics interface {
	IncConsumerReconnect(result string)
	SetConsumerConnected(connected bool)
}

// ConsumerSupervisor 负责维护一个可重连的 RabbitMQ Consumer Session。
type ConsumerSupervisor struct {
	connector  consumerConnector
	minBackoff time.Duration
	maxBackoff time.Duration
	logger     *slog.Logger
	metrics    consumerSupervisorMetrics

	mu      sync.RWMutex
	session consumerSession
}

func NewConsumerSupervisor(
	connector consumerConnector,
	minBackoff time.Duration,
	maxBackoff time.Duration,
	logger *slog.Logger,
	metrics consumerSupervisorMetrics,
) *ConsumerSupervisor {
	if minBackoff <= 0 {
		minBackoff = defaultReconnectMinBackoff
	}
	if maxBackoff < minBackoff {
		maxBackoff = defaultReconnectMaxBackoff
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &ConsumerSupervisor{
		connector:  connector,
		minBackoff: minBackoff,
		maxBackoff: maxBackoff,
		logger:     logger,
		metrics:    metrics,
	}
}

// Run 持续维护 Consumer Session，直到 ctx 被取消。
func (s *ConsumerSupervisor) Run(ctx context.Context) error {
	backoff := s.minBackoff
	recovering := false

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		session, err := s.connector.Connect(ctx)
		if err != nil {
			if err := ctx.Err(); err != nil {
				return nil
			}
			s.logger.Warn("failed to connect consumer", "error", err)
			s.incReconnect("connect_error")
			s.setConnected(false)
			recovering = true
			if !s.waitBackoff(ctx, backoff) {
				return nil
			}
			backoff = nextReconnectBackoff(backoff, s.maxBackoff)
			continue
		}
		backoff = s.minBackoff
		if recovering {
			s.incReconnect("success")
			recovering = false
		}
		s.replaceSession(session)
		s.setConnected(true)

		err = session.Consumer().Consume(ctx)
		s.clearSession(session)
		s.setConnected(false)
		if closeErr := session.Close(); closeErr != nil {
			s.logger.Warn("failed to close consumer session", "error", closeErr)
		}
		if err := ctx.Err(); err != nil {
			return nil
		}
		s.incReconnect("session_exit")
		recovering = true

		if err == nil {
			s.logger.Warn("consumer session stopped unexpectedly")
		} else {
			s.logger.Warn("consumer session exited with error", "error", err)
		}
		if !s.waitBackoff(ctx, backoff) {
			return nil
		}
		backoff = nextReconnectBackoff(backoff, s.maxBackoff)
	}
}

func (s *ConsumerSupervisor) Ready(ctx context.Context) error {
	s.mu.RLock()
	session := s.session
	s.mu.RUnlock()

	if session == nil {
		return fmt.Errorf("consumer session is not connected")
	}
	return session.Ready(ctx)
}

func (s *ConsumerSupervisor) Close() error {
	s.mu.Lock()
	session := s.session
	s.session = nil
	s.mu.Unlock()
	s.setConnected(false)

	if session == nil {
		return nil
	}
	return session.Close()
}

func (s *ConsumerSupervisor) incReconnect(result string) {
	if s.metrics != nil {
		s.metrics.IncConsumerReconnect(result)
	}
}

func (s *ConsumerSupervisor) setConnected(connected bool) {
	if s.metrics != nil {
		s.metrics.SetConsumerConnected(connected)
	}
}

func (s *ConsumerSupervisor) replaceSession(session consumerSession) {
	s.mu.Lock()
	s.session = session
	s.mu.Unlock()
}

func (s *ConsumerSupervisor) clearSession(session consumerSession) {
	s.mu.Lock()
	if s.session == session {
		s.session = nil
	}
	s.mu.Unlock()
}

func nextReconnectBackoff(current, max time.Duration) time.Duration {
	if current <= 0 {
		return defaultReconnectMinBackoff
	}
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func (s *ConsumerSupervisor) waitBackoff(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		d = defaultReconnectMinBackoff
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
