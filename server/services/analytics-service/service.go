// Package analyticsservice provides tenant-scoped operational analytics.
//
// Analytics is intentionally read-only for normal callers. Event recording is
// a narrow producer API and is expected to be called by trusted services.
package analyticsservice

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

var (
	ErrInvalidWindow = errors.New("analytics time window is invalid")
	ErrInvalidQuery  = errors.New("analytics query is invalid")
)

const (
	DefaultWindow = 24 * time.Hour
	MaxWindow     = 366 * 24 * time.Hour
	DefaultLimit  = 50
	MaxLimit      = 200
)

type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// WindowInput uses UTC instants. A zero bound is filled from the other bound,
// or defaults to the previous 24 hours when both bounds are omitted.
type WindowInput struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// DashboardInput is retained as the explicit name used by HTTP adapters.
type DashboardInput = WindowInput

func NormalizeWindow(input WindowInput, now time.Time) (Window, error) {
	now = now.UTC()
	from, to := input.From, input.To
	if from.IsZero() && to.IsZero() {
		to, from = now, now.Add(-DefaultWindow)
	} else if from.IsZero() {
		to = to.UTC()
		from = to.Add(-DefaultWindow)
	} else if to.IsZero() {
		from = from.UTC()
		to = now
	} else {
		from, to = from.UTC(), to.UTC()
	}
	if from.IsZero() || to.IsZero() || !from.Before(to) || to.After(now.Add(5*time.Minute)) || to.Sub(from) > MaxWindow {
		return Window{}, ErrInvalidWindow
	}
	return Window{From: from, To: to}, nil
}

type Event struct {
	ID           string                 `json:"id"`
	WorkspaceID  string                 `json:"workspaceId"`
	ActorUserID  string                 `json:"actorUserId,omitempty"`
	EventType    string                 `json:"eventType"`
	OccurredAt   time.Time              `json:"occurredAt"`
	Dimensions   map[string]interface{} `json:"dimensions,omitempty"`
	Measurements map[string]interface{} `json:"measurements,omitempty"`
	RequestID    string                 `json:"requestId,omitempty"`
}

type EventInput struct {
	WorkspaceID  string                 `json:"workspaceId"`
	ActorUserID  string                 `json:"actorUserId,omitempty"`
	EventType    string                 `json:"eventType"`
	OccurredAt   time.Time              `json:"occurredAt,omitempty"`
	Dimensions   map[string]interface{} `json:"dimensions,omitempty"`
	Measurements map[string]interface{} `json:"measurements,omitempty"`
	RequestID    string                 `json:"requestId,omitempty"`
}

type EventQuery struct {
	Window
	EventType   string
	ActorUserID string
	Limit       int
	Offset      int
}

type EventPage struct {
	Items      []Event `json:"items"`
	NextOffset int     `json:"nextOffset,omitempty"`
}

type AuditEvent struct {
	ID             string                 `json:"id"`
	OrganizationID string                 `json:"organizationId,omitempty"`
	WorkspaceID    string                 `json:"workspaceId,omitempty"`
	ActorUserID    string                 `json:"actorUserId,omitempty"`
	ActorDeviceID  string                 `json:"actorDeviceId,omitempty"`
	Action         string                 `json:"action"`
	ResourceType   string                 `json:"resourceType"`
	ResourceID     string                 `json:"resourceId,omitempty"`
	Outcome        string                 `json:"outcome"`
	RequestID      string                 `json:"requestId,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt      time.Time              `json:"createdAt"`
}

type AuditQuery struct {
	Window
	ActorUserID string
	Action      string
	Outcome     string
	Limit       int
	Offset      int
}

type AuditPage struct {
	Items      []AuditEvent `json:"items"`
	NextOffset int          `json:"nextOffset,omitempty"`
}

type RiskEvent struct {
	ID          string                 `json:"id"`
	WorkspaceID string                 `json:"workspaceId"`
	AccountID   string                 `json:"accountId,omitempty"`
	ProxyID     string                 `json:"proxyId,omitempty"`
	Severity    string                 `json:"severity"`
	EventType   string                 `json:"eventType"`
	Details     map[string]interface{} `json:"details,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
	ResolvedAt  *time.Time             `json:"resolvedAt,omitempty"`
}

