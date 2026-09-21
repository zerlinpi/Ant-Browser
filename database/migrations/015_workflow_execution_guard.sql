-- Validate target and pin the published immutable version at acceptance time.
-- Row locks prevent publication/archive/deletion racing the enqueue transaction.
CREATE FUNCTION validate_workflow_task_insert() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    selected workflows%ROWTYPE;
    target UUID;
BEGIN
    IF NEW.task_type <> 'workflow.execute' THEN RETURN NEW; END IF;
    SELECT * INTO selected FROM workflows
      WHERE workspace_id=NEW.workspace_id AND id=NEW.workflow_id FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'workflow not found' USING ERRCODE='23503'; END IF;
    IF selected.status <> 'published' OR selected.published_version_id IS DISTINCT FROM NEW.workflow_version_id THEN
      RAISE EXCEPTION 'workflow version is not published' USING ERRCODE='23503';
    END IF;
    PERFORM 1 FROM workflow_versions WHERE workspace_id=NEW.workspace_id
      AND workflow_id=NEW.workflow_id AND id=NEW.workflow_version_id FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'workflow version not found' USING ERRCODE='23503'; END IF;
    IF jsonb_typeof(NEW.payload) <> 'object' OR NOT (NEW.payload ? 'instanceId')
      OR (NEW.payload - 'instanceId') <> '{}'::jsonb THEN
      RAISE EXCEPTION 'invalid workflow target payload' USING ERRCODE='23503';
    END IF;
    target := (NEW.payload->>'instanceId')::uuid;
    PERFORM 1 FROM browser_instances WHERE workspace_id=NEW.workspace_id
      AND id=target AND deleted_at IS NULL FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'workflow instance not found' USING ERRCODE='23503'; END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER tasks_validate_workflow_insert BEFORE INSERT ON tasks
  FOR EACH ROW EXECUTE FUNCTION validate_workflow_task_insert();
