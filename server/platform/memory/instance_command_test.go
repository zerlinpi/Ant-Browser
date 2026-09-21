package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	instances "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
	devices "github.com/zerlinpi/Ant-Browser/server/services/device-service"
)

func TestMigrationCompletionRejectsRevokedOrSupersededTarget(t *testing.T) {
	for _, scenario := range []string{"valid", "revoked_target", "superseded", "reassigned", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			store := New()
			now := time.Now().UTC()
			device := devices.Device{ID: "destination", WorkspaceID: "workspace"}
			instance := instances.BrowserInstance{ID: "instance", WorkspaceID: "workspace", AssignedDeviceID: "source", DesiredState: "migrating", Version: 2}
			command := instances.Command{ID: "command", WorkspaceID: "workspace", InstanceID: instance.ID, DeviceID: "source", Action: "instance.migrate", Status: "running", Payload: map[string]interface{}{"targetDeviceId": device.ID}}
			switch scenario {
			case "revoked_target":
				device.RevokedAt = &now
			case "superseded":
				instance.DesiredState = "stopped"
			case "reassigned":
				instance.AssignedDeviceID = "another"
			case "deleted":
				instance.DeletedAt = &now
			}
			store.devices[device.ID] = device
			store.instances[instance.ID] = instance
			store.commands[command.ID] = command
			_, err := store.TransitionCommand(context.Background(), "workspace", "source", command.ID, "completed", "", "", now)
			if scenario == "valid" {
				if err != nil || store.instances[instance.ID].AssignedDeviceID != device.ID || store.commands[command.ID].Status != "completed" {
					t.Fatalf("valid completion failed: %v", err)
				}
			} else {
				if !errors.Is(err, instances.ErrStateConflict) {
					t.Fatalf("unsafe completion: %v", err)
				}
				if store.commands[command.ID].Status != "running" || store.instances[instance.ID].AssignedDeviceID != instance.AssignedDeviceID {
					t.Fatal("failed completion mutated state")
				}
			}
		})
	}
}

func TestPendingCommandsExpireBeforeDispatch(t *testing.T) {
	store := New()
	now := time.Now().UTC()
	store.commands["expired"] = instances.Command{ID: "expired", WorkspaceID: "workspace", DeviceID: "device", Status: "pending", Deadline: now.Add(-time.Second)}
	store.commands["active"] = instances.Command{ID: "active", WorkspaceID: "workspace", DeviceID: "device", Status: "pending", Deadline: now.Add(time.Minute)}
	service := instances.New(store, reportAuthorizer{})
	items, err := service.PendingCommands(context.Background(), "workspace", "device")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "active" {
		t.Fatalf("pending=%+v", items)
	}
	expired := store.commands["expired"]
	if expired.Status != "expired" || expired.FailureCode != "command_expired" || expired.CompletedAt == nil {
		t.Fatalf("expired=%+v", expired)
	}
}
