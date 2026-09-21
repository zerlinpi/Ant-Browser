package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

const taskColumns = `
	t.id::text, t.workspace_id::text, t.task_type,
	COALESCE(t.workflow_id::text, ''), COALESCE(t.workflow_version_id::text, ''),
	COALESCE(t.requested_by::text, ''), t.idempotency_key, t.status,
	t.priority, t.payload, t.retry_limit, t.available_at, t.lease_owner,
	t.lease_expires_at, t.created_at, t.updated_at, t.completed_at,
	t.error_code, t.error_message`

func (s *Store) CreateTask(ctx context.Context, task taskservice.Task) (taskservice.Task, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: task.WorkspaceID})
	// Replay an already accepted execution even if its workflow was archived later.
	existing, findErr := scanTask(s.pool.QueryRow(ctx, `SELECT `+taskColumns+` FROM tasks t WHERE t.workspace_id=$1::uuid AND t.idempotency_key=$2`, task.WorkspaceID, task.IdempotencyKey))
	if findErr == nil {
		if !taskservice.SameWorkflowRequest(existing, task) {
			return taskservice.Task{}, taskservice.ErrStateConflict
		}
		return existing, nil
	}
	if !errors.Is(findErr, taskservice.ErrNotFound) {
		return taskservice.Task{}, findErr
	}
	payload, err := json.Marshal(task.Payload)
	if err != nil {
		return taskservice.Task{}, err
	}
	created, err := scanTask(s.pool.QueryRow(ctx, `
		INSERT INTO tasks AS t (
			id, workspace_id, task_type, workflow_id, workflow_version_id,
			requested_by, idempotency_key, status, priority, payload,
			retry_limit, available_at, lease_owner, lease_expires_at,
			created_at, updated_at, completed_at, error_code, error_message
		) VALUES (
			$1::uuid, $2::uuid, $3, NULLIF($4, '')::uuid, NULLIF($5, '')::uuid,
			NULLIF($6, '')::uuid, $7, $8, $9, $10::jsonb,
			$11, $12, $13, $14, $15, $16, $17, $18, $19
		)
		ON CONFLICT (workspace_id, idempotency_key) DO NOTHING
		RETURNING `+taskColumns+`
	`, task.ID, task.WorkspaceID, task.TaskType, task.WorkflowID, task.WorkflowVersionID,
		task.RequestedBy, task.IdempotencyKey, task.Status, task.Priority, payload,
		task.RetryLimit, task.AvailableAt, task.LeaseOwner, task.LeaseExpiresAt,
		task.CreatedAt, task.UpdatedAt, task.CompletedAt, task.ErrorCode, task.ErrorMessage))
	if err == nil {
		return created, nil
	}
	if errors.Is(err, taskservice.ErrNotFound) {
		existing, findErr := scanTask(s.pool.QueryRow(ctx, `
			SELECT `+taskColumns+` FROM tasks t
			WHERE t.workspace_id = $1::uuid AND t.idempotency_key = $2
		`, task.WorkspaceID, task.IdempotencyKey))
		if findErr == nil && !taskservice.SameWorkflowRequest(existing, task) {
			return taskservice.Task{}, taskservice.ErrStateConflict
		}
		return existing, findErr
	}
	if isForeignKeyViolation(err) {
		return taskservice.Task{}, taskservice.ErrNotFound
	}
	if isBillingQuotaViolation(err) {
		return taskservice.Task{}, taskservice.ErrQuotaExceeded
	}
	return taskservice.Task{}, err
}

func (s *Store) FindTask(ctx context.Context, workspaceID, taskID string) (taskservice.Task, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	return scanTask(s.pool.QueryRow(ctx, `
		SELECT `+taskColumns+` FROM tasks t
		WHERE t.workspace_id = $1::uuid AND t.id = $2::uuid
	`, workspaceID, taskID))
}

func (s *Store) ListTasks(ctx context.Context, workspaceID string, limit int) ([]taskservice.Task, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	rows, err := s.pool.Query(ctx, `
		SELECT `+taskColumns+` FROM tasks t
		WHERE t.workspace_id = $1::uuid
		ORDER BY t.created_at DESC, t.id DESC
		LIMIT $2
	`, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]taskservice.Task, 0)
	for rows.Next() {
		task, scanErr := scanTask(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, task)
	}
	return items, rows.Err()
}

