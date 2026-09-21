BEGIN;
DO $$
DECLARE
 actor UUID := gen_random_uuid();
 org UUID := gen_random_uuid();
 ws UUID := gen_random_uuid();
 other_ws UUID := gen_random_uuid();
 flow UUID := gen_random_uuid();
 selected_version UUID := gen_random_uuid();
 target UUID := gen_random_uuid();
 foreign_target UUID := gen_random_uuid();
 task UUID := gen_random_uuid();
BEGIN
 INSERT INTO users(id,email,password_hash) VALUES(actor,actor::text || '@example.test','test');
 INSERT INTO organizations(id,name,slug,owner_user_id) VALUES(org,'Workflow',org::text,actor);
 INSERT INTO workspaces(id,organization_id,name,slug,created_by) VALUES(ws,org,'One',ws::text,actor),(other_ws,org,'Two',other_ws::text,actor);
 INSERT INTO browser_instances(id,workspace_id,name) VALUES(target,ws,'Target'),(foreign_target,other_ws,'Foreign');
 INSERT INTO workflows(id,workspace_id,name) VALUES(flow,ws,'Flow');
 INSERT INTO workflow_versions(id,workspace_id,workflow_id,version,dsl_schema_version,definition,content_hash)
   VALUES(selected_version,ws,flow,1,'ant-workflow/v1','{}',decode(repeat('00',32),'hex'));
 BEGIN
  INSERT INTO tasks(workspace_id,task_type,workflow_id,workflow_version_id,idempotency_key,payload)
   VALUES(ws,'workflow.execute',flow,selected_version,'draft',jsonb_build_object('instanceId',target));
  RAISE EXCEPTION 'draft workflow task accepted';
 EXCEPTION WHEN foreign_key_violation THEN NULL;
 END;
 UPDATE workflows SET status='published',published_version_id=selected_version WHERE id=flow;
 BEGIN
  INSERT INTO tasks(workspace_id,task_type,workflow_id,workflow_version_id,idempotency_key,payload)
   VALUES(ws,'workflow.execute',flow,selected_version,'foreign-target',jsonb_build_object('instanceId',foreign_target));
  RAISE EXCEPTION 'cross-tenant instance accepted';
 EXCEPTION WHEN foreign_key_violation THEN NULL;
 END;
 INSERT INTO tasks AS t(id,workspace_id,task_type,workflow_id,workflow_version_id,idempotency_key,payload)
   VALUES(task,ws,'workflow.execute',flow,selected_version,'accepted',jsonb_build_object('instanceId',target));
 UPDATE workflows SET status='archived' WHERE id=flow;
 IF NOT EXISTS(SELECT 1 FROM tasks WHERE id=task AND workflow_version_id=selected_version) THEN
  RAISE EXCEPTION 'accepted workflow version changed';
 END IF;
 BEGIN
  INSERT INTO tasks(workspace_id,task_type,workflow_id,workflow_version_id,idempotency_key,payload)
   VALUES(ws,'workflow.execute',flow,selected_version,'archived',jsonb_build_object('instanceId',target));
  RAISE EXCEPTION 'archived workflow accepted';
 EXCEPTION WHEN foreign_key_violation THEN NULL;
 END;
END $$;
ROLLBACK;
