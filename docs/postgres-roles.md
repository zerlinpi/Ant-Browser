# PostgreSQL runtime roles

RLS is enforced for runtime connections by migration `018_rls_runtime_roles.sql`.
Do not run the services as the migration user, a table owner, or a role with
`BYPASSRLS`.

Use separate connection URLs:

| Process | Variable | Role | Purpose |
| --- | --- | --- | --- |
| migration job | `ANT_MIGRATION_DATABASE_URL` | migration owner | Applies forward-only migrations and owns the schema |
| control plane | `ANT_DATABASE_URL` | `ant_control_plane` | HTTP repository operations with transaction-local tenant scope |
| worker | `ANT_WORKER_DATABASE_URL` | `ant_worker` | Queue claim/device authentication system paths plus tenant-scoped execution |

The migration job must grant the runtime roles table privileges after creating
the schema. Provision login/password or TLS credentials outside migrations;
the migration SQL intentionally creates `NOLOGIN`, `NOSUPERUSER`,
`NOBYPASSRLS` roles. In production `ANT_WORKER_DATABASE_URL` is required and
must not be the control-plane URL. Development may temporarily fall back to
`ANT_DATABASE_URL` for the worker, but this is not a production configuration.

The worker's cross-workspace queue claim and scheduler dispatch use fixed
server code paths (`task_claim` and `schedule_dispatch`). All other worker and
agent repository calls carry the authenticated workspace and execute under the
normal tenant policy. Pre-workspace device authentication goes through the
single `authenticate_device(UUID, TEXT)` SECURITY DEFINER function, owned by a
NOLOGIN role; runtime roles receive EXECUTE only and cannot use it for general
device or credential reads. Never pass arbitrary `app.system_operation` values
from HTTP input.

## Provisioning runtime logins

`deploy/postgres/init-runtime-roles.sh` is a `/docker-entrypoint-initdb.d`
script. The PostgreSQL image runs it **only when it initialises an empty data
directory**, i.e. the first start of a new `postgres-data` volume. There it
creates `ant_control_plane` and `ant_worker` as `LOGIN NOSUPERUSER NOCREATEDB
NOCREATEROLE NOINHERIT NOBYPASSRLS` roles with `CONTROL_PLANE_DB_PASSWORD` and
`WORKER_DB_PASSWORD`, before the migration job grants their privileges.

It never runs for a reused volume or an external/managed database. In those
cases migration 018 creates both roles as `NOLOGIN`, and the services fail
with `FATAL: role "ant_control_plane" is not permitted to log in` until an
operator enables login. After the migrations have run and before starting the
control plane or worker, run as a superuser or as the role that applied
migration 018 (on PostgreSQL 16+ a `CREATEROLE` role also needs `ADMIN OPTION`
on these roles, which their creator has):

```sql
ALTER ROLE ant_control_plane WITH LOGIN PASSWORD '<CONTROL_PLANE_DB_PASSWORD>';
ALTER ROLE ant_worker WITH LOGIN PASSWORD '<WORKER_DB_PASSWORD>';
```

Use the same passwords that appear in `CONTROL_PLANE_DATABASE_URL` and
`WORKER_DATABASE_URL`. Running the same statements with new passwords rotates
the credentials; update the URLs and restart the services afterwards.

A literal password in SQL can end up in shell history or, with
`log_statement = 'ddl'` or `'all'`, in the server log. Prefer passing it as a
psql variable (as the init script does) or setting it with psql's
`\password ant_control_plane`, which sends only a SCRAM hash:

```sh
psql "$MIGRATION_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -v control_password="$CONTROL_PLANE_DB_PASSWORD" \
  -v worker_password="$WORKER_DB_PASSWORD" <<'SQL'
ALTER ROLE ant_control_plane WITH LOGIN PASSWORD :'control_password';
ALTER ROLE ant_worker WITH LOGIN PASSWORD :'worker_password';
SQL
```

`docs/cloud-deployment.md` shows the same step for a reused Compose volume.
Verify the result; both rows must show `rolcanlogin = t`, `rolsuper = f` and
`rolbypassrls = f`:

```sql
SELECT rolname, rolcanlogin, rolsuper, rolbypassrls
FROM pg_roles
WHERE rolname IN ('ant_control_plane', 'ant_worker');
```

The migration, control-plane, and worker URLs must use different principals in
production.
