-- v012: optimistic workflow lifecycle and an explicit published version.

ALTER TABLE workflows ADD COLUMN latest_version INTEGER NOT NULL DEFAULT 0
    CHECK (latest_version >= 0);
ALTER TABLE workflows ADD COLUMN published_version_id UUID;
ALTER TABLE workflows ADD COLUMN version BIGINT NOT NULL DEFAULT 1
    CHECK (version > 0);
ALTER TABLE workflows ADD COLUMN archived_at TIMESTAMPTZ;

UPDATE workflows AS w
SET latest_version = versions.latest_version
FROM (
    SELECT workflow_id, max(version)::integer AS latest_version
    FROM workflow_versions
    GROUP BY workflow_id
) AS versions
WHERE versions.workflow_id = w.id;

ALTER TABLE workflows
    ADD CONSTRAINT workflows_published_version_tenant_fk
    FOREIGN KEY (workspace_id, published_version_id)
    REFERENCES workflow_versions(workspace_id, id);

CREATE INDEX workflows_workspace_status_idx
    ON workflows (workspace_id, status, updated_at DESC);
