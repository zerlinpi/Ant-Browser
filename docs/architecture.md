# Ant-Browser architecture

Ant-Browser is split into three ownership domains. The cloud control plane owns tenant data and desired state; the desktop agent owns host credentials and command execution; the browser runtime owns Chromium processes, profile files, proxy connectors, fingerprint injection, and automation sessions. Cloud code never receives a local PID, CDP port, profile key, or plaintext proxy/account secret unless an explicitly scoped protocol requires it.

## Runtime topology

```text
Browser / Vue 3 desktop UI
        |
        | HTTPS Bearer API + notification WebSocket
        v
Go control plane ---- PostgreSQL
  |   |   |             Redis / NATS
  |   |   +------------- object storage
  |   |
  |   +-- durable desired state, RBAC, audit, billing and scheduling
  |
  | device-authenticated WebSocket + scoped HTTPS
  v
Wails desktop / browser agent ---- encrypted local operation journal
        |
        +-- profile sync engine ---- encrypted manifests and chunks
        +-- selected proxy connector stack
        +-- Playwright / Puppeteer / CDP adapters
        v
Browser Runtime ---- isolated user-data-dir ---- Chromium
        |
        +-- validated launch arguments
        +-- content-addressed MV3 fingerprint runtime
```

The production control-plane entry points are `server/cmd/control-plane` and `server/cmd/worker`. `server/services` contains the service boundaries named in the product scope; `server/platform/postgres` is the durable repository implementation and `server/platform/memory` is the deterministic test/development implementation. Redis supplies realtime presence/cache primitives, NATS supplies task wake-up and worker delivery, and object storage holds profile objects rather than database rows containing large browser payloads.

The commercial desktop shell is `desktop/wails-client`, whose frontend build is `desktop/vue3-ui`. `desktop/browser-agent` implements cloud command execution and its durable command journal. Existing root Wails/Chromium behavior remains in place as the compatibility runtime and is called by the agent instead of being replaced.

## Control and data flows

### Browser command flow

1. A workspace member creates an idempotent instance command through the gateway.
2. The control plane persists the command before dispatch and routes it only to the instance's assigned, non-revoked device.
3. The desktop journal records receipt and prevents duplicate side effects across reconnects or restarts.
4. Before start, restart, or migration rollback, the agent fetches `/api/v1/agent/instances/{instanceID}/runtime-config` with device credentials.
5. The desktop validates instance identity, workspace scope, platform, Chromium major version, every allowlisted runtime argument, and the typed fingerprint configuration. Redirects are rejected so credentials cannot be forwarded to another origin.
6. Browser Runtime resolves the selected Chromium core and proxy stack, materializes any content-addressed fingerprint extension, and launches the isolated user-data directory.
7. Accepted/running/completed/failed and observed-state events advance the durable cloud command state machine.

A repeated start reuses an existing Chromium process only when its recorded launch arguments and fingerprint extension exactly match the current cloud template. An adopted process without trustworthy launch provenance is rejected for cloud fingerprint starts; local-only startup behavior is unchanged.

### Profile synchronization

Profile revisions are immutable manifests. A snapshot revision holds one client-side encrypted archive of the Chromium user-data directory; an incremental revision holds an encrypted archive of the changed files plus the deleted paths, applied on top of its base revision (the desktop compacts to a new snapshot after 32 deltas). Machine-local Chromium files such as locks, caches, crash and metrics data and `DevToolsActivePort` are never synchronized. Every transfer holds the profile's sync lease; object bytes go directly to object storage through short-lived presigned URLs, and the server verifies the objects before a commit. The desktop confirms the lease once more before committing, restoring or replacing the local directory, so the revision it applies is still current.

A revision whose base is not the current revision opens a conflict instead of overwriting it. While a conflict is open the server refuses new leases and revisions for that profile, so no device can pull, push or restore until it is resolved: `keep_local` promotes the snapshot the conflicting device uploaded (after verifying its objects), `keep_remote` supersedes it. Restores and resolutions are audited.

Everything Chromium persists in the profile directory (cookies, local and session storage, IndexedDB, extensions, bookmarks, history, preferences) travels as files inside these archives. The encryption key remains on the authorized desktop side; cloud object storage contains ciphertext and integrity metadata.

### Fingerprint isolation

The cloud template is the authority for seed, language, timezone, platform, Chromium major version, WebRTC policy, screen/window values and the advanced JS-visible surfaces. Kernel-supported dimensions are emitted as validated Chromium flags. Device memory, touch points, DNT, screen/DPR, AudioContext noise, fonts, WebGL identity, media devices, and battery are injected at `document_start` by an immutable MV3 extension in the page's main world. Templates support seeded/fixed/custom modes, atomic batch generation, and reusable marketplace-oriented presets.

The extension complements the selected fingerprint-capable Chromium build; it does not claim to modify worker-only or network-layer surfaces that Chromium itself does not expose to a page content script.

### Proxy ownership

All launch, download, health, warm-up, speed-test and real-connectivity operations obey [`proxy-connector-stacks.md`](proxy-connector-stacks.md). `xray` means the Xray + sing-box combination stack, with protocol ownership split exactly as documented. `mihomo` is an independent stack. No operation may silently fall back between them, and sing-box-owned protocols are not classified as unsupported merely because Xray does not implement them.

## Security boundaries

- User routes use short-lived access JWTs backed by revocable sessions and rotating refresh tokens. Device routes use separately issued and revocable device credentials.
- Workspace authorization is checked in the service layer before repositories are called. PostgreSQL requests carry tenant context in addition to application checks.
- Account and proxy secrets use envelope encryption; API responses return redacted metadata rather than ciphertext or plaintext.
- JSON decoding rejects unknown fields, trailing values, oversized bodies and malformed identifiers. Errors use a stable structured envelope with a request/trace ID.
- Browser-facing CORS and WebSocket origins are exact allowlists. Authentication headers are never sent across redirects.
- Commands, tasks, profile revisions and billing mutations use version or idempotency contracts appropriate to their side effects.

## Deployment

`deploy/compose/docker-compose.yml` defines the production-shaped control plane, worker, PostgreSQL, Redis, NATS, MinIO and Nginx topology. Nginx terminates TLS and forwards WebSocket upgrades. Secrets and public origins are supplied through deployment environment variables; `.env.example` contains names, not production values. Database migrations are ordered under `database/migrations` and mirrored by the server migration runner.

See [`api.md`](api.md), [`database.md`](database.md), [`cloud-deployment.md`](cloud-deployment.md), and [`security.md`](security.md) for the executable contracts and operational detail.
