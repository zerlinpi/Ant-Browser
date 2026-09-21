-- v016: durable schedule runtime state and a tenant-safe workflow-version pin.
ALTER TABLE schedules ADD COLUMN instance_id UUID;
ALTER TABLE schedules ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);
ALTER TABLE schedules ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'paused', 'error'));
ALTER TABLE schedules ADD COLUMN last_run_at TIMESTAMPTZ;
ALTER TABLE schedules ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN lease_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE schedules ADD COLUMN lease_expires_at TIMESTAMPTZ;

-- Existing rows (if any) must be reviewed before becoming executable. New
-- schedules always require an instance target; this keeps old data migratable.
ALTER TABLE schedules ADD CONSTRAINT schedules_instance_fk
    FOREIGN KEY (workspace_id, instance_id) REFERENCES browser_instances(workspace_id, id);
-- Preserve old rows without inventing an execution target. They are inert
-- until edited with a real instance by the API.
UPDATE schedules SET enabled = false, status = 'error', last_error = 'instance target is required'
    WHERE instance_id IS NULL;
ALTER TABLE schedules ADD CONSTRAINT schedules_instance_required
    CHECK (instance_id IS NOT NULL OR status = 'error');
ALTER TABLE workflow_versions ADD CONSTRAINT workflow_versions_workspace_workflow_id_uq
    UNIQUE (workspace_id, workflow_id, id);
ALTER TABLE schedules ADD CONSTRAINT schedules_workflow_version_matches_workflow
    FOREIGN KEY (workspace_id, workflow_id, workflow_version_id)
    REFERENCES workflow_versions(workspace_id, workflow_id, id);

CREATE INDEX schedules_due_idx ON schedules (next_run_at, id)
    WHERE enabled AND status = 'active' AND next_run_at IS NOT NULL;
CREATE INDEX schedules_workspace_idx ON schedules (workspace_id, updated_at DESC);
