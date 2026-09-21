package realtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemoryPresenceUsesConnectionCompareAndDelete(t *testing.T) {
	t.Parallel()
	bus := NewMemory()
	defer bus.Close()
	now := time.Now().UTC()
	first := DevicePresence{DeviceID: "device", WorkspaceID: "workspace", NodeID: "node-a", ConnectionID: "first", ConnectedAt: now, LastSeenAt: now}
	second := DevicePresence{DeviceID: "device", WorkspaceID: "workspace", NodeID: "node-b", ConnectionID: "second", ConnectedAt: now.Add(time.Second), LastSeenAt: now.Add(time.Second)}
	if err := bus.SetPresence(context.Background(), first, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := bus.SetPresence(context.Background(), second, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := bus.RemovePresence(context.Background(), first.DeviceID, first.ConnectionID); err != nil {
		t.Fatal(err)
	}
	presence, err := bus.Presence(context.Background(), second.DeviceID)
	if err != nil || presence.ConnectionID != second.ConnectionID {
		t.Fatalf("new connection presence was removed: presence=%+v err=%v", presence, err)
	}
	if err := bus.RefreshPresence(context.Background(), first.DeviceID, first.ConnectionID, now, time.Minute); !errors.Is(err, ErrPresenceNotFound) {
		t.Fatalf("stale connection refresh error=%v", err)
	}
}

func TestMemoryCommandSubscription(t *testing.T) {
	t.Parallel()
	bus := NewMemory()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan string, 1)
	if err := bus.SubscribeCommands(ctx, func(deviceID string, payload []byte) {
		received <- deviceID + ":" + string(payload)
	}); err != nil {
		t.Fatal(err)
	}
	if err := bus.PublishCommand(context.Background(), "device", []byte("command")); err != nil {
		t.Fatal(err)
	}
	select {
	case value := <-received:
		if value != "device:command" {
			t.Fatalf("received %q", value)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive command")
	}
}

func TestMemoryNotificationSubscriptionCopiesPayload(t *testing.T) {
	t.Parallel()
	bus := NewMemory()
	defer bus.Close()
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan UserNotification, 1)
	if err := bus.SubscribeNotifications(ctx, func(value UserNotification) { received <- value }); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"type":"notification.created"}`)
	if err := bus.PublishNotification(context.Background(), UserNotification{
		WorkspaceID: "workspace", UserID: "user", Payload: payload,
	}); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'x'
	select {
	case value := <-received:
		if value.WorkspaceID != "workspace" || value.UserID != "user" || string(value.Payload) != `{"type":"notification.created"}` {
			t.Fatalf("unexpected notification: %+v", value)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive notification")
	}
	cancel()
}
