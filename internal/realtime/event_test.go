package realtime

import (
	"encoding/json"
	"testing"
)

func TestNewEnvelope(t *testing.T) {
	tests := []struct {
		name        string
		eventType   EventType
		recipientID uint64
		payload     any
		wantErr     bool
		wantChannel string
	}{
		{
			name:        "通知事件",
			eventType:   EventNotificationCreated,
			recipientID: 2,
			payload:     map[string]string{"kind": "like"},
			wantChannel: NotificationChannel,
		},
		{
			name:        "私信事件",
			eventType:   EventMessageCreated,
			recipientID: 2,
			payload:     map[string]string{"content": "hello"},
			wantChannel: MessageChannel,
		},
		{
			name:        "接收者为零",
			eventType:   EventMessageCreated,
			recipientID: 0,
			payload:     map[string]string{},
			wantErr:     true,
		},
		{
			name:        "未知事件类型",
			eventType:   EventType("unknown"),
			recipientID: 2,
			payload:     map[string]string{},
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event, err := NewEnvelope(tt.eventType, tt.recipientID, tt.payload)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewEnvelope() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if event.ID == "" || event.OccurredAt.IsZero() || len(event.Payload) == 0 {
				t.Fatalf("incomplete envelope: %+v", event)
			}
			channel, ok := ChannelFor(event.Type)
			if !ok || channel != tt.wantChannel {
				t.Fatalf("channel = %q, ok=%v, want %q", channel, ok, tt.wantChannel)
			}
			var payload map[string]string
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("decode payload: %v", err)
			}
		})
	}
}

func TestEnvelopeValidate(t *testing.T) {
	valid := Envelope{
		ID:          "event-1",
		Type:        EventNotificationCreated,
		RecipientID: 2,
		OccurredAt:  testTime(),
		Payload:     json.RawMessage(`{"id":1}`),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}

	invalid := valid
	invalid.RecipientID = 0
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected zero recipient error")
	}
}
