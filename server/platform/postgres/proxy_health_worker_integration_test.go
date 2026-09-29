package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zerlinpi/Ant-Browser/server/platform/secureenvelope"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	taskworker "github.com/zerlinpi/Ant-Browser/server/services/task-worker"
	workspaceservice "github.com/zerlinpi/Ant-Browser/server/services/workspace-service"
)

// credentialProbe stands in for runtimeprobe: like the real probe it
// resolves the proxy credential through a ProxyProvider, here backed by the
// ant_worker store, and then reports a canned result.
type credentialProbe struct {
	secrets  *secureenvelope.ProxyProvider
	password string
	result   proxyservice.HealthResult
	// outage, when set, is returned after the credential resolved, as when
	// the probe runtime itself fails.
	outage error

	mu    sync.Mutex
	calls int
	err   error
}

func (p *credentialProbe) Probe(ctx context.Context, proxy proxyservice.Proxy) (proxyservice.HealthResult, error) {
	secret, err := p.secrets.Resolve(ctx, proxy.WorkspaceID, proxy.ID, proxy.SecretRef)
	if err == nil && secret.Password != p.password {
		err = errors.New("resolved credential does not match")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.err = err
	if err != nil {
		return proxyservice.HealthResult{}, err
	}
	if p.outage != nil {
		return proxyservice.HealthResult{}, p.outage
	}
	return p.result, nil
}

func (p *credentialProbe) snapshot() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls, p.err
}

// leasedQueue hands one lease that the test claimed with ClaimNextTask to the
// real task worker. The worker then starts the task, runs the handler under
// the task's tenant scope, and completes or fails it through the task
// service backed by the ant_worker store. idle closes once the worker asks
// for more work, that is after it finished the lease.
type leasedQueue struct {
	*taskservice.Service
	mu    sync.Mutex
	lease *taskservice.Lease
	idle  chan struct{}
}

func (q *leasedQueue) Claim(context.Context, string, []string, time.Duration) (taskservice.Lease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.lease != nil {
		lease := *q.lease
		q.lease = nil
		return lease, nil
	}
	select {
	case <-q.idle:
	default:
		close(q.idle)
	}
	return taskservice.Lease{}, taskservice.ErrNoWork
}

