-- v031: workflow names are unique per workspace ignoring letter case,
-- archived workflows included.
--
-- 027 made the other resource names case-insensitive among live rows but
-- kept the exact-match constraint from 005 for workflows, which are archived
-- rather than deleted and stay listed. Names such as "Nightly" and "nightly"
-- are easy to confuse in the workflow list and the schedule picker, so this
-- expression index compares them case-insensitively over every workflow,
-- archived or not. The in-memory store applies the same rule.
--
-- Workflows whose names differ only by letter case would violate the index.
-- The migration then fails and rolls back, naming the table and the affected
-- workspaces but not the names, instead of renaming anything. Run
-- `go run ./cmd/migrate preflight` to list the rows (`preflight -fix` renames
-- all but the oldest of each group), then rerun the migration.
--
-- FORCE ROW LEVEL SECURITY (018) also filters the table owner unless it is a
-- superuser or has BYPASSRLS; the check would then see no rows and pass
-- vacuously. With row_security off such a role gets an error instead, so the
-- check can only pass after it saw every workspace's workflows.

DO $$
DECLARE
    workspaces TEXT;
BEGIN
    SET LOCAL row_security = off;
    SELECT string_agg(DISTINCT duplicate.workspace_id::text, ', ' ORDER BY duplicate.workspace_id::text)
    INTO workspaces
    FROM (
        SELECT workspace_id FROM workflows
        GROUP BY workspace_id, lower(name) HAVING count(*) > 1
    ) AS duplicate;
    SET LOCAL row_security = on;
    IF workspaces IS NOT NULL THEN
        RAISE EXCEPTION 'workflows in workspaces % have names that differ only by letter case', workspaces
            USING HINT = 'Run "go run ./cmd/migrate preflight" to list them, rename the duplicates (or use "preflight -fix"), then rerun the migration.';
    END IF;
EXCEPTION
    WHEN insufficient_privilege THEN
        RAISE EXCEPTION 'the migration role cannot read the workflows of every workspace: %', SQLERRM
            USING HINT = 'Run migrations as a superuser or a role with BYPASSRLS; FORCE ROW LEVEL SECURITY also applies to the table owner.';
END
$$;

ALTER TABLE workflows DROP CONSTRAINT workflows_workspace_id_name_key;
CREATE UNIQUE INDEX workflows_lower_name_uq ON workflows (workspace_id, lower(name));
