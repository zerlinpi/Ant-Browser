package browseragent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type executorFunc func(context.Context, Execution) error

func (f executorFunc) Execute(ctx context.Context, input Execution) error { return f(ctx, input) }

func TestCloudWorkflowClaimStartExecuteReport(t *testing.T) {
	for _, scenario := range []string{"success", "cdp", "failure", "start-retry", "report-retry", "tampered", "foreign", "expired", "start-denied", "report-denied"} {
		t.Run(scenario, func(t *testing.T) {
			device, workspace := uuid.NewString(), uuid.NewString()
			claim := claimedExecution{}
			task := &claim.Lease.Task
			task.ID = uuid.NewString()
			task.WorkspaceID = workspace
			task.WorkflowID = uuid.NewString()
			task.WorkflowVersionID = uuid.NewString()
			task.LeaseOwner = "device:" + device
			task.TaskType = "workflow.execute"
			task.Payload.InstanceID = uuid.NewString()
			claim.Lease.RunID = uuid.NewString()
			claim.Lease.AttemptID = uuid.NewString()
			claim.Lease.ExpiresAt = time.Now().Add(time.Minute)
			claim.Version.ID = task.WorkflowVersionID
			claim.Version.WorkspaceID = workspace
			claim.Version.WorkflowID = task.WorkflowID
			if err := json.Unmarshal([]byte(`{"schemaVersion":"ant-workflow/v1","engine":"playwright","steps":[{"id":"wait","action":"wait","timeoutMs":30000,"parameters":{"durationMs":1}}]}`), &claim.Version.Definition); err != nil {
				t.Fatal(err)
			}
			if scenario == "cdp" {
				claim.Version.Definition.Engine = "cdp"
			}
			canonical, _ := json.Marshal(claim.Version.Definition)
			hash := sha256.Sum256(canonical)
			claim.Version.ContentHash = hex.EncodeToString(hash[:])
			switch scenario {
			case "tampered":
				claim.Version.ContentHash = "bad"
			case "foreign":
				task.WorkspaceID = uuid.NewString()
			case "expired":
				claim.Lease.ExpiresAt = time.Now().Add(-time.Minute)
			}
			reports := []string{}
			reportAttempts := map[string]int{}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Device private" || r.Header.Get("X-Device-ID") != device {
					t.Error("device headers missing")
				}
				if r.URL.Path == "/api/v1/agent/tasks/claim" {
					json.NewEncoder(w).Encode(map[string]interface{}{"data": claim})
					return
				}
				var report map[string]string
				if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
					t.Error(err)
				}
				reports = append(reports, report["status"])
				reportAttempts[report["status"]]++
				if report["runId"] != claim.Lease.RunID || report["attemptId"] != claim.Lease.AttemptID {
					t.Error("wrong report identity")
				}
				if scenario == "start-denied" || (scenario == "report-denied" && report["status"] != "running") {
					w.WriteHeader(409)
					return
				}
				if (scenario == "start-retry" && report["status"] == "running" && reportAttempts["running"] == 1) ||
					(scenario == "report-retry" && report["status"] == "succeeded" && reportAttempts["succeeded"] == 1) {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			runs := 0
			client, err := New(Config{BaseURL: server.URL, DeviceID: device, WorkspaceID: workspace, Credential: "private"}, executorFunc(func(ctx context.Context, input Execution) error {
				runs++
				if input.InstanceID != task.Payload.InstanceID || input.TaskID != task.ID {
					t.Error("execution target mismatch")
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("unbounded execution")
				}
				if scenario == "failure" {
					return errors.New("private browser error")
				}
				return nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			client.http.Transport = server.Client().Transport
			didRun, err := client.PollOnce(context.Background())
			switch scenario {
			case "success", "cdp", "failure", "start-retry", "report-retry":
				if err != nil || !didRun || runs != 1 {
					t.Fatalf("execution=%v runs=%d err=%v", didRun, runs, err)
				}
				terminal := "succeeded"
				if scenario == "failure" {
					terminal = "failed"
				}
				wantReports := []string{"running", terminal}
				if scenario == "start-retry" {
					wantReports = []string{"running", "running", terminal}
				}
				if scenario == "report-retry" {
					wantReports = []string{"running", terminal, terminal}
				}
				if !reflect.DeepEqual(reports, wantReports) {
					t.Fatalf("reports=%v", reports)
				}
			case "report-denied":
				if err == nil || !didRun || runs != 1 {
					t.Fatalf("report failure rerun: %d %v", runs, err)
				}
			default:
				if err == nil || didRun || runs != 0 {
					t.Fatalf("unsafe execution: %d %v", runs, err)
				}
			}
		})
	}
}

func TestRejectUnsafeCloudEndpoint(t *testing.T) {
	for _, endpoint := range []string{"http://example.test", "https://user:password@example.test", "https://example.test?token=secret"} {
		if _, err := New(Config{BaseURL: endpoint}, executorFunc(func(context.Context, Execution) error { return nil })); err == nil {
			t.Fatalf("unsafe endpoint accepted: %s", endpoint)
		}
	}
}

func TestClientCanonicalizesConfiguredUUIDs(t *testing.T) {
	device := uuid.NewString()
	workspace := uuid.NewString()
	client, err := New(Config{
		BaseURL:     "https://example.test",
		DeviceID:    strings.ToUpper(device),
		WorkspaceID: strings.ToUpper(workspace),
		Credential:  "private",
	}, executorFunc(func(context.Context, Execution) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if client.deviceID != device || client.workspaceID != workspace {
		t.Fatalf("UUIDs were not canonicalized: %q %q", client.deviceID, client.workspaceID)
	}
}

func TestRunRetriesOnlyTransientClaimFailures(t *testing.T) {
	device, workspace := uuid.NewString(), uuid.NewString()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	claims := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims++
		if claims == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		cancel()
	}))
	defer server.Close()
	client, err := New(Config{BaseURL: server.URL, DeviceID: device, WorkspaceID: workspace, Credential: "private"}, executorFunc(func(context.Context, Execution) error {
		t.Fatal("unexpected execution")
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = server.Client().Transport
	if err := client.Run(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("runner did not stop with cancellation: %v", err)
	}
	if claims != 2 {
		t.Fatalf("transient claim was not retried exactly once: %d", claims)
	}
}

func TestRedirectDoesNotForwardDeviceCredentials(t *testing.T) {
	forwarded := false
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true; w.WriteHeader(204) }))
	defer destination.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client, err := New(Config{BaseURL: source.URL, DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "private"}, executorFunc(func(context.Context, Execution) error { t.Fatal("unexpected execution"); return nil }))
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = source.Client().Transport
	if _, err := client.PollOnce(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	if forwarded {
		t.Fatal("device credentials forwarded")
	}
}

func TestRunStopsBeforeClaimWhenCancelled(t *testing.T) {
	client, err := New(Config{BaseURL: "https://example.test", DeviceID: uuid.NewString(), WorkspaceID: uuid.NewString(), Credential: "private"}, executorFunc(func(context.Context, Execution) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := client.Run(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled runner: %v", err)
	}
}
