package realtime

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	NotificationChannel = "feed:realtime:notifications"
	MessageChannel      = "feed:realtime:messages"
)

type EventType string

const (
	EventNotificationCreated EventType = "notification.created"
	EventMessageCreated      EventType = "message.created"
)

var allChannels = []string{
	NotificationChannel,
	MessageChannel,
}

// Envelope 是跨实例传播的稳定事件协议。
//
// Payload 保持原始 JSON，realtime 包不需要知道通知和私信各自的数据结构。
type Envelope struct {
	ID          string          `json:"id"`
	Type        EventType       `json:"type"`
	RecipientID uint64          `json:"recipient_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Payload     json.RawMessage `json:"payload"`
}

func NewEnvelope(
	eventType EventType,
	recipientID uint64,
	payload any,
) (Envelope, error) {
	if recipientID == 0 {
		return Envelope{}, fmt.Errorf("recipient id is zero")
	}
	if _, ok := ChannelFor(eventType); !ok {
		return Envelope{}, fmt.Errorf("unsupported realtime event type %q", eventType)
	}
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, fmt.Errorf("marshal realtime payload: %w", err)
	}
	return Envelope{
		ID:          uuid.NewString(),
		Type:        eventType,
		RecipientID: recipientID,
		OccurredAt:  time.Now().UTC().Truncate(time.Millisecond),
		Payload:     rawPayload,
	}, nil
}

func ChannelFor(eventType EventType) (string, bool) {
	switch eventType {
	case EventNotificationCreated:
		return NotificationChannel, true
	case EventMessageCreated:
		return MessageChannel, true
	default:
		return "", false
	}
}

func (e Envelope) Validate() error {
	if e.ID == "" {
		return fmt.Errorf("event id is empty")
	}
	if e.RecipientID == 0 {
		return fmt.Errorf("recipient id is zero")
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("event time is zero")
	}
	if len(e.Payload) == 0 {
		return fmt.Errorf("event payload is empty")
	}
	if _, ok := ChannelFor(e.Type); !ok {
		return fmt.Errorf("unsupported realtime event type %q", e.Type)
	}
	return nil
}
