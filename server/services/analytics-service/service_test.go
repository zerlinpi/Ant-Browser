package analyticsservice

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	memberservice "github.com/zerlinpi/Ant-Browser/server/services/member-service"
)

type analyticsAuthorizer struct {
	calls      int
	err        error
	permission memberservice.Permission
}

func (a *analyticsAuthorizer) Require(_ context.Context, _, _ string, p memberservice.Permission) error {
	a.calls++
	a.permission = p
	return a.err
}

type analyticsRepository struct {
	dashboard Dashboard
	events    []Event
	audits    []AuditEvent
	risks     []RiskEvent
	recorded  Event
	query     EventQuery
}

func (r *analyticsRepository) AggregateDashboard(_ context.Context, _ string, w Window) (Dashboard, error) {
	r.dashboard.Window = w
	return r.dashboard, nil
}
func (r *analyticsRepository) ListAnalyticsEvents(_ context.Context, _ string, q EventQuery) ([]Event, error) {
	r.query = q
	return r.events, nil
}
func (r *analyticsRepository) RecordAnalyticsEvent(_ context.Context, e Event) error {
	r.recorded = e
	return nil
}
func (r *analyticsRepository) ListAuditEvents(context.Context, string, AuditQuery) ([]AuditEvent, error) {
	return r.audits, nil
}
func (r *analyticsRepository) ListRiskEventsForAnalytics(context.Context, string, RiskQuery) ([]RiskEvent, error) {
	return r.risks, nil
}

func testService(r Repository, a Authorizer) *Service {
	s := New(r, a)
	s.now = func() time.Time { return time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC) }
	return s
}

func TestDashboardRequiresAnalyticsPermissionAndNormalizesWindow(t *testing.T) {
	r := &analyticsRepository{}
	a := &analyticsAuthorizer{}
	s := testService(r, a)
	d, err := s.Dashboard(context.Background(), "actor", "workspace", WindowInput{})
	if err != nil {
		t.Fatal(err)
	}
	if a.permission != memberservice.PermissionAnalyticsRead {
		t.Fatalf("permission=%q", a.permission)
	}
	if !d.Window.To.Equal(time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)) || d.Window.To.Sub(d.Window.From) != DefaultWindow {
		t.Fatalf("window=%+v", d.Window)
	}
}

func TestEventsValidatesUUIDFilterAndPaginates(t *testing.T) {
	r := &analyticsRepository{events: []Event{{ID: "1"}, {ID: "2"}}}
	a := &analyticsAuthorizer{}
	s := testService(r, a)
	page, err := s.Events(context.Background(), "actor", "workspace", EventQuery{Limit: 999, Offset: -10})
	if err != nil {
		t.Fatal(err)
	}
	if r.query.Limit != MaxLimit || r.query.Offset != 0 {
		t.Fatalf("query=%+v", r.query)
	}
	if len(page.Items) != 2 || page.NextOffset != 0 {
		t.Fatalf("page=%+v", page)
	}
	if _, err := s.Events(context.Background(), "actor", "workspace", EventQuery{ActorUserID: strings.Repeat("x", 101)}); err == nil {
		t.Fatal("expected long actor filter validation")
	}
}

func TestDashboardRejectsInvalidWindowBeforeRepository(t *testing.T) {
	r := &analyticsRepository{}
	s := testService(r, &analyticsAuthorizer{})
	from := time.Date(2026, 1, 2, 10, 0, 0, 0, time.UTC)
	to := from.Add(-time.Minute)
	if _, err := s.Dashboard(context.Background(), "actor", "workspace", WindowInput{From: from, To: to}); !errors.Is(err, ErrInvalidWindow) {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordDefaultsTimeAndClonesPayload(t *testing.T) {
	r := &analyticsRepository{}
	s := testService(r, &analyticsAuthorizer{})
	dims := map[string]interface{}{"kind": "login"}
	e, err := s.Record(context.Background(), EventInput{WorkspaceID: "workspace", ActorUserID: "actor", EventType: "account.login", Dimensions: dims})
	if err != nil {
		t.Fatal(err)
	}
	if e.ID == "" || !e.OccurredAt.Equal(s.now()) {
		t.Fatalf("event=%+v", e)
	}
	dims["kind"] = "changed"
	if r.recorded.Dimensions["kind"] != "login" {
		t.Fatal("record mutated caller payload")
	}
}

func TestAuditUsesDedicatedAuditPermission(t *testing.T) {
	a := &analyticsAuthorizer{}
	s := testService(&analyticsRepository{}, a)
	if _, err := s.Audit(context.Background(), "actor", "workspace", AuditQuery{}); err != nil {
		t.Fatal(err)
	}
	if a.permission != memberservice.PermissionAuditRead {
		t.Fatalf("permission=%q", a.permission)
	}
}

func TestAuditRejectsMalformedActorFilter(t *testing.T) {
	s := testService(&analyticsRepository{}, &analyticsAuthorizer{})
	if _, err := s.Audit(context.Background(), "actor", "workspace", AuditQuery{ActorUserID: "not-a-uuid"}); !errors.Is(err, ErrInvalidQuery) {
		t.Fatalf("err=%v, want ErrInvalidQuery", err)
	}
}
