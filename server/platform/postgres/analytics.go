package postgres

import (
	"context"
	"encoding/json"
	"errors"

	analyticsservice "github.com/zerlinpi/Ant-Browser/server/services/analytics-service"
)

var _ analyticsservice.Repository = (*Store)(nil)

// AggregateDashboard intentionally uses the source-of-truth operational
// tables. Rollups can be added later for large installations without changing
// the service contract.
func (s *Store) AggregateDashboard(ctx context.Context, workspaceID string, window analyticsservice.Window) (analyticsservice.Dashboard, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	var d analyticsservice.Dashboard
	var accountStatus, riskCounts, actorCounts []byte
	err := s.pool.QueryRow(ctx, `
SELECT
 (SELECT count(*) FROM accounts WHERE workspace_id=$1::uuid AND deleted_at IS NULL),
 (SELECT count(*) FROM accounts WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND status='active'),
 (SELECT count(*) FROM accounts WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND (status IN ('risk','locked') OR risk_level IN ('high','critical'))),
 COALESCE((SELECT jsonb_object_agg(status,n) FROM (SELECT status,count(*) n FROM accounts WHERE workspace_id=$1::uuid AND deleted_at IS NULL GROUP BY status) x),'{}'::jsonb),
 (SELECT count(*) FROM browser_instances WHERE workspace_id=$1::uuid AND deleted_at IS NULL),
 (SELECT count(*) FROM browser_instances WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND observed_state='running' AND last_seen_at IS NOT NULL AND last_seen_at >= $2 - interval '5 minutes'),
 (SELECT count(*) FROM browser_instances WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND observed_state='offline'),
 (SELECT count(*) FROM browser_instances WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND observed_state IN ('starting','stopping')),
 (SELECT count(*) FROM browser_instances WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND observed_state='failed'),
 (SELECT count(*) FROM proxies WHERE workspace_id=$1::uuid AND deleted_at IS NULL),
 (SELECT count(*) FROM proxies WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND status='active'),
 (SELECT count(*) FROM proxies WHERE workspace_id=$1::uuid AND deleted_at IS NULL AND status='unhealthy'),
 (SELECT count(*) FROM proxy_health_samples WHERE workspace_id=$1::uuid AND checked_at >= $3 AND checked_at < $4),
 COALESCE((SELECT avg(latency_ms)::float8 FROM proxy_health_samples WHERE workspace_id=$1::uuid AND success AND checked_at >= $3 AND checked_at < $4),0),
 COALESCE((SELECT avg(success::int)::float8 FROM proxy_health_samples WHERE workspace_id=$1::uuid AND checked_at >= $3 AND checked_at < $4),0),
 (SELECT count(*) FROM tasks WHERE workspace_id=$1::uuid AND created_at >= $3 AND created_at < $4),
 (SELECT count(*) FROM tasks WHERE workspace_id=$1::uuid AND status IN ('succeeded','failed','cancelled','dead_letter') AND COALESCE(completed_at,updated_at) >= $3 AND COALESCE(completed_at,updated_at) < $4),
 (SELECT count(*) FROM tasks WHERE workspace_id=$1::uuid AND status='succeeded' AND COALESCE(completed_at,updated_at) >= $3 AND COALESCE(completed_at,updated_at) < $4),
 (SELECT count(*) FROM tasks WHERE workspace_id=$1::uuid AND status IN ('failed','dead_letter') AND COALESCE(completed_at,updated_at) >= $3 AND COALESCE(completed_at,updated_at) < $4),
 (SELECT count(*) FROM risk_events WHERE workspace_id=$1::uuid AND resolved_at IS NULL),
 COALESCE((SELECT jsonb_object_agg(severity,n) FROM (SELECT severity,count(*) n FROM risk_events WHERE workspace_id=$1::uuid AND resolved_at IS NULL GROUP BY severity) x),'{}'::jsonb),
 (SELECT count(*) FROM workspace_members WHERE workspace_id=$1::uuid AND status='active'),
 (SELECT count(DISTINCT actor_user_id) FROM analytics_events WHERE workspace_id=$1::uuid AND actor_user_id IS NOT NULL AND occurred_at >= $3 AND occurred_at < $4),
 COALESCE((SELECT jsonb_object_agg(actor_user_id::text,n) FROM (SELECT actor_user_id,count(*) n FROM analytics_events WHERE workspace_id=$1::uuid AND actor_user_id IS NOT NULL AND occurred_at >= $3 AND occurred_at < $4 GROUP BY actor_user_id) x),'{}'::jsonb)
`, workspaceID, window.To, window.From, window.To).Scan(
		&d.Accounts.Total, &d.Accounts.Active, &d.Accounts.AtRisk, &accountStatus,
		&d.Instances.Total, &d.Instances.Online, &d.Instances.Offline, &d.Instances.Starting, &d.Instances.Failed,
		&d.Proxies.Total, &d.Proxies.Healthy, &d.Proxies.Unhealthy, &d.Proxies.Samples, &d.Proxies.AverageLatencyMS, &d.Proxies.SuccessRate,
		&d.Tasks.Total, &d.Tasks.Completed, &d.Tasks.Succeeded, &d.Tasks.Failed,
		&d.Risks.Open, &riskCounts, &d.Team.ActiveMembers, &d.Team.ActiveActors, &actorCounts)
	if err != nil {
		return analyticsservice.Dashboard{}, err
	}
	if err := json.Unmarshal(accountStatus, &d.Accounts.ByStatus); err != nil {
		return analyticsservice.Dashboard{}, err
	}
	if err := json.Unmarshal(riskCounts, &d.Risks.BySeverity); err != nil {
		return analyticsservice.Dashboard{}, err
	}
	if err := json.Unmarshal(actorCounts, &d.Team.EventsByActor); err != nil {
		return analyticsservice.Dashboard{}, err
	}
	if d.Proxies.Samples > 0 {
		d.Proxies.SuccessRate = d.Proxies.SuccessRate * 100
	}
	if d.Tasks.Completed > 0 {
		d.Tasks.SuccessRate = float64(d.Tasks.Succeeded) / float64(d.Tasks.Completed) * 100
	}
	return d, nil
}

