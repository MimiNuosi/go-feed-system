package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"go-feed-system/internal/message"
	"go-feed-system/internal/notification"
	"go-feed-system/internal/realtime"
)

type realtimeSubscriberMetrics interface {
	realtime.SubscriberMetrics
}

func newRealtimeSubscriber(
	client *redis.Client,
	logger *slog.Logger,
	metrics realtimeSubscriberMetrics,
	notificationHub *notification.Hub,
	messageHub *message.Hub,
) *realtime.RedisSubscriber {
	subscriber := realtime.NewRedisSubscriber(client, logger, metrics)

	subscriber.Register(
		realtime.EventNotificationCreated,
		func(ctx context.Context, event realtime.Envelope) error {
			item, err := decodeNotificationEvent(event)
			if err != nil {
				return err
			}
			notificationHub.Broadcast(item)
			return nil
		},
	)

	subscriber.Register(
		realtime.EventMessageCreated,
		func(ctx context.Context, event realtime.Envelope) error {
			item, err := decodeMessageEvent(event)
			if err != nil {
				return err
			}
			messageHub.Broadcast(item)
			return nil
		},
	)

	return subscriber
}

func decodeNotificationEvent(event realtime.Envelope) (notification.Item, error) {
	var item notification.Item
	if err := json.Unmarshal(event.Payload, &item); err != nil {
		return notification.Item{}, fmt.Errorf("decode notification event payload: %w", err)
	}
	if item.UserID != 0 && item.UserID != event.RecipientID {
		return notification.Item{}, fmt.Errorf(
			"notification recipient mismatch: payload=%d envelope=%d",
			item.UserID,
			event.RecipientID,
		)
	}
	item.UserID = event.RecipientID
	return item, nil
}

func decodeMessageEvent(event realtime.Envelope) (message.Item, error) {
	var item message.Item
	if err := json.Unmarshal(event.Payload, &item); err != nil {
		return message.Item{}, fmt.Errorf("decode message event payload: %w", err)
	}
	if item.ReceiverID != event.RecipientID {
		return message.Item{}, fmt.Errorf(
			"message recipient mismatch: payload=%d envelope=%d",
			item.ReceiverID,
			event.RecipientID,
		)
	}
	return item, nil
}
