# Proxy health worker

Health requests atomically create a check and a `proxy.health_check` task.
The task carries IDs and connector selection, never credentials. A worker
configured below resolves encrypted credentials and invokes the existing
browser runtime through stdin. Connector diagnostics are discarded by the
worker and are not copied into API errors.

Migration 014 synchronizes task cancellation, permanent failure and exhausted
retries into the unfinished health check in the same database transaction.
These administrative outcomes do not mark a proxy unhealthy. Once a check has
finished, a late probe response cannot replace its terminal result. The memory
adapter follows the same lifecycle contract. Unit tests cover cancellation,
retry exhaustion (including expired leases), tenant scope and late results;
the SQL contract covers terminal-state propagation and preservation.

Build the runtime command from the repository root:

```sh
go build -o proxy-probe ./backend/cmd/proxy-probe
```

Build the worker from `server/`, then supply these environment variables in
addition to its PostgreSQL, Redis and NATS settings:

| Variable | Value |
| --- | --- |
| `ANT_PROXY_PROBE_EXECUTABLE` | Absolute path to `proxy-probe` |
| `ANT_PROXY_RUNTIME_CONFIG` | Absolute path to existing runtime YAML configuration |
| `ANT_PROXY_RUNTIME_ROOT` | Absolute runtime directory containing installed connector binaries |
| `ANT_PROXY_PROBE_TARGET` | Operator-controlled HTTPS endpoint returning a plain IP or `{"ip":"..."}` |
| `ANT_SECRET_MASTER_KEY` | Same secret-manager injected key as the control plane |
| `ANT_ENCRYPTION_KEY_REF` | Same envelope key label as the control plane |
| `ANT_SECRET_KEY_VERSION` | Same key version as the control plane |

The YAML configuration uses the existing `browser.xray_binary_path`,
`browser.singbox_binary_path`, and `browser.clash_binary_path` settings.
Install the needed binaries before starting the worker. Missing binaries
cause a failed attempt; the runtime does not switch connector stacks.
Advanced proxy nodes store their complete URI/config in the encrypted token
field so TLS and transport parameters survive the cloud-to-runtime boundary.

Without `ANT_PROXY_PROBE_EXECUTABLE`, workers do not claim proxy health tasks.
The Worker image packages the command. Enable the `deploy/compose/proxy-health.yml`
overlay together with `docker-compose.yml`, supplying `PROXY_CONNECTOR_DIR`
as an absolute host directory containing executable, statically linked Linux connector binaries
and `PROXY_PROBE_TARGET` as your HTTPS IP echo endpoint. Connectors are mounted
read-only; ephemeral runtime files use a private tmpfs owned by the non-root
worker. External-proxy acceptance tests remain outstanding. Existing tests cover local IP response handling, route
mismatch, task replay, credential URI encoding and output limits.
