-- v024: hashed, single-use workspace invitations.
CREATE TABLE workspace_invitations (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    email TEXT NOT NULL,
    role_id UUID NOT NULL REFERENCES roles(id),
    token_hash TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','revoked')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ
);
CREATE INDEX workspace_invitations_workspace_idx ON workspace_invitations(workspace_id, status, created_at);
ALTER TABLE workspace_invitations ENABLE ROW LEVEL SECURITY;
ALTER TABLE workspace_invitations FORCE ROW LEVEL SECURITY;
CREATE POLICY workspace_invitations_tenant_policy ON workspace_invitations
    USING (
        workspace_id = cloud_current_workspace_id()
        AND (
            EXISTS (SELECT 1 FROM workspace_members wm WHERE wm.workspace_id = workspace_invitations.workspace_id AND wm.user_id = cloud_current_user_id() AND wm.status = 'active')
            OR lower(email) = lower((SELECT u.email FROM users u WHERE u.id = cloud_current_user_id()))
        )
    )
    WITH CHECK (
        workspace_id = cloud_current_workspace_id()
        AND EXISTS (SELECT 1 FROM workspace_members wm WHERE wm.workspace_id = workspace_invitations.workspace_id AND wm.user_id = cloud_current_user_id() AND wm.status = 'active')
    );
GRANT SELECT, INSERT, UPDATE, DELETE ON workspace_invitations TO ant_control_plane;
