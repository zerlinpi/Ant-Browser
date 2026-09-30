# Cloud deployment

The cloud target is a monolith-first Go control-plane plus a separately
scalable worker. Domain packages share one `server` Go module; they are not
deployed as a collection of premature microservices. The hosted frontend image
and `desktop/wails-client` both use the same `desktop/vue3-ui` commercial UI
build. The Wails executable and local Browser Agent/Runtime are not placed in
the cloud images.

## Local acceptance

1. Copy `deploy/.env.example` to `deploy/.env` and replace every placeholder.
   The file documents how to generate each secret: `openssl rand -hex 32` for
   passwords and `JWT_SIGNING_KEY` (at least 32 characters), and
   `openssl rand -base64 32` for `SECRET_MASTER_KEY`, which must decode to
   exactly 32 bytes (on Windows, Git Bash ships `openssl`). Keep the
   username/password embedded in `NATS_URL`
   synchronized with `NATS_USER`/`NATS_PASSWORD`; the same applies to
   `REDIS_URL`/`REDIS_PASSWORD` and each `*_DATABASE_URL` and its password.
2. Keep `APP_ENV=production` (the template default). `development` disables
   every production safety check: required durable dependencies, rejection of
   the development JWT and master keys, the required CORS origin list, the
   plain-SMTP ban, and the dedicated worker database URL. Use it only for a
   throwaway stack on your own machine.
3. From the repository root, run:

   ```sh
   docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up --build
   ```

4. Verify `GET http://localhost:8088/healthz` and
   `GET http://localhost:8088/readyz` (`/health` and `/ready` are aliases).
   Nginx proxies all four to the control plane, so a 200 always comes from the
   API and never from the SPA fallback.
5. Stop with `docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml down`.

The `migrate` one-shot service applies `database/migrations/` before the
control-plane and worker start. PostgreSQL is authoritative for commands,
tasks and their recovery; Redis is used for short-lived cache/rate-limit/
presence data, while the current NATS client emits non-authoritative wake and
fan-out signals even though Compose enables JetStream. MinIO/S3 stores Profile
and automation object bytes; callers must not claim end-to-end encrypted
Profile sync until the desktop upload/download encryption path is configured
and verified.
The bundled Compose topology requires password authentication for both Redis
and NATS and keeps all dependency ports private to the `cloud` network.
The distroless control-plane image includes a dedicated no-proxy readiness
probe, and Nginx waits for `/readyz` to pass before accepting traffic. MinIO's
health check uses the image's bundled `mc ready local`, because the
ubi-micro based `minio/minio` image ships neither `wget` nor `curl`. Both the
Agent and notification WebSocket routes forward Upgrade/subprotocol headers.

### Database roles on reused volumes or external databases

`deploy/postgres/init-runtime-roles.sh` runs only when PostgreSQL initialises
an empty data directory (the first start of a new `postgres-data` volume).
With a reused volume or an external database, migration 018 creates
`ant_control_plane` and `ant_worker` as `NOLOGIN` roles and the services fail
with `role "ant_control_plane" is not permitted to log in`. Enable login after
migrating and before starting the services:

```sql
ALTER ROLE ant_control_plane WITH LOGIN PASSWORD '<CONTROL_PLANE_DB_PASSWORD>';
ALTER ROLE ant_worker WITH LOGIN PASSWORD '<WORKER_DB_PASSWORD>';
```

For the Compose stack, this sequence keeps the passwords out of shell history
by passing them as psql variables from the container environment:

```sh
compose="docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml"
$compose up -d postgres
$compose run --rm --build migrate
$compose exec -T postgres sh -c 'psql -v ON_ERROR_STOP=1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" \
  -v control_password="$CONTROL_PLANE_DB_PASSWORD" -v worker_password="$WORKER_DB_PASSWORD"' <<'SQL'
ALTER ROLE ant_control_plane WITH LOGIN PASSWORD :'control_password';
ALTER ROLE ant_worker WITH LOGIN PASSWORD :'worker_password';
SQL
$compose up -d --build
```

The same statements rotate the passwords. See `docs/postgres-roles.md` for the
required privileges and a verification query.

### Edge proxy and client IPs

The `cloud` network uses a pinned subnet, `CLOUD_NETWORK_SUBNET` (default
`172.28.0.0/16`). The control plane receives `ANT_TRUSTED_PROXY_CIDRS` from
`TRUSTED_PROXY_CIDRS`, which defaults to that subnet, and honours
`X-Forwarded-For`/`X-Real-IP` only from peers inside those CIDRs. Nginx
overwrites `X-Real-IP` with the connecting address and appends it to
`X-Forwarded-For` on every proxied route, including both WebSocket routes.
Change both variables together if the subnet overlaps an existing Docker
network, VPN, or host route.

