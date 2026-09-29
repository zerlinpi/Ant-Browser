package taskworker

import (
	"context"
	"errors"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	"strings"
	"testing"
	"time"
)

type healthFixture struct {
	check       proxyservice.HealthCheck
	proxy       proxyservice.Proxy
	probes      int
	failure     error
	lookupError error
	result      proxyservice.HealthResult
}

func (f *healthFixture) FindProxy(context.Context, string, string) (proxyservice.Proxy, error) {
	return f.proxy, f.lookupError
}
func (f *healthFixture) FindProxyHealthCheck(context.Context, string, string) (proxyservice.HealthCheck, error) {
	return f.check, nil
}
func (f *healthFixture) CompleteProxyHealthCheck(_ context.Context, _, _ string, result proxyservice.HealthResult, now time.Time) (proxyservice.HealthCheck, error) {
	f.check.Status = result.Status
	f.check.IP = result.IP
	f.check.ErrorCode = result.ErrorCode
	f.check.CompletedAt = &now
	return f.check, nil
}

func TestProxyHealthEndsInvalidatedChecks(t *testing.T) {
	for _, state := range []string{"disabled", "deleted", "route"} {
		t.Run(state, func(t *testing.T) {
			f, task := healthTask()
			switch state {
			case "disabled":
				f.proxy.Status = "disabled"
			case "deleted":
				f.lookupError = proxyservice.ErrNotFound
			case "route":
				f.proxy.ConnectorType = "mihomo"
				f.proxy.Kernel = "mihomo"
			}
			handler, _ := NewProxyHealthHandler(f, f)
			for i := 0; i < 2; i++ {
				if _, err := handler(context.Background(), task); err == nil {
					t.Fatal("invalidated request succeeded")
				}
			}
			if f.check.CompletedAt == nil || f.check.Status != "failed" || f.check.ErrorCode == "" || f.probes != 0 {
				t.Fatalf("invalidated request not finalized: %+v", f)
			}
		})
	}
}
func (f *healthFixture) Probe(context.Context, proxyservice.Proxy) (proxyservice.HealthResult, error) {
	f.probes++
	if f.result.Status != "" {
		return f.result, f.failure
	}
	return proxyservice.HealthResult{Status: "succeeded", IP: "203.0.113.10", LatencyMS: 20}, f.failure
}
func healthTask() (*healthFixture, taskservice.Task) {
	f := &healthFixture{check: proxyservice.HealthCheck{ID: "check", RequestID: "task", WorkspaceID: "ws", ProxyID: "proxy", ConnectorType: "xray", Kernel: "sing-box", Status: "queued"}, proxy: proxyservice.Proxy{ID: "proxy", WorkspaceID: "ws", Protocol: "hysteria2", ConnectorType: "xray", Kernel: "sing-box", Status: "active"}}
	return f, taskservice.Task{ID: "task", WorkspaceID: "ws", TaskType: "proxy.health_check", Payload: map[string]interface{}{"checkId": "check", "proxyId": "proxy", "connectorType": "xray", "kernel": "sing-box"}}
}
func TestProxyHealthReplayDoesNotProbeTwice(t *testing.T) {
	f, task := healthTask()
	handler, err := NewProxyHealthHandler(f, f)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := handler(context.Background(), task)
		if err != nil || result["status"] != "succeeded" {
			t.Fatalf("result=%v err=%v", result, err)
		}
	}
	if f.probes != 1 {
		t.Fatalf("probe count=%d", f.probes)
	}
}
func TestProxyHealthRejectsRouteChangeAndForgedTask(t *testing.T) {
	for _, change := range []string{"route", "identity"} {
		t.Run(change, func(t *testing.T) {
			f, task := healthTask()
			if change == "route" {
				f.proxy.ConnectorType = "mihomo"
				f.proxy.Kernel = "mihomo"
			} else {
				task.ID = "forged"
			}
			handler, _ := NewProxyHealthHandler(f, f)
			if _, err := handler(context.Background(), task); err == nil {
				t.Fatal("invalid task accepted")
			}
			if f.probes != 0 {
				t.Fatal("invalid task reached probe")
			}
		})
	}
}
func TestProxyHealthDoesNotLeakProbeError(t *testing.T) {
	f, task := healthTask()
	f.failure = errors.New("password=private-token")
	handler, _ := NewProxyHealthHandler(f, f)
	_, err := handler(context.Background(), task)
	var failure Failure
	if !errors.As(err, &failure) || !failure.Retryable || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("unsafe failure: %v", err)
	}
	if f.check.CompletedAt != nil {
		t.Fatal("infrastructure failure completed check")
	}
}

func TestProxyHealthPublishesStableFailureNotification(t *testing.T) {
	f, task := healthTask()
	f.check.CreatedBy = "user"
	f.result = proxyservice.HealthResult{Status: "failed"}
	publisher := &fakeNotificationPublisher{}
	handler, err := NewProxyHealthHandler(f, f, publisher)
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler(context.Background(), task)
	if err != nil || result["status"] != "failed" {
		t.Fatalf("result=%v err=%v", result, err)
	}
	inputs := publisher.snapshot()
	if len(inputs) != 1 {
		t.Fatalf("notifications=%d want=1", len(inputs))
	}
	input := inputs[0]
	if input.EventType != "proxy.health_failed" || input.RecipientUserID != "user" || input.IdempotencyKey != "proxy-health-failure:check" {
		t.Fatalf("unexpected notification: %+v", input)
	}
	if input.Payload["errorCode"] != "proxy_unreachable" || input.Payload["kernel"] != proxyservice.KernelSingBox {
		t.Fatalf("unexpected payload: %+v", input.Payload)
	}
	// A replay reuses the same idempotency key and does not probe again.
	if _, err := handler(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if f.probes != 1 || len(publisher.snapshot()) != 2 {
		t.Fatalf("probe=%d publish attempts=%d", f.probes, len(publisher.snapshot()))
	}
}
