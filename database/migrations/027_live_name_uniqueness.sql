-- v027: resource names, and account identifiers per platform, are unique
-- among live rows only and compared case-insensitively.
--
-- Deleting these resources is a soft delete that keeps the row and its name.
-- The table-level UNIQUE constraints from 002/003/004/011 therefore reserved
-- a deleted resource's name forever, and the stores reported the violation as
-- an internal error. The in-memory store and the fingerprint service already
-- treat names as unique among live rows, case-insensitively; these partial
-- expression indexes make PostgreSQL enforce the same rule. Workflows keep
-- their constraint: they are archived, not deleted, and stay listed.
--
-- Live rows whose names differ only by letter case would violate the new
-- indexes. The migration then fails and rolls back, naming the table and
-- workspace, instead of renaming anything; rename the rows and rerun it.
-- Values are not printed because account identifiers can be e-mail addresses.

DO $$
DECLARE
    duplicate record;
BEGIN
    FOR duplicate IN
        SELECT 'browser_instances' AS resource, workspace_id FROM browser_instances
            WHERE deleted_at IS NULL GROUP BY workspace_id, lower(name) HAVING count(*) > 1
        UNION ALL
        SELECT 'proxies', workspace_id FROM proxies
            WHERE deleted_at IS NULL GROUP BY workspace_id, lower(name) HAVING count(*) > 1
        UNION ALL
        SELECT 'fingerprint_templates', workspace_id FROM fingerprint_templates
            WHERE deleted_at IS NULL GROUP BY workspace_id, lower(name) HAVING count(*) > 1
        UNION ALL
        SELECT 'browser_profiles', workspace_id FROM browser_profiles
            WHERE deleted_at IS NULL GROUP BY workspace_id, lower(name) HAVING count(*) > 1
        UNION ALL
        SELECT 'accounts', workspace_id FROM accounts
            WHERE deleted_at IS NULL GROUP BY workspace_id, platform, lower(external_identifier) HAVING count(*) > 1
    LOOP
        RAISE EXCEPTION 'live % in workspace % have names that differ only by letter case', duplicate.resource, duplicate.workspace_id
            USING HINT = 'Rename or delete the duplicates, then rerun the migration.';
    END LOOP;
END
$$;

ALTER TABLE browser_instances DROP CONSTRAINT browser_instances_workspace_id_name_key;
CREATE UNIQUE INDEX browser_instances_live_name_uq
    ON browser_instances (workspace_id, lower(name)) WHERE deleted_at IS NULL;

ALTER TABLE proxies DROP CONSTRAINT proxies_workspace_id_name_key;
CREATE UNIQUE INDEX proxies_live_name_uq
    ON proxies (workspace_id, lower(name)) WHERE deleted_at IS NULL;

ALTER TABLE fingerprint_templates DROP CONSTRAINT fingerprint_templates_workspace_id_name_key;
CREATE UNIQUE INDEX fingerprint_templates_live_name_uq
    ON fingerprint_templates (workspace_id, lower(name)) WHERE deleted_at IS NULL;

ALTER TABLE browser_profiles DROP CONSTRAINT browser_profiles_workspace_id_name_key;
CREATE UNIQUE INDEX browser_profiles_live_name_uq
    ON browser_profiles (workspace_id, lower(name)) WHERE deleted_at IS NULL;

ALTER TABLE accounts DROP CONSTRAINT accounts_workspace_id_platform_external_identifier_key;
CREATE UNIQUE INDEX accounts_live_identifier_uq
    ON accounts (workspace_id, platform, lower(external_identifier)) WHERE deleted_at IS NULL;