func (s *Store) CancelTask(ctx context.Context, workspaceID, taskID string, now time.Time) (taskservice.Task, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return taskservice.Task{}, err
	}
	defer rollback(ctx, tx)
	task, err := scanTask(tx.QueryRow(ctx, `
		SELECT `+taskColumns+` FROM tasks t
		WHERE t.workspace_id = $1::uuid AND t.id = $2::uuid
		FOR UPDATE
	`, workspaceID, taskID))
	if err != nil {
		return taskservice.Task{}, err
	}
	if task.Status == "succeeded" || task.Status == "failed" || task.Status == "dead_letter" {
		return taskservice.Task{}, taskservice.ErrStateConflict
	}
	if task.Status != "cancelled" {
		task, err = scanTask(tx.QueryRow(ctx, `
			UPDATE tasks AS t
			SET status = 'cancelled', completed_at = $3, updated_at = $3,
			    lease_owner = '', lease_expires_at = NULL
			WHERE t.workspace_id = $1::uuid AND t.id = $2::uuid
			RETURNING `+taskColumns+`
		`, workspaceID, taskID, now))
		if err != nil {
			return taskservice.Task{}, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_runs
		SET status = 'cancelled', finished_at = COALESCE(finished_at, $3), error_code = 'task_cancelled'
		WHERE workspace_id = $1::uuid AND task_id = $2::uuid
		  AND status IN ('queued', 'running')
	`, workspaceID, taskID, now); err != nil {
		return taskservice.Task{}, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_attempts AS a
		SET status = 'expired', finished_at = COALESCE(a.finished_at, $3), error_code = 'task_cancelled'
		FROM task_runs AS r
		WHERE r.workspace_id = $1::uuid AND r.task_id = $2::uuid
		  AND a.workspace_id = r.workspace_id AND a.task_run_id = r.id
		  AND a.status IN ('leased', 'running')
	`, workspaceID, taskID, now); err != nil {
		return taskservice.Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return taskservice.Task{}, err
	}
	return task, nil
}

func (s *Store) ClaimNextTask(ctx context.Context, workerID string, supportedTypes []string, leaseTTL time.Duration, now time.Time) (taskservice.Lease, error) {
	ctx = WithWorkerScope(ctx, "task_claim")
	return s.claimTasks(ctx, workerID, supportedTypes, leaseTTL, now, "", "")
}

func (s *Store) ClaimDeviceWorkflow(ctx context.Context, workspaceID, deviceID string, leaseTTL time.Duration, now time.Time) (taskservice.Lease, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	return s.claimTasks(ctx, "device:"+deviceID, []string{"workflow.execute"}, leaseTTL, now, workspaceID, deviceID)
}

func (s *Store) claimTasks(ctx context.Context, workerID string, supportedTypes []string, leaseTTL time.Duration, now time.Time, workspaceID, deviceID string) (taskservice.Lease, error) {
	for discarded := 0; discarded < 16; discarded++ {
		lease, retry, err := s.claimOneTask(ctx, workerID, supportedTypes, leaseTTL, now, workspaceID, deviceID)
		if err != nil {
			return taskservice.Lease{}, err
		}
		if !retry {
			return lease, nil
		}
	}
	return taskservice.Lease{}, taskservice.ErrNoWork
}

