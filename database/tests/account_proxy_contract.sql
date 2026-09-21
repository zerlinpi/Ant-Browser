-- Run after migrations in a disposable PostgreSQL database.
-- Every fixture is rolled back. Any failed assertion aborts execution.
BEGIN;
DO $$
DECLARE
    actor UUID := gen_random_uuid();
    org UUID := gen_random_uuid();
    ws UUID := gen_random_uuid();
    other_ws UUID := gen_random_uuid();
    account UUID := gen_random_uuid();
    proxy UUID := gen_random_uuid();
    task UUID;
    terminal TEXT;
    expected_code TEXT;
BEGIN
    INSERT INTO users(id,email,password_hash) VALUES(actor,actor::text || '@example.test','test-only');
    INSERT INTO organizations(id,name,slug,owner_user_id) VALUES(org,'Contract',org::text,actor);
    INSERT INTO workspaces(id,organization_id,name,slug,created_by)
        VALUES(ws,org,'Contract',ws::text,actor),(other_ws,org,'Other',other_ws::text,actor);
    INSERT INTO accounts(id,workspace_id,platform,external_identifier,status)
        VALUES(account,ws,'amazon',account::text,'pending');
    INSERT INTO proxies(id,workspace_id,name,protocol,host,port,username,connector_type,kernel)
        VALUES(proxy,ws,'Direct','direct','',0,'','xray','direct');
    INSERT INTO proxy_assignments(workspace_id,proxy_id,account_id,target_type)
        VALUES(ws,proxy,account,'account');
    UPDATE proxy_assignments SET deleted_at=now() WHERE workspace_id=ws AND proxy_id=proxy;
    INSERT INTO proxy_assignments(workspace_id,proxy_id,account_id,target_type)
        VALUES(ws,proxy,account,'account');
    BEGIN
        INSERT INTO account_bindings(workspace_id,account_id,binding_type,proxy_id)
            VALUES(other_ws,account,'proxy',proxy);
        RAISE EXCEPTION 'cross-workspace binding accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL;
    END;
    BEGIN
        INSERT INTO proxies(workspace_id,name,protocol,host,port,connector_type,kernel)
            VALUES(ws,'Invalid port','http','localhost',0,'xray','direct');
        RAISE EXCEPTION 'invalid proxy port accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    INSERT INTO proxy_health_checks(workspace_id,proxy_id,request_id,connector_type,kernel)
        VALUES(ws,proxy,gen_random_uuid(),'xray','direct');
    FOREACH terminal IN ARRAY ARRAY['cancelled','failed','dead_letter'] LOOP
        task := gen_random_uuid();
        INSERT INTO tasks(id,workspace_id,idempotency_key,task_type)
            VALUES(task,ws,task::text,'proxy.health_check');
        INSERT INTO proxy_health_checks(workspace_id,proxy_id,request_id,connector_type,kernel)
            VALUES(ws,proxy,task,'xray','direct');
        UPDATE tasks SET status=terminal,completed_at=now() WHERE id=task;
        expected_code := CASE terminal WHEN 'cancelled' THEN 'task_cancelled'
            WHEN 'dead_letter' THEN 'retry_limit_exhausted' ELSE 'task_failed' END;
        IF NOT EXISTS (SELECT 1 FROM proxy_health_checks WHERE request_id=task
            AND status=CASE WHEN terminal='cancelled' THEN 'cancelled' ELSE 'failed' END
            AND error_code=expected_code AND completed_at IS NOT NULL) THEN
            RAISE EXCEPTION 'health check did not follow task terminal state %', terminal;
        END IF;
        -- A later task transition must not rewrite an already finished check.
        UPDATE tasks SET status=CASE WHEN terminal='cancelled' THEN 'failed' ELSE 'cancelled' END
            WHERE id=task;
        IF NOT EXISTS (SELECT 1 FROM proxy_health_checks WHERE request_id=task AND error_code=expected_code) THEN
            RAISE EXCEPTION 'terminal health result overwritten';
        END IF;
    END LOOP;
END $$;
ROLLBACK;