If a load balancer or another proxy sits in front of Nginx, do not widen
`TRUSTED_PROXY_CIDRS` to the internet. Configure Nginx `set_real_ip_from`
(the balancer's range), `real_ip_header X-Forwarded-For` and
`real_ip_recursive on` so that `$remote_addr` is the original client.

Non-WebSocket routes use HTTP/1.1 with an empty `Connection` header, so the
upstream keepalive pools declared in `deploy/nginx/default.conf` are reused.

### Service credentials

Each service receives only the settings it reads. The control plane gets the
object-store (MinIO root) credentials. The worker gets its `ant_worker`
database URL, Redis, NATS and optional SMTP settings only; it never touches
object storage.

To enable real proxy-health probes, add the reviewed overlay and mount the
three connector binaries:

```sh
docker compose --env-file deploy/.env \
  -f deploy/compose/docker-compose.yml \
  -f deploy/compose/proxy-health.yml up --build
```

The overlay runs `/app/proxy-probe` as a non-root user and reads
`deploy/production/proxy-runtime.yml`. It is also the only place the worker
receives `ANT_ENCRYPTION_KEY_REF`, `ANT_SECRET_MASTER_KEY` and
`ANT_SECRET_KEY_VERSION`, because only the probe decrypts stored proxy
credentials. All three must equal the control plane's values: envelopes are
bound to the key reference and version, so changing any of them makes existing
secrets undecryptable. With the probe enabled, the worker applies the control
plane's master-key rules at startup (base64 that decodes to exactly 32 bytes,
and the development key is refused outside `development`) and exits with an
error instead of running with an invalid key. The selected connector stack remains strict:
`xray` means the Xray + sing-box combination, while `mihomo` means the
independent Mihomo stack. The worker never falls back across those stacks.

## Production Linux shape

Run the same images on Linux behind a managed load balancer or Nginx with TLS.
Use separate PostgreSQL, Redis, NATS, and S3/MinIO production instances where
possible. Keep control-plane instances stateless and scale workers by queue
depth and workspace quota. Do not expose PostgreSQL, Redis, NATS, or MinIO
administrative ports to the public network.

Production requirements:

- Use a secret manager for database credentials, JWT signing keys, and KMS/Vault
  references; never commit `deploy/.env`.
- Use TLS for public HTTP/WebSocket traffic and TLS/authenticated connections
  to managed dependencies.
- Run migrations as a reviewed, serialized release job. Capture migration
  filename/checksum and retain backups before destructive contract steps.
  Provision the runtime role logins as described above.
- Configure PostgreSQL backups/PITR and perform restore drills. Redis is not a
  source of truth and must be rebuildable. Enable versioned, encrypted object
  storage and lifecycle policies for profile artifacts.
- Set resource limits, non-root containers, read-only filesystems where the
  runtime permits, health/readiness probes, structured logs, metrics, traces,
  and alerts for queue lag, failed jobs, DB pool saturation, WebSocket churn,
  and artifact failures.
- Terminate Agent WebSockets only at authenticated endpoints. Forward
  `Upgrade`, `Connection`, client-address and request correlation headers as
  shown in `deploy/nginx/default.conf`, and set `ANT_TRUSTED_PROXY_CIDRS` to
  exactly the proxies that connect to the control plane.

The Dockerfiles build the implemented `server/cmd/control-plane`,
`server/cmd/worker`, `server/cmd/migrate` and `server/cmd/healthcheck` entry
points. The worker image also contains the proxy-probe helper; connector
executables remain an explicit operator-supplied mount so their versions and
checksums can be reviewed.
The frontend image builds `desktop/vue3-ui` with `vue-tsc` before Vite emits
the same static bundle embedded by `desktop/wails-client`; this prevents the
hosted operator console and desktop shell from drifting onto different product
surfaces.

The root `.dockerignore` is an allowlist. The build context contains only what
the Dockerfiles copy: `server/`, `database/migrations/`, the root
`go.mod`/`go.sum`, `backend/`, `desktop/vue3-ui/`, the product logo and
`deploy/nginx/frontend.conf`. The git-tracked connector binaries in `bin/`,
local data, `.env` files and `node_modules` never reach the builder. When a
Dockerfile starts copying a new path, add a matching `!` rule.

## Continuous integration

`.github/workflows/cloud-control-plane-ci.yml` runs on changes to the server,
database, deploy, desktop and frontend trees, the root Go module (`go.mod`,
`go.sum`, root `*.go`, `backend/`, `tools/`; the desktop agent, profile sync
and fingerprint runtime packages live under `desktop/`), `wails.json` and
`.dockerignore`. Jobs:

- `server`: race tests against PostgreSQL, `go vet`, builds of all four
  commands, and `gofmt` gates for the server module and the root module.
- `containers`: `docker compose ... config -q` for the base file and the
  proxy-health overlay using `deploy/.env.example` (which therefore must list
  every variable Compose requires), `nginx -t` for `deploy/nginx/default.conf`
  and inside the built frontend image, and `docker build --target runtime` of
  all three Dockerfiles from the repository root. Nothing is pushed.
- `desktop-and-ui` (Linux) and `windows-desktop`: build both frontends, then
  `go test ./...` on Linux and `go build ./...` on Windows for the root module.
