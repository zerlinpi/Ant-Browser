-- Synchronize request lifecycle even when a worker is lost or a user cancels.
CREATE FUNCTION sync_proxy_health_task_terminal() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.task_type = 'proxy.health_check'
       AND NEW.status IN ('cancelled', 'failed', 'dead_letter')
       AND NEW.status IS DISTINCT FROM OLD.status THEN
        UPDATE proxy_health_checks
        SET status = CASE WHEN NEW.status = 'cancelled' THEN 'cancelled' ELSE 'failed' END,
            error_code = CASE WHEN NEW.status = 'cancelled' THEN 'task_cancelled'
                              WHEN NEW.status = 'dead_letter' THEN 'retry_limit_exhausted'
                              ELSE 'task_failed' END,
            error_message = '',
            completed_at = COALESCE(NEW.completed_at, NEW.updated_at)
        WHERE workspace_id = NEW.workspace_id AND request_id = NEW.id
          AND completed_at IS NULL;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER tasks_sync_proxy_health_terminal
    AFTER UPDATE OF status ON tasks
    FOR EACH ROW EXECUTE FUNCTION sync_proxy_health_task_terminal();
