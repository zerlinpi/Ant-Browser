package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	analyticsservice "github.com/zerlinpi/Ant-Browser/server/services/analytics-service"
)

var _ analyticsservice.Repository = (*Store)(nil)

// Analytics sidecar keeps this adapter source-compatible with callers that
// still construct memory.Store literals. Production data lives in PostgreSQL.
type analyticsState struct {
	mu     sync.RWMutex
	events map[string]analyticsservice.Event
	audit  map[string]analyticsservice.AuditEvent
	risks  map[string]analyticsservice.RiskEvent
}

var analyticsStates sync.Map // map[*Store]*analyticsState

func (s *Store) analytics() *analyticsState {
	if value, ok := analyticsStates.Load(s); ok {
		return value.(*analyticsState)
	}
	created := &analyticsState{events: map[string]analyticsservice.Event{}, audit: map[string]analyticsservice.AuditEvent{}, risks: map[string]analyticsservice.RiskEvent{}}
	actual, _ := analyticsStates.LoadOrStore(s, created)
	return actual.(*analyticsState)
}

func (s *Store) RecordAnalyticsEvent(_ context.Context, event analyticsservice.Event) error {
	state := s.analytics()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.events[event.ID] = cloneAnalyticsEvent(event)
	return nil
}

func (s *Store) CreateAnalyticsEvent(ctx context.Context, event analyticsservice.Event) error {
	return s.RecordAnalyticsEvent(ctx, event)
}

func (s *Store) AddAuditEvent(_ context.Context, event analyticsservice.AuditEvent) error {
	state := s.analytics()
	state.mu.Lock()
	defer state.mu.Unlock()
	if event.ID == "" {
		event.ID = event.CreatedAt.UTC().Format(time.RFC3339Nano)
	}
	state.audit[event.ID] = cloneAuditEvent(event)
	return nil
}

func (s *Store) AddAnalyticsRiskEvent(_ context.Context, event analyticsservice.RiskEvent) error {
	state := s.analytics()
	state.mu.Lock()
	defer state.mu.Unlock()
	state.risks[event.ID] = cloneRiskEvent(event)
	return nil
}

