-- v009: columns required by the service contracts introduced after the base
-- schema. This is additive; existing rows retain safe defaults.

ALTER TABLE sessions ADD COLUMN device_id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN revoke_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE refresh_tokens ADD COLUMN consumed_at TIMESTAMPTZ;
ALTER TABLE refresh_tokens ADD COLUMN replaced_by_id UUID REFERENCES refresh_tokens(id);

ALTER TABLE organizations ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);
ALTER TABLE workspaces ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'suspended', 'deleted'));
ALTER TABLE workspaces ADD COLUMN version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0);
ALTER TABLE workspaces ADD COLUMN deleted_at TIMESTAMPTZ;
ALTER TABLE workspace_members ADD COLUMN id UUID DEFAULT gen_random_uuid();
ALTER TABLE workspace_members ADD COLUMN status TEXT NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'invited', 'removed'));
ALTER TABLE workspace_members ADD COLUMN joined_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE workspace_members ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE workspace_members ALTER COLUMN id SET NOT NULL;
ALTER TABLE workspace_members ADD CONSTRAINT workspace_members_id_uq UNIQUE (id);

ALTER TABLE devices ADD COLUMN agent_version TEXT NOT NULL DEFAULT '';
ALTER TABLE devices ADD COLUMN revoked_at TIMESTAMPTZ;
ALTER TABLE device_credentials ADD COLUMN expires_at TIMESTAMPTZ;

ALTER TABLE browser_instances ADD COLUMN fingerprint_template_id UUID;
ALTER TABLE browser_instances ADD COLUMN proxy_assignment_id UUID;
ALTER TABLE browser_instances ADD COLUMN current_revision BIGINT NOT NULL DEFAULT 0 CHECK (current_revision >= 0);
ALTER TABLE browser_instances ADD COLUMN tags JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE instance_commands ADD COLUMN deadline TIMESTAMPTZ;
ALTER TABLE instance_commands ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE instance_commands ADD COLUMN acknowledged_at TIMESTAMPTZ;
ALTER TABLE instance_commands ADD COLUMN failure_message TEXT NOT NULL DEFAULT '';
ALTER TABLE instance_commands DROP CONSTRAINT IF EXISTS instance_commands_status_check;
ALTER TABLE instance_commands ADD CONSTRAINT instance_commands_status_check
    CHECK (status IN ('pending', 'queued', 'accepted', 'running', 'completed', 'failed', 'cancelled', 'expired'));

ALTER TABLE browser_instances DROP CONSTRAINT IF EXISTS browser_instances_desired_state_check;
ALTER TABLE browser_instances ADD CONSTRAINT browser_instances_desired_state_check
    CHECK (desired_state IN ('stopped', 'running', 'paused', 'migrating', 'deleted'));

CREATE TRIGGER workspace_members_set_updated_at BEFORE UPDATE ON workspace_members
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
