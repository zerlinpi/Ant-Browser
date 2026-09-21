-- Run as migration role on an empty disposable database; all fixtures roll back.
BEGIN;
DO $$
DECLARE
    owner_id UUID := gen_random_uuid();
    invitee_id UUID := gen_random_uuid();
    outsider_id UUID := gen_random_uuid();
    org_id UUID := gen_random_uuid();
    ws_id UUID := gen_random_uuid();
    other_ws_id UUID := gen_random_uuid();
    invitation_id UUID := gen_random_uuid();
    selected_id UUID;
    seen INTEGER;
BEGIN
    INSERT INTO users(id,email,password_hash) VALUES
        (owner_id,owner_id::text || '@invite.test','test'),
        (invitee_id,invitee_id::text || '@invite.test','test'),
        (outsider_id,outsider_id::text || '@invite.test','test');
    INSERT INTO organizations(id,name,slug,owner_user_id) VALUES(org_id,'Invitations',org_id::text,owner_id);
    INSERT INTO workspaces(id,organization_id,name,slug,created_by) VALUES
        (ws_id,org_id,'Invitations',ws_id::text,owner_id),
        (other_ws_id,org_id,'Other',other_ws_id::text,owner_id);
    INSERT INTO workspace_members(workspace_id,user_id,role_id,status)
        SELECT ws_id,owner_id,id,'active' FROM roles WHERE code='owner';

    SET LOCAL ROLE ant_control_plane;
    PERFORM set_config('app.current_workspace_id',ws_id::text,true);
    PERFORM set_config('app.current_user_id',owner_id::text,true);
    INSERT INTO workspace_invitations(id,workspace_id,email,role_id,token_hash,expires_at)
        SELECT invitation_id,ws_id,invitee_id::text || '@invite.test',id,'one-time-hash',now()+interval '1 day'
        FROM roles WHERE code='viewer';
    SELECT count(*) INTO seen FROM workspace_invitations;
    IF seen <> 1 THEN RAISE EXCEPTION 'owner cannot list invitation'; END IF;

    PERFORM set_config('app.current_user_id',outsider_id::text,true);
    SELECT count(*) INTO seen FROM workspace_invitations;
    IF seen <> 0 THEN RAISE EXCEPTION 'outsider read invitation'; END IF;
    PERFORM set_config('app.current_user_id',invitee_id::text,true);
    PERFORM set_config('app.current_workspace_id',other_ws_id::text,true);
    SELECT count(*) INTO seen FROM workspace_invitations;
    IF seen <> 0 THEN RAISE EXCEPTION 'invitation crossed workspace boundary'; END IF;

    PERFORM set_config('app.current_workspace_id',ws_id::text,true);
    SELECT i.id INTO selected_id FROM workspace_invitations i JOIN roles r ON r.id=i.role_id
        WHERE i.token_hash='one-time-hash' FOR UPDATE OF i;
    IF selected_id IS DISTINCT FROM invitation_id THEN RAISE EXCEPTION 'invitee cannot lock invitation'; END IF;
    -- Same transaction as acceptance: membership first, then token consumption.
    INSERT INTO workspace_members(workspace_id,user_id,role_id,status)
        SELECT ws_id,invitee_id,id,'active' FROM roles WHERE code='viewer';
    UPDATE workspace_invitations SET status='accepted',accepted_at=now() WHERE id=invitation_id;
    IF NOT FOUND THEN RAISE EXCEPTION 'acceptance did not consume invitation'; END IF;
    IF EXISTS(SELECT 1 FROM workspace_invitations WHERE id=invitation_id AND status='pending') THEN
        RAISE EXCEPTION 'accepted token remained pending';
    END IF;
    -- A member still cannot move the invitation into a different workspace.
    BEGIN
        UPDATE workspace_invitations SET workspace_id=other_ws_id WHERE id=invitation_id;
        RAISE EXCEPTION 'cross-workspace invitation mutation succeeded';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    RESET ROLE;
END
$$;
ROLLBACK;
