BEGIN;
DO $$
DECLARE
    owner_id UUID := gen_random_uuid();
    org_id UUID := gen_random_uuid();
    ws_a UUID := gen_random_uuid();
    ws_b UUID := gen_random_uuid();
    instance_id UUID := gen_random_uuid();
    proxy_id UUID := gen_random_uuid();
    profile_id UUID := gen_random_uuid();
    assignment_id UUID := gen_random_uuid();
    caught_constraint TEXT;
BEGIN
    INSERT INTO users(id,email,password_hash) VALUES(owner_id,owner_id::text || '@instance.test','test');
    INSERT INTO organizations(id,name,slug,owner_user_id) VALUES(org_id,'Instances',org_id::text,owner_id);
    INSERT INTO workspaces(id,organization_id,name,slug,created_by) VALUES
        (ws_a,org_id,'A',ws_a::text,owner_id), (ws_b,org_id,'B',ws_b::text,owner_id);
    INSERT INTO browser_instances(id,workspace_id,name) VALUES(instance_id,ws_a,'Instance A');
    INSERT INTO browser_profiles(id,workspace_id,name) VALUES(profile_id,ws_b,'Profile B');
    INSERT INTO proxies(id,workspace_id,name,protocol,host,port,connector_type,kernel)
        VALUES(proxy_id,ws_b,'Proxy B','http','example.com',8080,'xray','xray');
    INSERT INTO proxy_assignments(id,workspace_id,proxy_id,profile_id,target_type)
        VALUES(assignment_id,ws_b,proxy_id,profile_id,'profile');
    -- Test UPDATE too: create-time service validation is not sufficient.
    BEGIN
        UPDATE browser_instances SET proxy_assignment_id=assignment_id WHERE id=instance_id;
        RAISE EXCEPTION 'cross-tenant proxy assignment accepted';
    EXCEPTION WHEN foreign_key_violation THEN
        GET STACKED DIAGNOSTICS caught_constraint = CONSTRAINT_NAME;
        IF caught_constraint <> 'browser_instances_proxy_assignment_tenant_fk' THEN RAISE; END IF;
    END;
    INSERT INTO browser_instances(workspace_id,name,profile_id,proxy_assignment_id)
        VALUES(ws_b,'Instance B',profile_id,assignment_id);
END
$$;
ROLLBACK;
