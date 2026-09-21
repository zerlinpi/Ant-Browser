package scheduleservice

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	automationservice "github.com/zerlinpi/Ant-Browser/server/services/automation-service"
	browserinstanceservice "github.com/zerlinpi/Ant-Browser/server/services/browser-instance-service"
)

type scheduleRepo struct{ item Schedule }

func (r *scheduleRepo) CreateSchedule(_ context.Context, item Schedule) (Schedule, error) {
	r.item = item
	return item, nil
}
func (r *scheduleRepo) FindSchedule(context.Context, string, string) (Schedule, error) {
	return r.item, nil
}
func (r *scheduleRepo) ListSchedules(context.Context, string) ([]Schedule, error) {
	return []Schedule{r.item}, nil
}
func (r *scheduleRepo) UpdateSchedule(_ context.Context, item Schedule, _ int64, _ time.Time) (Schedule, error) {
	r.item = item
	return item, nil
}
func (r *scheduleRepo) DeleteSchedule(context.Context, string, string) error { return nil }
func (r *scheduleRepo) SetScheduleEnabled(_ context.Context, _ string, _ string, enabled bool, _ time.Time) (Schedule, error) {
	r.item.Enabled = enabled
	return r.item, nil
}

type workflowLookup struct {
	workflow automationservice.Workflow
	version  automationservice.WorkflowVersion
}

func (r workflowLookup) FindWorkflow(context.Context, string, string) (automationservice.Workflow, error) {
	return r.workflow, nil
}
func (r workflowLookup) FindWorkflowVersionByID(context.Context, string, string, string) (automationservice.WorkflowVersion, error) {
	return r.version, nil
}

type instanceLookup struct{}

func (instanceLookup) FindInstance(context.Context, string, string) (browserinstanceservice.BrowserInstance, error) {
	return browserinstanceservice.BrowserInstance{}, nil
}

func TestCreateRequiresPublishedPinnedVersion(t *testing.T) {
	wid, vid, iid := uuid.NewString(), uuid.NewString(), uuid.NewString()
	repo := &scheduleRepo{}
	workflow := workflowLookup{workflow: automationservice.Workflow{ID: wid, Status: "draft"}, version: automationservice.WorkflowVersion{ID: vid}}
	svc := New(repo, workflow, instanceLookup{}, nil)
	_, err := svc.Create(context.Background(), "actor", "workspace", CreateInput{WorkflowID: wid, WorkflowVersionID: vid, InstanceID: iid, CronExpression: "*/5 * * * *", Timezone: "UTC"})
	if err == nil {
		t.Fatal("expected unpublished workflow rejection")
	}
}

func TestCreateRejectsEngineWithoutInstalledDeviceRuntime(t *testing.T) {
	wid, vid, iid := uuid.NewString(), uuid.NewString(), uuid.NewString()
	repo := &scheduleRepo{}
	lookup := workflowLookup{
		workflow: automationservice.Workflow{ID: wid, Status: "published", PublishedVersionID: vid},
		version: automationservice.WorkflowVersion{
			ID: vid, Definition: automationservice.Definition{Engine: "puppeteer"},
		},
	}
	svc := New(repo, lookup, instanceLookup{}, nil)
	_, err := svc.Create(context.Background(), "actor", "workspace", CreateInput{
		WorkflowID: wid, WorkflowVersionID: vid, InstanceID: iid,
		CronExpression: "*/5 * * * *", Timezone: "UTC",
	})
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("unsupported runtime error = %v", err)
	}
}
