-- Instances already have composite tenant FKs for profile, fingerprint and
-- device. Close the remaining proxy-assignment reference gap. Existing invalid
-- references deliberately fail migration instead of silently rewriting data.
ALTER TABLE proxy_assignments
    ADD CONSTRAINT proxy_assignments_workspace_id_uq UNIQUE (workspace_id, id);
ALTER TABLE browser_instances
    ADD CONSTRAINT browser_instances_proxy_assignment_tenant_fk
    FOREIGN KEY (workspace_id, proxy_assignment_id)
    REFERENCES proxy_assignments(workspace_id, id);