func (s *Store) ListAnalyticsEvents(_ context.Context, workspaceID string, query analyticsservice.EventQuery) ([]analyticsservice.Event, error) {
	state := s.analytics()
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]analyticsservice.Event, 0)
	for _, event := range state.events {
		if event.WorkspaceID == workspaceID && !event.OccurredAt.Before(query.From) && event.OccurredAt.Before(query.To) && (query.EventType == "" || event.EventType == query.EventType) && (query.ActorUserID == "" || event.ActorUserID == query.ActorUserID) {
			items = append(items, cloneAnalyticsEvent(event))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
	return pageEvents(items, query.Offset, query.Limit), nil
}

func (s *Store) ListAuditEvents(_ context.Context, workspaceID string, query analyticsservice.AuditQuery) ([]analyticsservice.AuditEvent, error) {
	state := s.analytics()
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]analyticsservice.AuditEvent, 0)
	for _, event := range state.audit {
		if event.WorkspaceID == workspaceID && !event.CreatedAt.Before(query.From) && event.CreatedAt.Before(query.To) && (query.ActorUserID == "" || event.ActorUserID == query.ActorUserID) && (query.Action == "" || event.Action == query.Action) && (query.Outcome == "" || event.Outcome == query.Outcome) {
			items = append(items, cloneAuditEvent(event))
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if query.Offset >= len(items) {
		return []analyticsservice.AuditEvent{}, nil
	}
	end := query.Offset + query.Limit
	if end > len(items) {
		end = len(items)
	}
	return items[query.Offset:end], nil
}

func (s *Store) ListRiskEventsForAnalytics(_ context.Context, workspaceID string, query analyticsservice.RiskQuery) ([]analyticsservice.RiskEvent, error) {
	state := s.analytics()
	state.mu.RLock()
	defer state.mu.RUnlock()
	items := make([]analyticsservice.RiskEvent, 0)
	for _, event := range state.risks {
		if event.WorkspaceID == workspaceID && !event.CreatedAt.Before(query.From) && event.CreatedAt.Before(query.To) && (!query.OpenOnly || event.ResolvedAt == nil) && (query.Severity == "" || event.Severity == query.Severity) {
			items = append(items, cloneRiskEvent(event))
		}
	}
	// Account-service risk events are persisted in its existing sidecar. They
	// are open by definition because that legacy table has no resolved_at.
	accountState := s.accountCenter()
	accountState.mu.RLock()
	for _, event := range accountState.events {
		if event.WorkspaceID == workspaceID && !event.CreatedAt.Before(query.From) && event.CreatedAt.Before(query.To) && (query.Severity == "" || event.Level == query.Severity) {
			items = append(items, analyticsservice.RiskEvent{ID: event.ID, WorkspaceID: event.WorkspaceID, AccountID: event.AccountID, Severity: event.Level, EventType: event.Code, Details: map[string]interface{}{"description": event.Description, "createdBy": event.CreatedBy}, CreatedAt: event.CreatedAt})
		}
	}
	accountState.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	if query.Offset >= len(items) {
		return []analyticsservice.RiskEvent{}, nil
	}
	end := query.Offset + query.Limit
	if end > len(items) {
		end = len(items)
	}
	return items[query.Offset:end], nil
}

func (s *Store) AggregateDashboard(ctx context.Context, workspaceID string, window analyticsservice.Window) (analyticsservice.Dashboard, error) {
	accounts, err := s.ListAccounts(ctx, workspaceID)
	if err != nil {
		return analyticsservice.Dashboard{}, err
	}
	instances, err := s.ListInstances(ctx, workspaceID)
	if err != nil {
		return analyticsservice.Dashboard{}, err
	}
	proxies, err := s.ListProxies(ctx, workspaceID)
	if err != nil {
		return analyticsservice.Dashboard{}, err
	}
	tasks, err := s.ListTasks(ctx, workspaceID, 10000)
	if err != nil {
		return analyticsservice.Dashboard{}, err
	}
	d := analyticsservice.Dashboard{Window: window, Accounts: analyticsservice.AccountMetrics{ByStatus: map[string]int{}}, Risks: analyticsservice.RiskMetrics{BySeverity: map[string]int{}}, Team: analyticsservice.TeamMetrics{EventsByActor: map[string]int{}}}
	for _, a := range accounts {
		d.Accounts.Total++
		d.Accounts.ByStatus[a.Status]++
		if a.Status == "active" {
			d.Accounts.Active++
		}
		if a.Status == "risk" || a.Status == "locked" || a.RiskLevel == "high" || a.RiskLevel == "critical" {
			d.Accounts.AtRisk++
		}
	}
	now := window.To
	for _, i := range instances {
		d.Instances.Total++
		switch i.ObservedState {
		case "running":
			if i.LastSeenAt != nil && !i.LastSeenAt.Before(now.Add(-5*time.Minute)) {
				d.Instances.Online++
			} else {
				d.Instances.Offline++
			}
		case "offline":
			d.Instances.Offline++
		case "starting", "stopping":
			d.Instances.Starting++
		case "failed":
			d.Instances.Failed++
		}
	}
	d.Proxies.Total = len(proxies)
	for _, p := range proxies {
		if p.Status == "active" {
			d.Proxies.Healthy++
		}
		if p.Status == "unhealthy" {
			d.Proxies.Unhealthy++
		}
		checks, checkErr := s.ListProxyHealthChecks(ctx, workspaceID, p.ID, 10000)
		if checkErr != nil {
			return analyticsservice.Dashboard{}, checkErr
		}
		for _, c := range checks {
			if c.CompletedAt == nil || c.CompletedAt.Before(window.From) || !c.CompletedAt.Before(window.To) {
				continue
			}
			d.Proxies.Samples++
			if c.Status == "succeeded" {
				d.Proxies.SuccessRate++
				d.Proxies.AverageLatencyMS += float64(c.LatencyMS)
			}
		}
	}
	if d.Proxies.Samples > 0 {
		successes := d.Proxies.SuccessRate
		if successes > 0 {
			d.Proxies.AverageLatencyMS /= successes
		}
		d.Proxies.SuccessRate = d.Proxies.SuccessRate / float64(d.Proxies.Samples) * 100
	}
	for _, t := range tasks {
		if t.CreatedAt.Before(window.From) || !t.CreatedAt.Before(window.To) {
			continue
		}
		d.Tasks.Total++
		if t.Status == "succeeded" || t.Status == "failed" || t.Status == "cancelled" || t.Status == "dead_letter" {
			d.Tasks.Completed++
		}
		if t.Status == "succeeded" {
			d.Tasks.Succeeded++
		}
		if t.Status == "failed" || t.Status == "dead_letter" {
			d.Tasks.Failed++
		}
	}
	if d.Tasks.Completed > 0 {
		d.Tasks.SuccessRate = float64(d.Tasks.Succeeded) / float64(d.Tasks.Completed) * 100
	}
	// Open alerts are current state, so they are not limited to the selected
	// event window (the PostgreSQL aggregate follows the same rule).
	risks, err := s.ListRiskEventsForAnalytics(ctx, workspaceID, analyticsservice.RiskQuery{Window: analyticsservice.Window{To: window.To}, OpenOnly: true, Limit: 100000})
	if err != nil {
		return analyticsservice.Dashboard{}, err
	}
	for _, r := range risks {
		d.Risks.Open++
		d.Risks.BySeverity[r.Severity]++
	}
	members, err := s.ListMembers(ctx, workspaceID)
	if err == nil {
		d.Team.ActiveMembers = len(members)
	}
	state := s.analytics()
	state.mu.RLock()
	for _, e := range state.events {
		if e.WorkspaceID == workspaceID && !e.OccurredAt.Before(window.From) && e.OccurredAt.Before(window.To) && e.ActorUserID != "" {
			d.Team.EventsByActor[e.ActorUserID]++
		}
	}
	state.mu.RUnlock()
	for range d.Team.EventsByActor {
		d.Team.ActiveActors++
	}
	return d, nil
}

func pageEvents(items []analyticsservice.Event, offset, limit int) []analyticsservice.Event {
	if offset >= len(items) {
		return []analyticsservice.Event{}
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}
func cloneAnalyticsEvent(v analyticsservice.Event) analyticsservice.Event {
	v.Dimensions = cloneAny(v.Dimensions)
	v.Measurements = cloneAny(v.Measurements)
	return v
}
func cloneAuditEvent(v analyticsservice.AuditEvent) analyticsservice.AuditEvent {
	v.Metadata = cloneAny(v.Metadata)
	return v
}
func cloneRiskEvent(v analyticsservice.RiskEvent) analyticsservice.RiskEvent {
	v.Details = cloneAny(v.Details)
	return v
}
func cloneAny(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
