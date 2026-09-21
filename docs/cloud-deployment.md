# Cloud deployment

The cloud target is a monolith-first Go control-plane plus a separately
scalable worker. Domain packages share one `server` Go module; they are not
deployed as a collection of premature microservices. The existing Wails
desktop application remains the local Agent/Runtime and is not built into the
cloud images.

## Local acceptance

1. Copy `deploy/.env.example` to `deploy/.env` and replace every placeholder.
   Keep the username/password embedded in `NATS_URL` synchronized with
   `NATS_USER`/`NATS_PASSWORD`; the same applies to `REDIS_URL` and
   `REDIS_PASSWORD`.
2. From the repository root, run:

   ```sh
   docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up --build
   ```

3. Verify `GET http://localhost:8088/health` and `GET http://localhost:8088/ready`.
4. Stop with `docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml down`.

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

To enable real proxy-health probes, add the reviewed overlay and mount the
three connector binaries:

```sh
docker compose --env-file deploy/.env \
  -f deploy/compose/docker-compose.yml \
  -f deploy/compose/proxy-health.yml up --build
```

The overlay runs `/app/proxy-probe` as a non-root user and reads
`deploy/production/proxy-runtime.yml`. The selected connector stack remains
strict: `xray` means the Xray + sing-box combination, while `mihomo` means the
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
- Configure PostgreSQL backups/PITR and perform restore drills. Redis is not a
  source of truth and must be rebuildable. Enable versioned, encrypted object
  storage and lifecycle policies for profile artifacts.
- Set resource limits, non-root containers, read-only filesystems where the
  runtime permits, health/readiness probes, structured logs, metrics, traces,
  and alerts for queue lag, failed jobs, DB pool saturation, WebSocket churn,
  and artifact failures.
- Terminate Agent WebSockets only at authenticated endpoints. Forward
  `Upgrade`, `Connection`, and request correlation headers as shown in
  `deploy/nginx/default.conf`.

The Dockerfiles build the implemented `server/cmd/control-plane`,
`server/cmd/worker`, and `server/cmd/migrate` entry points. The worker image
also contains the proxy-probe helper; connector executables remain an explicit
operator-supplied mount so their versions and checksums can be reviewed.