func (s *Store) RecordAnalyticsEvent(ctx context.Context, event analyticsservice.Event) error {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: event.WorkspaceID})
	dimensions, err := json.Marshal(event.Dimensions)
	if err != nil {
		return err
	}
	measurements, err := json.Marshal(event.Measurements)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO analytics_events (id,workspace_id,actor_user_id,event_type,occurred_at,dimensions,measurements,request_id) VALUES ($1::uuid,$2::uuid,NULLIF($3,'')::uuid,$4,$5,$6::jsonb,$7::jsonb,$8)`, event.ID, event.WorkspaceID, event.ActorUserID, event.EventType, event.OccurredAt, dimensions, measurements, event.RequestID)
	if isForeignKeyViolation(err) {
		return errors.New("analytics event references an unknown tenant member")
	}
	return err
}

func (s *Store) ListAnalyticsEvents(ctx context.Context, workspaceID string, query analyticsservice.EventQuery) ([]analyticsservice.Event, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	rows, err := s.pool.Query(ctx, `SELECT id::text,workspace_id::text,COALESCE(actor_user_id::text,''),event_type,occurred_at,dimensions,measurements,request_id FROM analytics_events WHERE workspace_id=$1::uuid AND occurred_at >= $2 AND occurred_at < $3 AND ($4='' OR event_type=$4) AND ($5='' OR actor_user_id=$5::uuid) ORDER BY occurred_at DESC,id DESC LIMIT $6 OFFSET $7`, workspaceID, query.From, query.To, query.EventType, query.ActorUserID, query.Limit, query.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]analyticsservice.Event, 0)
	for rows.Next() {
		var item analyticsservice.Event
		var dimensions, measurements []byte
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.ActorUserID, &item.EventType, &item.OccurredAt, &dimensions, &measurements, &item.RequestID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(dimensions, &item.Dimensions); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(measurements, &item.Measurements); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListAuditEvents(ctx context.Context, workspaceID string, query analyticsservice.AuditQuery) ([]analyticsservice.AuditEvent, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	rows, err := s.pool.Query(ctx, `SELECT id::text,COALESCE(organization_id::text,''),COALESCE(workspace_id::text,''),COALESCE(actor_user_id::text,''),COALESCE(actor_device_id::text,''),action,resource_type,COALESCE(resource_id::text,''),outcome,request_id,metadata,created_at FROM audit_events WHERE workspace_id=$1::uuid AND created_at >= $2 AND created_at < $3 AND ($4='' OR actor_user_id=$4::uuid) AND ($5='' OR action=$5) AND ($6='' OR outcome=$6) ORDER BY created_at DESC,id DESC LIMIT $7 OFFSET $8`, workspaceID, query.From, query.To, query.ActorUserID, query.Action, query.Outcome, query.Limit, query.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]analyticsservice.AuditEvent, 0)
	for rows.Next() {
		var item analyticsservice.AuditEvent
		var metadata []byte
		if err := rows.Scan(&item.ID, &item.OrganizationID, &item.WorkspaceID, &item.ActorUserID, &item.ActorDeviceID, &item.Action, &item.ResourceType, &item.ResourceID, &item.Outcome, &item.RequestID, &metadata, &item.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(metadata, &item.Metadata); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) ListRiskEventsForAnalytics(ctx context.Context, workspaceID string, query analyticsservice.RiskQuery) ([]analyticsservice.RiskEvent, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	rows, err := s.pool.Query(ctx, `SELECT id::text,workspace_id::text,COALESCE(account_id::text,''),COALESCE(proxy_id::text,''),severity,event_type,details,created_at,resolved_at FROM risk_events WHERE workspace_id=$1::uuid AND created_at >= $2 AND created_at < $3 AND ($4=false OR resolved_at IS NULL) AND ($5='' OR severity=$5) ORDER BY created_at DESC,id DESC LIMIT $6 OFFSET $7`, workspaceID, query.From, query.To, query.OpenOnly, query.Severity, query.Limit, query.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]analyticsservice.RiskEvent, 0)
	for rows.Next() {
		var item analyticsservice.RiskEvent
		var details []byte
		if err := rows.Scan(&item.ID, &item.WorkspaceID, &item.AccountID, &item.ProxyID, &item.Severity, &item.EventType, &details, &item.CreatedAt, &item.ResolvedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(details, &item.Details); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) CreateAnalyticsEvent(ctx context.Context, event analyticsservice.Event) error {
	return s.RecordAnalyticsEvent(ctx, event)
}
