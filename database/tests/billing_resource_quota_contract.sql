BEGIN;
DO $$
DECLARE
    owner_id UUID := gen_random_uuid();
    member_id UUID := gen_random_uuid();
    extra_id UUID := gen_random_uuid();
    quota_organization_id UUID := gen_random_uuid();
    workspace_a UUID := gen_random_uuid();
    workspace_b UUID := gen_random_uuid();
    caught_constraint TEXT;
BEGIN
    INSERT INTO users(id,email,password_hash) VALUES
        (owner_id,owner_id::text || '@quota.test','test'),
        (member_id,member_id::text || '@quota.test','test'),
        (extra_id,extra_id::text || '@quota.test','test');
    INSERT INTO organizations(id,name,slug,owner_user_id)
        VALUES(quota_organization_id,'Quota Org',quota_organization_id::text,owner_id);
    INSERT INTO workspaces(id,organization_id,name,slug,created_by) VALUES
        (workspace_a,quota_organization_id,'A',workspace_a::text,owner_id),
        (workspace_b,quota_organization_id,'B',workspace_b::text,owner_id);
    INSERT INTO workspace_members(workspace_id,user_id,role_id,status)
        SELECT workspace_a,owner_id,id,'active' FROM roles WHERE code='owner';

    IF NOT EXISTS (
        SELECT 1 FROM subscriptions subscription JOIN plans plan ON plan.id=subscription.plan_id
        WHERE subscription.organization_id=quota_organization_id
          AND subscription.status='active' AND plan.code='free'
    ) THEN
        RAISE EXCEPTION 'new organization did not receive a Free subscription';
    END IF;

    INSERT INTO browser_instances(workspace_id,name) VALUES
        (workspace_a,'one'),(workspace_a,'two'),(workspace_b,'three');
    BEGIN
        INSERT INTO browser_instances(workspace_id,name) VALUES(workspace_b,'four');
        RAISE EXCEPTION 'instance quota was not enforced';
    EXCEPTION WHEN raise_exception THEN
        GET STACKED DIAGNOSTICS caught_constraint = CONSTRAINT_NAME;
        IF caught_constraint IS DISTINCT FROM 'billing_instances_quota' THEN
            RAISE;
        END IF;
    END;

    INSERT INTO workspace_members(workspace_id,user_id,role_id,status)
        SELECT workspace_a,member_id,id,'active' FROM roles WHERE code='operator';
    -- The same person may join a second workspace without consuming a second
    -- organization seat.
    INSERT INTO workspace_members(workspace_id,user_id,role_id,status)
        SELECT workspace_b,member_id,id,'active' FROM roles WHERE code='viewer';
    BEGIN
        INSERT INTO workspace_members(workspace_id,user_id,role_id,status)
            SELECT workspace_b,extra_id,id,'active' FROM roles WHERE code='viewer';
        RAISE EXCEPTION 'team member quota was not enforced';
    EXCEPTION WHEN raise_exception THEN
        GET STACKED DIAGNOSTICS caught_constraint = CONSTRAINT_NAME;
        IF caught_constraint IS DISTINCT FROM 'billing_team_members_quota' THEN
            RAISE;
        END IF;
    END;
END
$$;
ROLLBACK;
