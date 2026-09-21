-- v010: task types, terminal diagnostics, and one durable run record per task.

ALTER TABLE tasks ADD COLUMN task_type TEXT NOT NULL DEFAULT 'workflow.execute';
ALTER TABLE tasks ADD COLUMN error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN error_message TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD CONSTRAINT tasks_task_type_check
    CHECK (task_type IN (
        'workflow.execute', 'profile.sync', 'proxy.health_check',
        'notification.deliver', 'analytics.rollup', 'system.healthcheck'
    ));

CREATE UNIQUE INDEX task_runs_task_uq ON task_runs (workspace_id, task_id);
CREATE INDEX tasks_worker_claim_idx
    ON tasks (task_type, status, priority DESC, available_at, created_at)
    WHERE status IN ('queued', 'leased', 'running');
