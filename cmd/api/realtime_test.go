package main

import (
	"encoding/json"
	"testing"

	"go-feed-system/internal/message"
	"go-feed-system/internal/notification"
	"go-feed-system/internal/realtime"
)

func TestDecodeRealtimeEvents(t *testing.T) {
	t.Run("通知事件补回接收者", func(t *testing.T) {
		payload, _ := json.Marshal(notification.Item{
			ID:   1,
			Kind: "like",
		})
		item, err := decodeNotificationEvent(realtime.Envelope{
			RecipientID: 7,
			Payload:     payload,
		})
		if err != nil {
			t.Fatalf("decode notification: %v", err)
		}
		if item.UserID != 7 || item.Kind != "like" {
			t.Fatalf("notification = %+v", item)
		}
	})

	t.Run("私信接收者一致", func(t *testing.T) {
		payload, _ := json.Marshal(message.Item{
			ID:         2,
			SenderID:   1,
			ReceiverID: 7,
			Content:    "hello",
		})
		item, err := decodeMessageEvent(realtime.Envelope{
			RecipientID: 7,
			Payload:     payload,
		})
		if err != nil {
			t.Fatalf("decode message: %v", err)
		}
		if item.Content != "hello" {
			t.Fatalf("message = %+v", item)
		}
	})

	t.Run("私信接收者不一致", func(t *testing.T) {
		payload, _ := json.Marshal(message.Item{
			ID:         2,
			ReceiverID: 8,
		})
		if _, err := decodeMessageEvent(realtime.Envelope{
			RecipientID: 7,
			Payload:     payload,
		}); err == nil {
			t.Fatal("expected recipient mismatch error")
		}
	})
}
