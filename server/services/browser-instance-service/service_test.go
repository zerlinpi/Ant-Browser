package browserinstanceservice

import "testing"

func TestCommandMatchesCanonicalPayload(t *testing.T) {
	command := Command{InstanceID: "instance", Action: "instance.migrate", ExpectedVersion: 4, Payload: map[string]interface{}{"targetDeviceId": "device-a", "options": map[string]interface{}{"a": 1, "b": 2}}}
	if !command.MatchesRequest("instance", "instance.migrate", 4, map[string]interface{}{"options": map[string]interface{}{"b": float64(2), "a": float64(1)}, "targetDeviceId": "device-a"}) {
		t.Fatal("JSON-equivalent replay rejected")
	}
	for _, input := range []struct {
		id, action string
		version    int64
		payload    map[string]interface{}
	}{
		{"other", command.Action, 4, command.Payload}, {command.InstanceID, "instance.stop", 4, command.Payload},
		{command.InstanceID, command.Action, 5, command.Payload},
		{command.InstanceID, command.Action, 4, map[string]interface{}{"targetDeviceId": "device-b"}},
	} {
		if command.MatchesRequest(input.id, input.action, input.version, input.payload) {
			t.Fatal("different command reused idempotency key")
		}
	}
	command.Payload = nil
	if !command.MatchesRequest(command.InstanceID, command.Action, 4, map[string]interface{}{}) {
		t.Fatal("empty and omitted payload should match")
	}
}

func TestPendingStartAndMigrationFenceRuntimeEdits(t *testing.T) {
	for _, state := range []string{"running", "migrating"} {
		instance := BrowserInstance{DesiredState: state, ObservedState: "offline"}
		if !instance.isRuntimeActive() {
			t.Fatalf("pending %s permits runtime reconfiguration", state)
		}
	}
}