// TestProxyHealthCheckAsWorkerAgainstPostgres runs a proxy health check end
// to end: the control plane stores an authenticated proxy and requests a
// check, and the worker, connected as ant_worker, claims the task, resolves
// the credential, records the result and publishes failure notifications.
func TestProxyHealthCheckAsWorkerAgainstPostgres(t *testing.T) {
	env := newIntegrationEnv(t)
	crypto, err := secureenvelope.New("MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=", "integration-proxy", "v1")
	if err != nil {
		t.Fatal(err)
	}
	controlSecrets, err := secureenvelope.NewProxyProvider(env.Store, crypto)
	if err != nil {
		t.Fatal(err)
	}
	proxies := proxyservice.New(env.Store, workspaceservice.New(env.Store), controlSecrets)
	workerSecrets, err := secureenvelope.NewProxyProvider(env.Worker, crypto)
	if err != nil {
		t.Fatal(err)
	}
	notifications := notificationservice.New(env.Worker, nil)
	const workerID = "integration-proxy-worker"
	suffix := uuid.NewString()[:8]

	// request stores a proxy the way the gateway does and queues a check.
	request := func(t *testing.T, input proxyservice.CreateInput) (proxyservice.Proxy, proxyservice.HealthCheck) {
		t.Helper()
		input.Name += " " + suffix
		proxy, err := proxies.Create(env.Tenant, env.Owner.ID, env.Workspace.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		if !proxy.HasCredentials || proxy.SecretRef == "" {
			t.Fatalf("proxy stored without a credential reference: %+v", proxy)
		}
		check, err := proxies.RequestHealthCheck(env.Tenant, env.Owner.ID, env.Workspace.ID, proxy.ID)
		if err != nil {
			t.Fatal(err)
		}
		return proxy, check
	}
	// claimAt takes the check's task as ant_worker at the given clock. The
	// claim spans workspaces; tasks left by an earlier run of a reused
	// database are skipped and their leases expire.
	claimAt := func(t *testing.T, taskID string, now time.Time) taskservice.Lease {
		t.Helper()
		for attempt := 0; attempt < 50; attempt++ {
			lease, err := env.Worker.ClaimNextTask(env.Ctx, workerID, []string{"proxy.health_check"}, time.Minute, now)
			if err != nil {
				t.Fatalf("claim as ant_worker: %v", err)
			}
			if lease.Task.ID == taskID {
				return lease
			}
		}
		t.Fatalf("task %s was not claimed", taskID)
		return taskservice.Lease{}
	}
	claim := func(t *testing.T, taskID string) taskservice.Lease {
		t.Helper()
		return claimAt(t, taskID, time.Now().UTC())
	}
	// process runs the claimed lease through the real worker and handler.
	process := func(t *testing.T, lease taskservice.Lease, probe *credentialProbe) {
		t.Helper()
		handler, err := taskworker.NewProxyHealthHandler(env.Worker, probe, notifications)
		if err != nil {
			t.Fatal(err)
		}
		var logs bytes.Buffer
		queue := &leasedQueue{Service: taskservice.New(env.Worker, nil), lease: &lease, idle: make(chan struct{})}
		worker, err := taskworker.New(queue, workerID, map[string]taskworker.Handler{"proxy.health_check": handler},
			time.Minute, time.Hour, 1, slog.New(slog.NewTextHandler(&logs, nil)), notifications)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(env.Ctx)
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			_ = worker.Run(ctx, nil)
		}()
		select {
		case <-queue.idle:
		case <-time.After(30 * time.Second):
			t.Error("the worker did not finish the lease")
		}
		cancel()
		<-stopped
		if strings.Contains(logs.String(), "level=ERROR") {
			t.Errorf("worker logged errors:\n%s", logs.String())
		}
	}
	type sample struct {
		success   bool
		latency   *int
		ip        string
		errorCode string
		connector string
	}
	samples := func(t *testing.T, proxyID string) []sample {
		t.Helper()
		rows, err := env.Pool.Query(env.Ctx, `SELECT success, latency_ms, COALESCE(host(public_ip), ''), error_code, connector_type
			FROM proxy_health_samples WHERE workspace_id = $1::uuid AND proxy_id = $2::uuid ORDER BY checked_at`, env.Workspace.ID, proxyID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var items []sample
		for rows.Next() {
			var item sample
			if err := rows.Scan(&item.success, &item.latency, &item.ip, &item.errorCode, &item.connector); err != nil {
				t.Fatal(err)
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return items
	}
	notificationsFor := func(t *testing.T, key, value string) []notificationservice.Notification {
		t.Helper()
		items, err := env.Store.ListNotifications(env.Tenant, env.Workspace.ID, env.Owner.ID, 200, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		var matched []notificationservice.Notification
		for _, item := range items {
			if item.Payload[key] == value {
				matched = append(matched, item)
			}
		}
		return matched
	}
	finished := func(t *testing.T, taskID string) taskservice.Task {
		t.Helper()
		task, err := env.Store.FindTask(env.Tenant, env.Workspace.ID, taskID)
		if err != nil {
			t.Fatal(err)
		}
		return task
	}

	t.Run("healthy exit through the xray kernel", func(t *testing.T) {
		proxy, check := request(t, proxyservice.CreateInput{
			Name: "Residential", Protocol: "socks5", Host: "proxy.example.test", Port: 1080, Username: "seller",
			Secret: &proxyservice.SecretInput{Password: "socks-password"}, ConnectorType: proxyservice.ConnectorXray,
		})
		if proxy.Kernel != proxyservice.KernelXray {
			t.Fatalf("kernel=%s", proxy.Kernel)
		}
		probe := &credentialProbe{secrets: workerSecrets, password: "socks-password", result: proxyservice.HealthResult{Status: "succeeded", IP: "203.0.113.10", LatencyMS: 42}}
		process(t, claim(t, check.RequestID), probe)

		if calls, err := probe.snapshot(); calls != 1 || err != nil {
			t.Fatalf("probe calls=%d credential error=%v", calls, err)
		}
		if task := finished(t, check.RequestID); task.Status != "succeeded" || task.CompletedAt == nil {
			t.Fatalf("task=%+v", task)
		}
		saved, err := proxies.GetHealthCheck(env.Tenant, env.Owner.ID, env.Workspace.ID, check.ID)
		if err != nil || saved.Status != "succeeded" || saved.IP != "203.0.113.10" || saved.LatencyMS != 42 || saved.ErrorCode != "" || saved.CompletedAt == nil {
			t.Fatalf("health check=%+v err=%v", saved, err)
		}
		current, err := proxies.Get(env.Tenant, env.Owner.ID, env.Workspace.ID, proxy.ID)
		if err != nil || current.Status != "active" || current.Version != proxy.Version+1 {
			t.Fatalf("proxy=%+v err=%v", current, err)
		}
		if got := samples(t, proxy.ID); len(got) != 1 || !got[0].success || got[0].latency == nil || *got[0].latency != 42 || got[0].ip != "203.0.113.10" || got[0].connector != "xray" {
			t.Fatalf("samples=%+v", got)
		}
		if items := notificationsFor(t, "checkId", check.ID); len(items) != 0 {
			t.Fatalf("a healthy check notified: %+v", items)
		}
	})

	t.Run("unreachable exit through the sing-box kernel", func(t *testing.T) {
		proxy, check := request(t, proxyservice.CreateInput{
			Name: "Hysteria", Protocol: "hysteria2", Host: "hy2.example.test", Port: 8443,
			Secret: &proxyservice.SecretInput{Password: "hy2-password"}, ConnectorType: proxyservice.ConnectorXray,
		})
		if proxy.Kernel != proxyservice.KernelSingBox {
			t.Fatalf("kernel=%s", proxy.Kernel)
		}
		probe := &credentialProbe{secrets: workerSecrets, password: "hy2-password", result: proxyservice.HealthResult{Status: "failed", ErrorMessage: "dial tcp: connection refused"}}
		process(t, claim(t, check.RequestID), probe)

		if calls, err := probe.snapshot(); calls != 1 || err != nil {
			t.Fatalf("probe calls=%d credential error=%v", calls, err)
		}
		// The probe ran and reported: the task succeeds, the check failed.
		if task := finished(t, check.RequestID); task.Status != "succeeded" {
			t.Fatalf("task=%+v", task)
		}
		saved, err := proxies.GetHealthCheck(env.Tenant, env.Owner.ID, env.Workspace.ID, check.ID)
		if err != nil || saved.Status != "failed" || saved.ErrorCode != "proxy_unreachable" || saved.ErrorMessage != "" || saved.IP != "" {
			t.Fatalf("health check=%+v err=%v", saved, err)
		}
		if current, err := proxies.Get(env.Tenant, env.Owner.ID, env.Workspace.ID, proxy.ID); err != nil || current.Status != "unhealthy" {
			t.Fatalf("proxy=%+v err=%v", current, err)
		}
		if got := samples(t, proxy.ID); len(got) != 1 || got[0].success || got[0].errorCode != "proxy_unreachable" || got[0].connector != "xray" {
			t.Fatalf("samples=%+v", got)
		}
		items := notificationsFor(t, "checkId", check.ID)
		if len(items) != 1 || items[0].EventType != "proxy.health_failed" || items[0].RecipientUserID != env.Owner.ID ||
			items[0].Payload["errorCode"] != "proxy_unreachable" || items[0].Payload["kernel"] != "sing-box" || items[0].Payload["connectorType"] != "xray" {
			t.Fatalf("notifications=%+v", items)
		}
		var inApp, websocket, email int
		if err := env.Pool.QueryRow(env.Ctx, `SELECT
			count(*) FILTER (WHERE channel = 'in_app' AND status = 'sent'),
			count(*) FILTER (WHERE channel = 'websocket' AND status = 'pending'),
			count(*) FILTER (WHERE channel = 'email')
			FROM notification_deliveries WHERE workspace_id = $1::uuid AND notification_id = $2::uuid`, env.Workspace.ID, items[0].ID).Scan(&inApp, &websocket, &email); err != nil {
			t.Fatal(err)
		}
		if inApp != 1 || websocket != 1 || email != 0 {
			t.Fatalf("deliveries in_app=%d websocket=%d email=%d", inApp, websocket, email)
		}
	})

	t.Run("disabled proxy fails the task", func(t *testing.T) {
		proxy, check := request(t, proxyservice.CreateInput{
			Name: "Mihomo", Protocol: "vless", Host: "vless.example.test", Port: 443,
			Secret: &proxyservice.SecretInput{Token: "vless-uuid"}, ConnectorType: proxyservice.ConnectorMihomo,
		})
		if _, err := proxies.Update(env.Tenant, env.Owner.ID, env.Workspace.ID, proxy.ID, proxyservice.UpdateInput{
			Name: proxy.Name, Protocol: proxy.Protocol, Host: proxy.Host, Port: proxy.Port,
			ConnectorType: proxy.ConnectorType, Status: "disabled", Version: proxy.Version,
		}); err != nil {
			t.Fatal(err)
		}
		probe := &credentialProbe{secrets: workerSecrets, result: proxyservice.HealthResult{Status: "succeeded", IP: "203.0.113.11"}}
		lease := claim(t, check.RequestID)
		process(t, lease, probe)

		if calls, _ := probe.snapshot(); calls != 0 {
			t.Fatalf("a disabled proxy was probed %d times", calls)
		}
		if task := finished(t, check.RequestID); task.Status != "failed" || task.ErrorCode != "proxy_disabled" {
			t.Fatalf("task=%+v", task)
		}
		saved, err := proxies.GetHealthCheck(env.Tenant, env.Owner.ID, env.Workspace.ID, check.ID)
		if err != nil || saved.Status != "failed" || saved.ErrorCode != "proxy_disabled" || saved.CompletedAt == nil {
			t.Fatalf("health check=%+v err=%v", saved, err)
		}
		if current, err := proxies.Get(env.Tenant, env.Owner.ID, env.Workspace.ID, proxy.ID); err != nil || current.Status != "disabled" {
			t.Fatalf("proxy=%+v err=%v", current, err)
		}
		if got := samples(t, proxy.ID); len(got) != 0 {
			t.Fatalf("an administrative failure was sampled: %+v", got)
		}
		items := notificationsFor(t, "taskId", check.RequestID)
		if len(items) != 1 || items[0].EventType != "proxy.health_failed" || items[0].Payload["errorCode"] != "proxy_disabled" {
			t.Fatalf("terminal failure notifications=%+v", items)
		}
	})

	t.Run("probe outage is retried", func(t *testing.T) {
		proxy, check := request(t, proxyservice.CreateInput{
			Name: "Shadowsocks", Protocol: "shadowsocks", Host: "ss.example.test", Port: 8388,
			Secret: &proxyservice.SecretInput{Password: "ss-password"}, ConnectorType: proxyservice.ConnectorXray,
		})
		outage := &credentialProbe{secrets: workerSecrets, password: "ss-password", outage: errors.New("probe runtime exited: password=ss-password")}
		process(t, claim(t, check.RequestID), outage)
		task := finished(t, check.RequestID)
		if task.Status != "queued" || task.ErrorCode != "proxy_probe_unavailable" || strings.Contains(task.ErrorMessage, "ss-password") || !task.AvailableAt.After(time.Now().UTC()) {
			t.Fatalf("task after a probe outage=%+v", task)
		}
		if saved, err := proxies.GetHealthCheck(env.Tenant, env.Owner.ID, env.Workspace.ID, check.ID); err != nil || saved.CompletedAt != nil || saved.Status != "queued" {
			t.Fatalf("health check after a probe outage=%+v err=%v", saved, err)
		}
		if items := notificationsFor(t, "taskId", check.RequestID); len(items) != 0 {
			t.Fatalf("a retryable failure notified: %+v", items)
		}

		retry := claimAt(t, check.RequestID, task.AvailableAt.Add(time.Second))
		if retry.Attempt != 2 {
			t.Fatalf("retry attempt=%d", retry.Attempt)
		}
		healthy := &credentialProbe{secrets: workerSecrets, password: "ss-password", result: proxyservice.HealthResult{Status: "succeeded", IP: "2001:db8::10", LatencyMS: 7}}
		process(t, retry, healthy)
		if task := finished(t, check.RequestID); task.Status != "succeeded" {
			t.Fatalf("retried task=%+v", task)
		}
		saved, err := proxies.GetHealthCheck(env.Tenant, env.Owner.ID, env.Workspace.ID, check.ID)
		if err != nil || saved.Status != "succeeded" || saved.IP != "2001:db8::10" {
			t.Fatalf("health check after the retry=%+v err=%v", saved, err)
		}
		if got := samples(t, proxy.ID); len(got) != 1 || got[0].ip != "2001:db8::10" {
			t.Fatalf("samples=%+v", got)
		}
	})

	// Every lease expires without a result, as when the worker process dies
	// mid-probe. The claim after the last allowed attempt dead-letters the
	// task under the task_claim operation, which has no workspace scope; the
	// check must still be finalized (migration 032).
	t.Run("a lost worker exhausts the retries", func(t *testing.T) {
		_, check := request(t, proxyservice.CreateInput{
			Name: "Trojan", Protocol: "trojan", Host: "trojan.example.test", Port: 443,
			Secret: &proxyservice.SecretInput{Password: "trojan-password"}, ConnectorType: proxyservice.ConnectorXray,
		})
		clock := time.Now().UTC()
		lease := claimAt(t, check.RequestID, clock)
		for attempt := 2; attempt <= lease.Task.RetryLimit+1; attempt++ {
			clock = clock.Add(2 * time.Minute)
			if next := claimAt(t, check.RequestID, clock); next.Attempt != attempt {
				t.Fatalf("attempt=%d, want %d", next.Attempt, attempt)
			}
		}
		clock = clock.Add(2 * time.Minute)
		for claims := 0; ; claims++ {
			lease, err := env.Worker.ClaimNextTask(env.Ctx, workerID, []string{"proxy.health_check"}, time.Minute, clock)
			if errors.Is(err, taskservice.ErrNoWork) {
				break
			}
			if err != nil || lease.Task.ID == check.RequestID || claims > 50 {
				t.Fatalf("claim past the retry limit lease=%+v err=%v", lease.Task, err)
			}
		}
		if task := finished(t, check.RequestID); task.Status != "dead_letter" || task.ErrorCode != "retry_limit_exhausted" {
			t.Fatalf("task=%+v", task)
		}
		saved, err := proxies.GetHealthCheck(env.Tenant, env.Owner.ID, env.Workspace.ID, check.ID)
		if err != nil || saved.Status != "failed" || saved.ErrorCode != "retry_limit_exhausted" || saved.CompletedAt == nil {
			t.Fatalf("health check of a dead-lettered task=%+v err=%v", saved, err)
		}
	})
}
