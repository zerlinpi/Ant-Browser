# Authoritative PostgreSQL schema

The SQL files in `database/migrations/` are the authoritative cloud schema for
the control-plane and worker. They are intentionally separate from the legacy
desktop SQLite migrations in `backend/internal/database/sqlite.go` and from the
Phase-0 drafts in `server/migrations/`.

## Ordering and policy

Apply migrations in lexical order, once, using a migration runner that records
the filename, checksum, and applied timestamp in a dedicated migration table.
Each file is forward-only: do not edit an applied file and do not add rollback
SQL that silently destroys tenant data. Use expand/contract releases for
breaking changes, with a compatibility window and an explicit data backfill.

The schema uses UUIDs, `timestamptz`, tenant-scoped composite foreign keys,
optimistic `version` columns, idempotency keys, and status checks. Runtime
queries must require a workspace/organization identity and apply the same
tenant predicate as the foreign keys. Migration `008_rls_tenant_guard.sql`
enables RLS for resource tables after the application role and
`app.current_workspace_id`/`app.current_organization_id` session settings are
established. Bootstrap identity rows (organizations, workspaces, and
memberships) remain behind explicit repository guards so a new tenant can be
created before a tenant context exists. Migrations do not grant broad bypass
privileges.

Migration `018_rls_runtime_roles.sql` forces RLS for runtime access, creates
the `ant_control_plane` and `ant_worker` roles with `NOBYPASSRLS`, and grants
the worker only its fixed queue-claim/device-auth surface. See
`docs/postgres-roles.md` for the required migration, control-plane, and worker
connection variables. Repository operations must use the transaction-scoped
Store context; a PostgreSQL connection must never retain tenant settings.

Secrets are never stored as plaintext. Account, proxy, device, lease, refresh,
and license credentials are represented by hashes or envelope-encrypted
ciphertext plus key references. Ciphertext must be produced by the crypto
adapter using a KMS/Vault-managed data-encryption key; the database role must
not have access to the root key.

Profile and automation artifacts are object-store metadata only. Large or
sensitive payloads belong in S3/MinIO under encrypted, workspace-scoped object
keys. Redis, NATS, and object storage are not authoritative for relational
state: PostgreSQL remains the source of truth, while Redis is cache/presence,
NATS JetStream is delivery/queue infrastructure, and MinIO stores blobs.

## Domain map

| Migration | Domain |
| --- | --- |
| 001 | Users, organizations, workspaces, RBAC, sessions, devices, audit |
| 002 | Browser instances, sessions, commands, event replay |
| 003 | Profile revisions, manifests, objects, leases, conflicts, restore |
| 004 | Accounts, encrypted secret metadata, proxies, assignments, health |
| 005 | Workflows, schedules, tasks, worker leases, runs, attempts, artifacts |
| 006 | Notifications, delivery state, analytics, rollups, risk events |
| 007 | Plans, subscriptions, entitlements, usage, releases, licenses |

The first production deployment should run the migrations as a one-shot job,
then start the stateless control-plane and worker. Schema changes must be
covered by migration replay tests and tenant-isolation tests in CI.
