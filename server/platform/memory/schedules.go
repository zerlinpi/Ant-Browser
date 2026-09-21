package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

type scheduleState struct {
	mu    sync.Mutex
	items map[string]scheduleservice.Schedule
}

var scheduleStates sync.Map

func (s *Store) schedule() *scheduleState {
	if v, ok := scheduleStates.Load(s); ok {
		return v.(*scheduleState)
	}
	v := &scheduleState{items: map[string]scheduleservice.Schedule{}}
	actual, _ := scheduleStates.LoadOrStore(s, v)
	return actual.(*scheduleState)
}

func (s *Store) CreateSchedule(_ context.Context, item scheduleservice.Schedule) (scheduleservice.Schedule, error) {
	st := s.schedule()
	st.mu.Lock()
	defer st.mu.Unlock()
	st.items[item.ID] = item
	return item, nil
}
func (s *Store) FindSchedule(_ context.Context, workspace, id string) (scheduleservice.Schedule, error) {
	st := s.schedule()
	st.mu.Lock()
	defer st.mu.Unlock()
	item, ok := st.items[id]
	if !ok || item.WorkspaceID != workspace {
		return scheduleservice.Schedule{}, scheduleservice.ErrNotFound
	}
	return item, nil
}
func (s *Store) ListSchedules(_ context.Context, workspace string) ([]scheduleservice.Schedule, error) {
	st := s.schedule()
	st.mu.Lock()
	defer st.mu.Unlock()
	items := make([]scheduleservice.Schedule, 0)
	for _, item := range st.items {
		if item.WorkspaceID == workspace {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAt.After(items[j].UpdatedAt) })
	return items, nil
}
func (s *Store) UpdateSchedule(_ context.Context, item scheduleservice.Schedule, expected int64, now time.Time) (scheduleservice.Schedule, error) {
	st := s.schedule()
	st.mu.Lock()
	defer st.mu.Unlock()
	current, ok := st.items[item.ID]
	if !ok || current.WorkspaceID != item.WorkspaceID {
		return scheduleservice.Schedule{}, scheduleservice.ErrNotFound
	}
	if expected < 1 || current.Version != expected {
		return scheduleservice.Schedule{}, scheduleservice.ErrVersionConflict
	}
	item.Version = current.Version + 1
	item.UpdatedAt = now
	st.items[item.ID] = item
	return item, nil
}
func (s *Store) DeleteSchedule(_ context.Context, workspace, id string) error {
	st := s.schedule()
	st.mu.Lock()
	defer st.mu.Unlock()
	item, ok := st.items[id]
	if !ok || item.WorkspaceID != workspace {
		return scheduleservice.ErrNotFound
	}
	delete(st.items, id)
	return nil
}
func (s *Store) SetScheduleEnabled(_ context.Context, workspace, id string, enabled bool, now time.Time) (scheduleservice.Schedule, error) {
	st := s.schedule()
	st.mu.Lock()
	defer st.mu.Unlock()
	item, ok := st.items[id]
	if !ok || item.WorkspaceID != workspace {
		return scheduleservice.Schedule{}, scheduleservice.ErrNotFound
	}
	item.Enabled = enabled
	if enabled {
		item.Status = "active"
	} else {
		item.Status = "paused"
	}
	item.Version++
	item.UpdatedAt = now
	st.items[id] = item
	return item, nil
}

func (s *Store) ClaimDueSchedules(_ context.Context, worker string, now time.Time, limit int) ([]scheduleservice.Dispatch, error) {
	st := s.schedule()
	st.mu.Lock()
	defer st.mu.Unlock()
	if limit <= 0 {
		limit = 100
	}
	out := make([]scheduleservice.Dispatch, 0, limit)
	for _, item := range st.items {
		if len(out) >= limit || !item.Enabled || item.Status != "active" || item.NextRunAt == nil || item.NextRunAt.After(now) || item.LeaseExpiresAt != nil && item.LeaseExpiresAt.After(now) {
			continue
		}
		cron, err := scheduleservice.ParseCron(item.CronExpression)
		if err != nil {
			item.Status = "error"
			item.LastError = err.Error()
			st.items[item.ID] = item
			continue
		}
		loc, err := time.LoadLocation(item.Timezone)
		if err != nil {
			item.Status = "error"
			item.LastError = err.Error()
			st.items[item.ID] = item
			continue
		}
		scheduled := item.NextRunAt.UTC()
		next, err := cron.Next(now, loc)
		if err != nil {
			item.Status = "error"
			item.LastError = err.Error()
			st.items[item.ID] = item
			continue
		}
		expires := now.Add(30 * time.Second)
		item.LeaseOwner = worker
		item.LeaseExpiresAt = &expires
		item.LastRunAt = &scheduled
		item.NextRunAt = &next
		item.LastError = ""
		item.UpdatedAt = now
		st.items[item.ID] = item
		task := taskservice.Task{ID: uuid.NewString(), WorkspaceID: item.WorkspaceID, TaskType: "workflow.execute", WorkflowID: item.WorkflowID, WorkflowVersionID: item.WorkflowVersionID, IdempotencyKey: "schedule:" + item.ID + ":" + scheduled.Format(time.RFC3339), Status: "queued", Payload: map[string]interface{}{"instanceId": item.InstanceID}, RetryLimit: 3, AvailableAt: scheduled, CreatedAt: now, UpdatedAt: now}
		s.mu.Lock()
		if existingID, exists := s.taskIdempotency[task.WorkspaceID+"\x00"+task.IdempotencyKey]; exists {
			task = s.tasks[existingID]
		} else {
			s.tasks[task.ID] = task
			s.taskIdempotency[task.WorkspaceID+"\x00"+task.IdempotencyKey] = task.ID
		}
		s.mu.Unlock()
		out = append(out, scheduleservice.Dispatch{Schedule: item, Task: task})
		item.LeaseOwner = ""
		item.LeaseExpiresAt = nil
		st.items[item.ID] = item
	}
	return out, nil
}

var _ scheduleservice.Repository = (*Store)(nil)
var _ scheduleservice.SchedulerRepository = (*Store)(nil)