func (s *Store) claimOneTask(ctx context.Context, workerID string, supportedTypes []string, leaseTTL time.Duration, now time.Time, workspaceID, deviceID string) (taskservice.Lease, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return taskservice.Lease{}, false, err
	}
	defer rollback(ctx, tx)
	task, err := scanTask(tx.QueryRow(ctx, `
		SELECT `+taskColumns+`
		FROM tasks t
		WHERE t.task_type = ANY($1::text[])
		  AND (($4 = '' AND t.task_type <> 'workflow.execute') OR
		    ($4 <> '' AND t.workspace_id::text=$3 AND EXISTS (
		      SELECT 1 FROM browser_instances i JOIN devices d ON d.id=i.assigned_device_id AND d.workspace_id=i.workspace_id
		      JOIN workflow_versions v ON v.workspace_id=t.workspace_id AND v.workflow_id=t.workflow_id AND v.id=t.workflow_version_id
		      WHERE i.workspace_id=t.workspace_id AND i.id::text=t.payload->>'instanceId'
		      AND i.assigned_device_id::text=$4 AND i.deleted_at IS NULL AND d.revoked_at IS NULL
		      AND v.definition->>'engine' IN ('playwright','cdp')
		      FOR SHARE OF i,d
		    )))
		  AND (
		    (t.status = 'queued' AND t.available_at <= $2)
		    OR (t.status IN ('leased', 'running') AND t.lease_expires_at <= $2)
		  )
		ORDER BY t.priority DESC, t.available_at, t.created_at, t.id
		FOR UPDATE OF t SKIP LOCKED
		LIMIT 1
	`, supportedTypes, now, workspaceID, deviceID))
	if errors.Is(err, taskservice.ErrNotFound) {
		return taskservice.Lease{}, false, taskservice.ErrNoWork
	}
	if err != nil {
		return taskservice.Lease{}, false, err
	}

	var runID string
	var attemptCount int
	err = tx.QueryRow(ctx, `
		SELECT id::text, attempt_count FROM task_runs
		WHERE workspace_id = $1::uuid AND task_id = $2::uuid
		FOR UPDATE
	`, task.WorkspaceID, task.ID).Scan(&runID, &attemptCount)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		runID = uuid.NewString()
		attemptCount = 0
	case err != nil:
		return taskservice.Lease{}, false, err
	}
	attempt := attemptCount + 1
	if attempt > task.RetryLimit+1 {
		_, err = tx.Exec(ctx, `
			UPDATE tasks
			SET status = 'dead_letter', completed_at = $3, updated_at = $3,
			    lease_owner = '', lease_expires_at = NULL,
			    error_code = 'retry_limit_exhausted',
			    error_message = 'Task lease expired after the retry limit was exhausted'
			WHERE workspace_id = $1::uuid AND id = $2::uuid
		`, task.WorkspaceID, task.ID, now)
		if err != nil {
			return taskservice.Lease{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return taskservice.Lease{}, false, err
		}
		return taskservice.Lease{}, true, nil
	}

	if attemptCount > 0 {
		if _, err := tx.Exec(ctx, `
			UPDATE task_attempts
			SET status = 'expired', finished_at = COALESCE(finished_at, $3),
			    error_code = CASE WHEN error_code = '' THEN 'lease_expired' ELSE error_code END
			WHERE workspace_id = $1::uuid AND task_run_id = $2::uuid
			  AND status IN ('leased', 'running')
		`, task.WorkspaceID, runID, now); err != nil {
			return taskservice.Lease{}, false, err
		}
	}
	expiresAt := now.Add(leaseTTL)
	task, err = scanTask(tx.QueryRow(ctx, `
		UPDATE tasks AS t
		SET status = 'leased', lease_owner = $3, lease_expires_at = $4,
		    updated_at = $5, error_code = '', error_message = ''
		WHERE t.workspace_id = $1::uuid AND t.id = $2::uuid
		RETURNING `+taskColumns+`
	`, task.WorkspaceID, task.ID, workerID, expiresAt, now))
	if err != nil {
		return taskservice.Lease{}, false, err
	}
	if attemptCount == 0 {
		_, err = tx.Exec(ctx, `
			INSERT INTO task_runs (
				id, workspace_id, task_id, attempt_count, status,
				started_at, finished_at, result, error_code
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 'queued', NULL, NULL, '{}'::jsonb, '')
		`, runID, task.WorkspaceID, task.ID, attempt)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE task_runs
			SET attempt_count = $3, status = 'queued', started_at = NULL,
			    finished_at = NULL, result = '{}'::jsonb, error_code = ''
			WHERE workspace_id = $1::uuid AND id = $2::uuid
		`, task.WorkspaceID, runID, attempt)
	}
	if err != nil {
		return taskservice.Lease{}, false, err
	}
	attemptID := uuid.NewString()
	_, err = tx.Exec(ctx, `
		INSERT INTO task_attempts (
			id, workspace_id, task_run_id, attempt, worker_id, status,
			lease_expires_at, started_at, finished_at, error_code
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4, $5, 'leased', $6, $7, NULL, '')
	`, attemptID, task.WorkspaceID, runID, attempt, workerID, expiresAt, now)
	if err != nil {
		return taskservice.Lease{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return taskservice.Lease{}, false, err
	}
	return taskservice.Lease{
		Task: task, RunID: runID, AttemptID: attemptID, Attempt: attempt, ExpiresAt: expiresAt,
	}, false, nil
}

func (s *Store) StartTask(ctx context.Context, lease taskservice.Lease, now time.Time) error {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: lease.Task.WorkspaceID})
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := lockCurrentTaskLease(ctx, tx, lease, now); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status = 'running', updated_at = $4
		WHERE workspace_id = $1::uuid AND id = $2::uuid AND lease_owner = $3
		  AND status = 'leased' AND lease_expires_at > $4
	`, lease.Task.WorkspaceID, lease.Task.ID, lease.Task.LeaseOwner, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return taskservice.ErrStateConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_runs SET status = 'running', started_at = COALESCE(started_at, $3)
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, lease.Task.WorkspaceID, lease.RunID, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_attempts SET status = 'running', started_at = $4
		WHERE workspace_id = $1::uuid AND id = $2::uuid AND task_run_id = $3::uuid AND status = 'leased'
	`, lease.Task.WorkspaceID, lease.AttemptID, lease.RunID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) CompleteTask(ctx context.Context, lease taskservice.Lease, result map[string]interface{}, now time.Time) error {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: lease.Task.WorkspaceID})
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := lockCurrentTaskLease(ctx, tx, lease, now); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status = 'succeeded', completed_at = $4, updated_at = $4,
		    lease_owner = '', lease_expires_at = NULL, error_code = '', error_message = ''
		WHERE workspace_id = $1::uuid AND id = $2::uuid AND lease_owner = $3
		  AND status IN ('leased', 'running')
	`, lease.Task.WorkspaceID, lease.Task.ID, lease.Task.LeaseOwner, now)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return taskservice.ErrStateConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_runs
		SET status = 'succeeded', finished_at = $3, result = $4::jsonb, error_code = ''
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, lease.Task.WorkspaceID, lease.RunID, now, resultJSON); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_attempts SET status = 'succeeded', finished_at = $4, error_code = ''
		WHERE workspace_id = $1::uuid AND id = $2::uuid AND task_run_id = $3::uuid
	`, lease.Task.WorkspaceID, lease.AttemptID, lease.RunID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) FailTask(ctx context.Context, lease taskservice.Lease, code, message string, retryable bool, retryAt, now time.Time) error {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: lease.Task.WorkspaceID})
	status := "failed"
	completedAt := interface{}(now)
	availableAt := lease.Task.AvailableAt
	if retryable && lease.Attempt <= lease.Task.RetryLimit {
		status = "queued"
		completedAt = nil
		availableAt = retryAt
	} else if retryable {
		status = "dead_letter"
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := lockCurrentTaskLease(ctx, tx, lease, now); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE tasks
		SET status = $4, available_at = $5, completed_at = $6, updated_at = $7,
		    lease_owner = '', lease_expires_at = NULL, error_code = $8, error_message = $9
		WHERE workspace_id = $1::uuid AND id = $2::uuid AND lease_owner = $3
		  AND status IN ('leased', 'running')
	`, lease.Task.WorkspaceID, lease.Task.ID, lease.Task.LeaseOwner, status,
		availableAt, completedAt, now, code, message)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return taskservice.ErrStateConflict
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_runs SET status = 'failed', finished_at = $3, error_code = $4
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, lease.Task.WorkspaceID, lease.RunID, now, code); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE task_attempts SET status = 'failed', finished_at = $4, error_code = $5
		WHERE workspace_id = $1::uuid AND id = $2::uuid AND task_run_id = $3::uuid
	`, lease.Task.WorkspaceID, lease.AttemptID, lease.RunID, now, code); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Lock the task before validating the attempt. A worker identity can be reused
// across retries; only the current unexpired attempt may change task state.
func lockCurrentTaskLease(ctx context.Context, tx pgx.Tx, lease taskservice.Lease, now time.Time) error {
	var id string
	err := tx.QueryRow(ctx, `SELECT id::text FROM tasks
		WHERE workspace_id=$1::uuid AND id=$2::uuid AND lease_owner=$3
		AND status IN ('leased','running') AND lease_expires_at>$4 FOR UPDATE`,
		lease.Task.WorkspaceID, lease.Task.ID, lease.Task.LeaseOwner, now).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskservice.ErrStateConflict
	}
	if err != nil {
		return err
	}
	// Separate statement after obtaining the lock observes any retry that
	// committed while we waited, including when the worker name is unchanged.
	if strings.HasPrefix(lease.Task.LeaseOwner, "device:") {
		err = tx.QueryRow(ctx, `SELECT i.id::text FROM tasks t
		 JOIN browser_instances i ON i.workspace_id=t.workspace_id AND i.id::text=t.payload->>'instanceId'
		 JOIN devices d ON d.workspace_id=i.workspace_id AND d.id=i.assigned_device_id
		 WHERE t.workspace_id=$1::uuid AND t.id=$2::uuid AND d.id::text=$3 AND d.revoked_at IS NULL AND i.deleted_at IS NULL
		 FOR SHARE OF i,d`, lease.Task.WorkspaceID, lease.Task.ID, strings.TrimPrefix(lease.Task.LeaseOwner, "device:")).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return taskservice.ErrStateConflict
		}
		if err != nil {
			return err
		}
	}
	err = tx.QueryRow(ctx, `SELECT t.id::text FROM tasks t
		JOIN task_runs r ON r.workspace_id=t.workspace_id AND r.task_id=t.id
		JOIN task_attempts a ON a.workspace_id=r.workspace_id AND a.task_run_id=r.id
		WHERE t.workspace_id=$1::uuid AND t.id=$2::uuid AND t.lease_owner=$3
		AND t.status IN ('leased','running') AND t.lease_expires_at>$4
		AND r.id=$5::uuid AND a.id=$6::uuid AND a.attempt=$7
		AND a.attempt=r.attempt_count AND a.worker_id=$3
		AND a.status IN ('leased','running') AND a.lease_expires_at>$4`, lease.Task.WorkspaceID, lease.Task.ID, lease.Task.LeaseOwner,
		now, lease.RunID, lease.AttemptID, lease.Attempt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskservice.ErrStateConflict
	}
	return err
}

func scanTask(row scanner) (taskservice.Task, error) {
	var task taskservice.Task
	var payload []byte
	if err := row.Scan(
		&task.ID, &task.WorkspaceID, &task.TaskType,
		&task.WorkflowID, &task.WorkflowVersionID, &task.RequestedBy,
		&task.IdempotencyKey, &task.Status, &task.Priority, &payload,
		&task.RetryLimit, &task.AvailableAt, &task.LeaseOwner, &task.LeaseExpiresAt,
		&task.CreatedAt, &task.UpdatedAt, &task.CompletedAt,
		&task.ErrorCode, &task.ErrorMessage,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return taskservice.Task{}, taskservice.ErrNotFound
		}
		return taskservice.Task{}, err
	}
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &task.Payload); err != nil {
			return taskservice.Task{}, err
		}
	}
	if task.Payload == nil {
		task.Payload = map[string]interface{}{}
	}
	return task, nil
}

func (s *Store) FindTaskLease(ctx context.Context, workspaceID, taskID string) (taskservice.Lease, error) {
	ctx = WithTenantScope(ctx, TenantScope{WorkspaceID: workspaceID})
	task, err := s.FindTask(ctx, workspaceID, taskID)
	if err != nil {
		return taskservice.Lease{}, err
	}
	lease := taskservice.Lease{Task: task}
	var workerID string
	err = s.pool.QueryRow(ctx, `SELECT r.id::text,a.id::text,a.attempt,a.lease_expires_at,a.worker_id
 FROM task_runs r JOIN task_attempts a ON a.workspace_id=r.workspace_id AND a.task_run_id=r.id AND a.attempt=r.attempt_count
 WHERE r.workspace_id=$1::uuid AND r.task_id=$2::uuid`, workspaceID, taskID).Scan(&lease.RunID, &lease.AttemptID, &lease.Attempt, &lease.ExpiresAt, &workerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return taskservice.Lease{}, taskservice.ErrNotFound
	}
	lease.Task.LeaseOwner = workerID
	return lease, err
}
