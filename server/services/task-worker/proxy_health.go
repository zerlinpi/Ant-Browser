package taskworker

import (
	"context"
	"errors"
	"net/netip"
	"time"

	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
	proxyservice "github.com/zerlinpi/Ant-Browser/server/services/proxy-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

type ProxyHealthRepository interface {
	FindProxy(context.Context, string, string) (proxyservice.Proxy, error)
	FindProxyHealthCheck(context.Context, string, string) (proxyservice.HealthCheck, error)
	CompleteProxyHealthCheck(context.Context, string, string, proxyservice.HealthResult, time.Time) (proxyservice.HealthCheck, error)
}

// ProxyProbe must execute through the provided connector/kernel. Implementors
// resolve credentials internally and must never silently switch stacks.
type ProxyProbe interface {
	Probe(context.Context, proxyservice.Proxy) (proxyservice.HealthResult, error)
}

func NewProxyHealthHandler(repository ProxyHealthRepository, probe ProxyProbe, publishers ...NotificationPublisher) (Handler, error) {
	if repository == nil || probe == nil {
		return nil, errors.New("proxy health repository and probe are required")
	}
	var notifications NotificationPublisher
	if len(publishers) > 0 {
		notifications = publishers[0]
	}
	return func(ctx context.Context, task taskservice.Task) (map[string]interface{}, error) {
		checkID, _ := task.Payload["checkId"].(string)
		if task.TaskType != "proxy.health_check" || checkID == "" {
			return nil, Failure{Code: "invalid_health_task", Message: "Health task identity is invalid"}
		}
		check, err := repository.FindProxyHealthCheck(ctx, task.WorkspaceID, checkID)
		if err != nil {
			return nil, err
		}
		if check.WorkspaceID != task.WorkspaceID || check.RequestID != task.ID || check.ProxyID != task.Payload["proxyId"] || string(check.ConnectorType) != task.Payload["connectorType"] || string(check.Kernel) != task.Payload["kernel"] {
			return nil, Failure{Code: "health_scope_mismatch", Message: "Health task does not match its persisted request"}
		}
		response := func(value proxyservice.HealthCheck) map[string]interface{} {
			return map[string]interface{}{"checkId": value.ID, "status": value.Status, "ip": value.IP, "latencyMs": value.LatencyMS, "errorCode": value.ErrorCode}
		}
		notifyFailure := func(value proxyservice.HealthCheck) error {
			if notifications == nil || value.CreatedBy == "" {
				return nil
			}
			_, err := notifications.Publish(ctx, notificationservice.CreateInput{
				WorkspaceID: value.WorkspaceID, RecipientUserID: value.CreatedBy,
				EventType: "proxy.health_failed", Title: "Proxy health check failed",
				Body: "The configured proxy could not provide a healthy exit. Review the proxy center before launching accounts.",
				Payload: map[string]interface{}{
					"proxyId": value.ProxyID, "checkId": value.ID,
					"errorCode": value.ErrorCode, "connectorType": value.ConnectorType,
					"kernel": value.Kernel,
				},
				IdempotencyKey: "proxy-health-failure:" + value.ID,
			})
			return err
		}
		if check.CompletedAt != nil {
			if check.Status == "failed" && check.ErrorCode != "proxy_unreachable" {
				return nil, Failure{Code: check.ErrorCode, Message: "Proxy health request could not execute"}
			}
			if check.Status == "failed" {
				if err := notifyFailure(check); err != nil {
					return nil, Failure{Code: "notification_publish_failed", Message: "Proxy failure notification could not be published", Retryable: true, RetryDelay: 5 * time.Second}
				}
			}
			return response(check), nil
		}
		terminate := func(code, message string) (map[string]interface{}, error) {
			_, err := repository.CompleteProxyHealthCheck(ctx, task.WorkspaceID, check.ID, proxyservice.HealthResult{Status: "failed", ErrorCode: code}, time.Now().UTC())
			if err != nil {
				return nil, err
			}
			return nil, Failure{Code: code, Message: message}
		}
		proxy, err := repository.FindProxy(ctx, task.WorkspaceID, check.ProxyID)
		if errors.Is(err, proxyservice.ErrNotFound) {
			return terminate("proxy_deleted", "Proxy no longer exists")
		}
		if err != nil {
			return nil, err
		}
		kernel, err := proxyservice.ResolveProxyKernelForConnector(proxy.ConnectorType, proxy.Protocol, proxy.HasCredentials || proxy.Username != "")
		if err != nil {
			return terminate("unsupported_proxy_route", "Proxy route is unsupported")
		}
		if proxy.WorkspaceID != task.WorkspaceID || proxy.ID != check.ProxyID || proxy.ConnectorType != check.ConnectorType || kernel != check.Kernel || proxy.Kernel != kernel {
			return terminate("proxy_route_changed", "Proxy route changed after the health request was queued")
		}
		if proxy.Status != "active" && proxy.Status != "unhealthy" {
			return terminate("proxy_disabled", "Proxy is not enabled for health checks")
		}
		result, err := probe.Probe(ctx, proxy)
		if err != nil {
			// Infrastructure failures are retried, but may contain credentials or
			// connector configuration. Never persist their raw error strings.
			return nil, Failure{Code: "proxy_probe_unavailable", Message: "Proxy probe could not complete", Retryable: true, RetryDelay: 5 * time.Second}
		}
		if result.Status != "succeeded" && result.Status != "failed" {
			return nil, Failure{Code: "invalid_probe_result", Message: "Probe returned an invalid status"}
		}
		if result.LatencyMS < 0 || result.LatencyMS > 2147483647 {
			return nil, Failure{Code: "invalid_probe_result", Message: "Probe returned an invalid latency"}
		}
		if result.IP != "" {
			ip, err := netip.ParseAddr(result.IP)
			if err != nil || ip.Zone() != "" {
				return nil, Failure{Code: "invalid_probe_result", Message: "Probe returned an invalid IP"}
			}
			result.IP = ip.String()
		}
		if result.Status == "succeeded" && result.IP == "" {
			return nil, Failure{Code: "invalid_probe_result", Message: "Successful probe must include an exit IP"}
		}
		// Only stable server-owned diagnostics are exposed in persisted results.
		result.ErrorMessage = ""
		result.ErrorCode = ""
		if result.Status == "failed" {
			result.ErrorCode = "proxy_unreachable"
		}
		saved, err := repository.CompleteProxyHealthCheck(ctx, task.WorkspaceID, check.ID, result, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		if saved.Status == "failed" {
			if err := notifyFailure(saved); err != nil {
				return nil, Failure{Code: "notification_publish_failed", Message: "Proxy failure notification could not be published", Retryable: true, RetryDelay: 5 * time.Second}
			}
		}
		return response(saved), nil
	}, nil
}
