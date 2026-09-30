package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

const scheduleColumns = `s.id::text,s.workspace_id::text,s.workflow_id::text,s.workflow_version_id::text,s.instance_id::text,s.cron_expression,s.timezone,s.enabled,s.status,s.next_run_at,s.last_run_at,s.last_error,s.lease_owner,s.lease_expires_at,s.created_at,s.updated_at,s.version`

func scanSchedule(row scanner) (scheduleservice.Schedule, error) {
	var v scheduleservice.Schedule
	err := row.Scan(&v.ID, &v.WorkspaceID, &v.WorkflowID, &v.WorkflowVersionID, &v.InstanceID, &v.CronExpression, &v.Timezone, &v.Enabled, &v.Status, &v.NextRunAt, &v.LastRunAt, &v.LastError, &v.LeaseOwner, &v.LeaseExpiresAt, &v.CreatedAt, &v.UpdatedAt, &v.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return v, scheduleservice.ErrNotFound
	}
	return v, err
}
func (s *Store) CreateSchedule(ctx context.Context, v scheduleservice.Schedule) (scheduleservice.Schedule, error) {
	return scanSchedule(s.pool.QueryRow(ctx, `INSERT INTO schedules AS s (id,workspace_id,workflow_id,workflow_version_id,instance_id,cron_expression,timezone,enabled,status,next_run_at,last_error,version,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING `+scheduleColumns, v.ID, v.WorkspaceID, v.WorkflowID, v.WorkflowVersionID, v.InstanceID, v.CronExpression, v.Timezone, v.Enabled, v.Status, v.NextRunAt, v.LastError, v.Version, v.CreatedAt, v.UpdatedAt))
}
func (s *Store) FindSchedule(ctx context.Context, w, id string) (scheduleservice.Schedule, error) {
	return scanSchedule(s.pool.QueryRow(ctx, `SELECT `+scheduleColumns+` FROM schedules s WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid`, w, id))
}
func (s *Store) ListSchedules(ctx context.Context, w string) ([]scheduleservice.Schedule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+scheduleColumns+` FROM schedules s WHERE s.workspace_id=$1::uuid ORDER BY s.updated_at DESC,s.id`, w)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scheduleservice.Schedule{}
	for rows.Next() {
		v, e := scanSchedule(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) UpdateSchedule(ctx context.Context, v scheduleservice.Schedule, expected int64, now time.Time) (scheduleservice.Schedule, error) {
	updated, err := scanSchedule(s.pool.QueryRow(ctx, `UPDATE schedules s SET cron_expression=$3,timezone=$4,next_run_at=$5,updated_at=$6,version=s.version+1 WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid AND s.version=$7 RETURNING `+scheduleColumns, v.WorkspaceID, v.ID, v.CronExpression, v.Timezone, v.NextRunAt, now, expected))
	if errors.Is(err, scheduleservice.ErrNotFound) {
		if _, findErr := s.FindSchedule(ctx, v.WorkspaceID, v.ID); findErr == nil {
			return scheduleservice.Schedule{}, scheduleservice.ErrVersionConflict
		}
	}
	return updated, err
}
func (s *Store) DeleteSchedule(ctx context.Context, w, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM schedules WHERE workspace_id=$1::uuid AND id=$2::uuid`, w, id)
	if err == nil && tag.RowsAffected() == 0 {
		return scheduleservice.ErrNotFound
	}
	return err
}
func (s *Store) SetScheduleEnabled(ctx context.Context, w, id string, enabled bool, nextRunAt *time.Time, expectedVersion int64, now time.Time) (scheduleservice.Schedule, error) {
	status := "paused"
	if enabled {
		status = "active"
	}
	updated, err := scanSchedule(s.pool.QueryRow(ctx, `UPDATE schedules s SET enabled=$3,status=$4,next_run_at=$5,updated_at=$6,version=s.version+1 WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid AND s.version=$7 RETURNING `+scheduleColumns, w, id, enabled, status, nextRunAt, now, expectedVersion))
	if errors.Is(err, scheduleservice.ErrNotFound) {
		if _, findErr := s.FindSchedule(ctx, w, id); findErr == nil {
			return scheduleservice.Schedule{}, scheduleservice.ErrVersionConflict
		}
	}
	return updated, err
}

func (s *Store) ClaimDueSchedules(ctx context.Context, worker string, now time.Time, limit int) ([]scheduleservice.Dispatch, error) {
	ctx = WithWorkerScope(ctx, "schedule_dispatch")
	if limit <= 0 {
		limit = 100
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollback(ctx, tx)
	rows, err := tx.Query(ctx, `SELECT `+scheduleColumns+` FROM schedules s WHERE s.enabled AND s.status='active' AND s.next_run_at IS NOT NULL AND s.next_run_at <= $1 AND (s.lease_expires_at IS NULL OR s.lease_expires_at <= $1) ORDER BY s.next_run_at,s.id FOR UPDATE SKIP LOCKED LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	items := []scheduleservice.Schedule{}
	for rows.Next() {
		v, e := scanSchedule(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		items = append(items, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]scheduleservice.Dispatch, 0, len(items))
	for _, item := range items {
		cron, err := scheduleservice.ParseCron(item.CronExpression)
		if err != nil {
			_, err = tx.Exec(ctx, `UPDATE schedules SET status='error',last_error=$3,updated_at=$4,lease_owner='',lease_expires_at=NULL WHERE workspace_id=$1::uuid AND id=$2::uuid`, item.WorkspaceID, item.ID, err.Error(), now)
			if err != nil {
				return nil, err
			}
			continue
		}
		loc, err := time.LoadLocation(item.Timezone)
		if err != nil {
			if _, err = tx.Exec(ctx, `UPDATE schedules SET status='error',last_error=$3,updated_at=$4,lease_owner='',lease_expires_at=NULL WHERE workspace_id=$1::uuid AND id=$2::uuid`, item.WorkspaceID, item.ID, err.Error(), now); err != nil {
				return nil, err
			}
			continue
		}
		scheduled := item.NextRunAt.UTC()
		next, err := cron.Next(now, loc)
		if err != nil {
			if _, err = tx.Exec(ctx, `UPDATE schedules SET status='error',last_error=$3,updated_at=$4,lease_owner='',lease_expires_at=NULL WHERE workspace_id=$1::uuid AND id=$2::uuid`, item.WorkspaceID, item.ID, err.Error(), now); err != nil {
				return nil, err
			}
			continue
		}
		// schedule_target_executable (migration 028) checks the pinned
		// workflow version and instance and locks them FOR SHARE until the
		// transaction ends, so publication, archiving or deletion cannot race
		// the enqueue. ant_worker itself has no UPDATE privilege to lock them.
		var executable bool
		if err := tx.QueryRow(ctx, `SELECT schedule_target_executable($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid)`,
			item.WorkspaceID, item.ID, item.WorkflowID, item.WorkflowVersionID, item.InstanceID).Scan(&executable); err != nil {
			return nil, err
		}
		if !executable {
			if _, updateErr := tx.Exec(ctx, `UPDATE schedules SET status='error',last_error='workflow target is no longer executable',updated_at=$3,lease_owner='',lease_expires_at=NULL,version=version+1 WHERE workspace_id=$1::uuid AND id=$2::uuid`, item.WorkspaceID, item.ID, now); updateErr != nil {
				return nil, updateErr
			}
			continue
		}
		taskID := uuid.NewString()
		key := "schedule:" + item.ID + ":" + scheduled.Format(time.RFC3339)
		payloadMap := map[string]interface{}{"instanceId": item.InstanceID}
		payload, _ := json.Marshal(payloadMap)
		desired := taskservice.Task{
			ID: taskID, WorkspaceID: item.WorkspaceID, TaskType: "workflow.execute",
			WorkflowID: item.WorkflowID, WorkflowVersionID: item.WorkflowVersionID,
			IdempotencyKey: key, Status: "queued", Payload: payloadMap, RetryLimit: 3,
			AvailableAt: scheduled, CreatedAt: now, UpdatedAt: now,
		}
		task, err := scanTask(tx.QueryRow(ctx, `INSERT INTO tasks AS t
			(id,workspace_id,task_type,workflow_id,workflow_version_id,idempotency_key,status,payload,retry_limit,available_at,created_at,updated_at)
			VALUES ($1::uuid,$2::uuid,'workflow.execute',$3::uuid,$4::uuid,$5,'queued',$6::jsonb,3,$7,$8,$8)
			ON CONFLICT (workspace_id,idempotency_key) DO UPDATE SET id=t.id
			RETURNING `+taskColumns, taskID, item.WorkspaceID, item.WorkflowID, item.WorkflowVersionID, key, payload, scheduled, now))
		if err != nil {
			return nil, err
		}
		if !taskservice.SameWorkflowRequest(task, desired) {
			if _, updateErr := tx.Exec(ctx, `UPDATE schedules SET status='error',last_error='schedule task idempotency conflict',updated_at=$3,lease_owner='',lease_expires_at=NULL,version=version+1 WHERE workspace_id=$1::uuid AND id=$2::uuid`, item.WorkspaceID, item.ID, now); updateErr != nil {
				return nil, updateErr
			}
			continue
		}
		_, err = tx.Exec(ctx, `UPDATE schedules SET last_run_at=$3,next_run_at=$4,last_error='',lease_owner='',lease_expires_at=NULL,updated_at=$5,version=version+1 WHERE workspace_id=$1::uuid AND id=$2::uuid`, item.WorkspaceID, item.ID, scheduled, next, now)
		if err != nil {
			return nil, err
		}
		out = append(out, scheduleservice.Dispatch{Schedule: item, Task: task})
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

var _ scheduleservice.Repository = (*Store)(nil)
var _ scheduleservice.SchedulerRepository = (*Store)(nil)