type RiskQuery struct {
	Window
	OpenOnly bool
	Severity string
	Limit    int
	Offset   int
}

type RiskPage struct {
	Items      []RiskEvent `json:"items"`
	NextOffset int         `json:"nextOffset,omitempty"`
}

type AccountMetrics struct {
	Total    int            `json:"total"`
	Active   int            `json:"active"`
	AtRisk   int            `json:"atRisk"`
	ByStatus map[string]int `json:"byStatus"`
}
type InstanceMetrics struct {
	Total    int `json:"total"`
	Online   int `json:"online"`
	Offline  int `json:"offline"`
	Starting int `json:"starting"`
	Failed   int `json:"failed"`
}
type ProxyMetrics struct {
	Total            int     `json:"total"`
	Healthy          int     `json:"healthy"`
	Unhealthy        int     `json:"unhealthy"`
	Samples          int     `json:"samples"`
	SuccessRate      float64 `json:"successRate"`
	AverageLatencyMS float64 `json:"averageLatencyMs"`
}
type TaskMetrics struct {
	Total       int     `json:"total"`
	Completed   int     `json:"completed"`
	Succeeded   int     `json:"succeeded"`
	Failed      int     `json:"failed"`
	SuccessRate float64 `json:"successRate"`
}
type RiskMetrics struct {
	Open       int            `json:"open"`
	BySeverity map[string]int `json:"bySeverity"`
}
type TeamMetrics struct {
	ActiveMembers int            `json:"activeMembers"`
	ActiveActors  int            `json:"activeActors"`
	EventsByActor map[string]int `json:"eventsByActor"`
}

type Dashboard struct {
	Window    Window          `json:"window"`
	Accounts  AccountMetrics  `json:"accounts"`
	Instances InstanceMetrics `json:"instances"`
	Proxies   ProxyMetrics    `json:"proxies"`
	Tasks     TaskMetrics     `json:"tasks"`
	Risks     RiskMetrics     `json:"risks"`
	Team      TeamMetrics     `json:"team"`
}

type Repository interface {
	AggregateDashboard(context.Context, string, Window) (Dashboard, error)
	ListAnalyticsEvents(context.Context, string, EventQuery) ([]Event, error)
	RecordAnalyticsEvent(context.Context, Event) error
	ListAuditEvents(context.Context, string, AuditQuery) ([]AuditEvent, error)
	ListRiskEventsForAnalytics(context.Context, string, RiskQuery) ([]RiskEvent, error)
}

type Authorizer interface {
	Require(context.Context, string, string, memberservice.Permission) error
}

type Service struct {
	repository Repository
	authorizer Authorizer
	now        func() time.Time
}

func New(repository Repository, authorizer Authorizer) *Service {
	return &Service{repository: repository, authorizer: authorizer, now: time.Now}
}

func NewService(repository Repository, authorizer Authorizer) *Service {
	return New(repository, authorizer)
}

func (s *Service) Dashboard(ctx context.Context, actorID, workspaceID string, input WindowInput) (Dashboard, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return Dashboard{}, err
	}
	w, err := NormalizeWindow(input, s.now())
	if err != nil {
		return Dashboard{}, err
	}
	dashboard, err := s.repository.AggregateDashboard(ctx, strings.TrimSpace(workspaceID), w)
	if err != nil {
		return Dashboard{}, err
	}
	dashboard.Window = w
	return dashboard, nil
}

func (s *Service) GetDashboard(ctx context.Context, actorID, workspaceID string, input WindowInput) (Dashboard, error) {
	return s.Dashboard(ctx, actorID, workspaceID, input)
}

func (s *Service) Events(ctx context.Context, actorID, workspaceID string, input EventQuery) (EventPage, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return EventPage{}, err
	}
	query, err := s.normalizeEventQuery(input)
	if err != nil {
		return EventPage{}, err
	}
	items, err := s.repository.ListAnalyticsEvents(ctx, strings.TrimSpace(workspaceID), query)
	if err != nil {
		return EventPage{}, err
	}
	page := EventPage{Items: items}
	if len(items) == query.Limit {
		page.NextOffset = query.Offset + len(items)
	}
	return page, nil
}

func (s *Service) ListEvents(ctx context.Context, actorID, workspaceID string, input EventQuery) (EventPage, error) {
	return s.Events(ctx, actorID, workspaceID, input)
}

func (s *Service) QueryEvents(ctx context.Context, actorID, workspaceID string, input EventQuery) (EventPage, error) {
	return s.Events(ctx, actorID, workspaceID, input)
}

// Record is used by trusted event producers. It does not grant a caller a
// read permission; callers should keep this method behind their event bus.
func (s *Service) Record(ctx context.Context, input EventInput) (Event, error) {
	workspaceID, eventType := strings.TrimSpace(input.WorkspaceID), strings.TrimSpace(input.EventType)
	if workspaceID == "" || eventType == "" || len(eventType) > 200 {
		return Event{}, ErrInvalidQuery
	}
	// IDs are opaque at the service boundary. PostgreSQL adapters enforce UUID
	// shape when binding to the relational schema, while memory/test adapters
	// intentionally support readable fixture IDs.
	now := s.now().UTC()
	occurred := input.OccurredAt.UTC()
	if input.OccurredAt.IsZero() {
		occurred = now
	}
	if occurred.After(now.Add(5*time.Minute)) || occurred.Before(now.Add(-MaxWindow)) {
		return Event{}, ErrInvalidWindow
	}
	e := Event{ID: uuid.NewString(), WorkspaceID: workspaceID, ActorUserID: strings.TrimSpace(input.ActorUserID), EventType: eventType, OccurredAt: occurred, Dimensions: cloneMap(input.Dimensions), Measurements: cloneMap(input.Measurements), RequestID: strings.TrimSpace(input.RequestID)}
	if err := s.repository.RecordAnalyticsEvent(ctx, e); err != nil {
		return Event{}, err
	}
	return e, nil
}

func (s *Service) RecordEvent(ctx context.Context, input EventInput) (Event, error) {
	return s.Record(ctx, input)
}

func (s *Service) Audit(ctx context.Context, actorID, workspaceID string, input AuditQuery) (AuditPage, error) {
	if err := s.requireAudit(ctx, actorID, workspaceID); err != nil {
		return AuditPage{}, err
	}
	query, err := s.normalizeAuditQuery(input)
	if err != nil {
		return AuditPage{}, err
	}
	items, err := s.repository.ListAuditEvents(ctx, strings.TrimSpace(workspaceID), query)
	if err != nil {
		return AuditPage{}, err
	}
	page := AuditPage{Items: items}
	if len(items) == query.Limit {
		page.NextOffset = query.Offset + len(items)
	}
	return page, nil
}

func (s *Service) ListAuditEvents(ctx context.Context, actorID, workspaceID string, input AuditQuery) (AuditPage, error) {
	return s.Audit(ctx, actorID, workspaceID, input)
}

func (s *Service) Operations(ctx context.Context, actorID, workspaceID string, input AuditQuery) (AuditPage, error) {
	return s.Audit(ctx, actorID, workspaceID, input)
}

func (s *Service) Risks(ctx context.Context, actorID, workspaceID string, input RiskQuery) (RiskPage, error) {
	if err := s.require(ctx, actorID, workspaceID); err != nil {
		return RiskPage{}, err
	}
	query, err := s.normalizeRiskQuery(input)
	if err != nil {
		return RiskPage{}, err
	}
	items, err := s.repository.ListRiskEventsForAnalytics(ctx, strings.TrimSpace(workspaceID), query)
	if err != nil {
		return RiskPage{}, err
	}
	page := RiskPage{Items: items}
	if len(items) == query.Limit {
		page.NextOffset = query.Offset + len(items)
	}
	return page, nil
}

func (s *Service) ListRiskEvents(ctx context.Context, actorID, workspaceID string, input RiskQuery) (RiskPage, error) {
	return s.Risks(ctx, actorID, workspaceID, input)
}

func (s *Service) Alerts(ctx context.Context, actorID, workspaceID string, input RiskQuery) (RiskPage, error) {
	return s.Risks(ctx, actorID, workspaceID, input)
}

func (s *Service) require(ctx context.Context, actorID, workspaceID string) error {
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(workspaceID) == "" {
		return errors.New("workspaceId and actorId are required")
	}
	if s.authorizer == nil {
		return errors.New("authorizer is not configured")
	}
	return s.authorizer.Require(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID), memberservice.PermissionAnalyticsRead)
}
func (s *Service) requireAudit(ctx context.Context, actorID, workspaceID string) error {
	if strings.TrimSpace(actorID) == "" || strings.TrimSpace(workspaceID) == "" {
		return errors.New("workspaceId and actorId are required")
	}
	if s.authorizer == nil {
		return errors.New("authorizer is not configured")
	}
	if err := s.authorizer.Require(ctx, strings.TrimSpace(workspaceID), strings.TrimSpace(actorID), memberservice.PermissionAuditRead); err != nil {
		return err
	}
	return nil
}
func (s *Service) normalizeEventQuery(q EventQuery) (EventQuery, error) {
	w, err := NormalizeWindow(WindowInput(q.Window), s.now())
	if err != nil {
		return q, err
	}
	q.Window = w
	q.Limit, q.Offset = normalizePage(q.Limit, q.Offset)
	q.EventType, q.ActorUserID = strings.TrimSpace(q.EventType), strings.TrimSpace(q.ActorUserID)
	if len(q.EventType) > 200 || len(q.ActorUserID) > 100 {
		return q, ErrInvalidQuery
	}
	if q.ActorUserID != "" {
		if _, err := uuid.Parse(q.ActorUserID); err != nil {
			return q, ErrInvalidQuery
		}
	}
	return q, nil
}
func (s *Service) normalizeAuditQuery(q AuditQuery) (AuditQuery, error) {
	w, err := NormalizeWindow(WindowInput(q.Window), s.now())
	if err != nil {
		return q, err
	}
	q.Window = w
	q.Limit, q.Offset = normalizePage(q.Limit, q.Offset)
	q.ActorUserID, q.Action, q.Outcome = strings.TrimSpace(q.ActorUserID), strings.TrimSpace(q.Action), strings.TrimSpace(q.Outcome)
	if len(q.Action) > 200 || len(q.Outcome) > 50 || q.Offset < 0 {
		return q, ErrInvalidQuery
	}
	if q.ActorUserID != "" {
		if _, err := uuid.Parse(q.ActorUserID); err != nil {
			return q, ErrInvalidQuery
		}
	}
	return q, nil
}
func (s *Service) normalizeRiskQuery(q RiskQuery) (RiskQuery, error) {
	w, err := NormalizeWindow(WindowInput(q.Window), s.now())
	if err != nil {
		return q, err
	}
	q.Window = w
	q.Limit, q.Offset = normalizePage(q.Limit, q.Offset)
	q.Severity = strings.ToLower(strings.TrimSpace(q.Severity))
	if q.Severity != "" && !validSeverity(q.Severity) {
		return q, ErrInvalidQuery
	}
	return q, nil
}
func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
func validSeverity(s string) bool {
	switch s {
	case "info", "low", "medium", "high", "critical":
		return true
	}
	return false
}
func cloneMap(input map[string]interface{}) map[string]interface{} {
	if input == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(input))
	for k, v := range input {
		out[k] = v
	}
	return out
}

// Keep deterministic output for adapters that build actor summaries.
func SortEvents(items []Event) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].OccurredAt.Equal(items[j].OccurredAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].OccurredAt.After(items[j].OccurredAt)
	})
}
