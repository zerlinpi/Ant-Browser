# Desktop cloud command client

The desktop cloud command client is an opt-in control channel for starting,
stopping, and restarting already-authorized local browser instances. It does
not create local bindings, accept profile paths, or apply runtime overrides.
The client connects to the cloud Agent WebSocket using the
`ant-browser-agent.v1` subprotocol.

## Enablement and credentials

The client is disabled unless `ANT_CLOUD_COMMANDS_ENABLED` is exactly
`true` (case-insensitive after trimming). When enabled, configuration is read
from the path in `ANT_CLOUD_COMMANDS_CONFIG`. For compatibility with the
older cloud workflow client, an empty `ANT_CLOUD_COMMANDS_CONFIG` falls back to
`ANT_CLOUD_WORKFLOWS_CONFIG`.

The device credential must be supplied separately through
`ANT_CLOUD_DEVICE_CREDENTIAL`; it is never read from or written to the JSON
configuration. Keep the credential in the deployment secret store and do not
put it in shell history, source control, or the config file.

The configuration path must be absolute. The file is limited to 64 KiB, uses a
strict JSON schema, and must contain a UUID `deviceId`, a UUID `workspaceId`,
an HTTPS `baseUrl`, and at least one explicit cloud-instance-to-local-profile
binding. Local profile IDs must be distinct. Profile synchronization adds an
explicit `cloudProfiles` mapping; its keys must already exist in `bindings`
and both the instance and cloud profile IDs are canonical UUIDs. Example:

```json
{
  "baseUrl": "https://cloud.example.test",
  "deviceId": "11111111-1111-4111-8111-111111111111",
  "workspaceId": "22222222-2222-4222-8222-222222222222",
  "bindings": {
    "33333333-3333-4333-8333-333333333333": "local-profile-id"
  },
  "cloudProfiles": {
    "33333333-3333-4333-8333-333333333333": "44444444-4444-4444-8444-444444444444"
  }
}
```

When `cloudProfiles` is present, provide these secrets through the process
secret store:

- `ANT_CLOUD_PROFILE_ENCRYPTION_KEY`: base64 encoding of exactly 32 random bytes.
- `ANT_CLOUD_PROFILE_ENCRYPTION_KEY_REF`: the non-secret key-version label also configured as the control plane's `ANT_ENCRYPTION_KEY_REF`.

All devices that migrate the same profile need the same key version. The key
is used locally for a chunked AES-256-GCM container and is never written to the
binding file, command journal, control-plane database, or object-store request.

The endpoint, device identity, and workspace identity are checked during the
WebSocket handshake. Ordinary transport loss is retried inside the client
(1 s doubling to 30 s). The client returns only on terminal conditions, which
the desktop host handles as follows:

- `ErrDeviceRejected` (handshake HTTP 401/403, for example after revocation or
  credential rotation): the command channel stops. Provision a new credential
  (`POST /api/v1/devices/{deviceID}/rotate-credential`) and restart the app.
- `ErrProtocolMismatch` (HTTP 426, another subprotocol, or a different
  announced device/workspace): the host retries from 30 s doubling to 15 min,
  so a rolling control-plane upgrade recovers without a restart.
- `ErrJournalUnavailable`: the host closes and reopens the journal on the
  same backoff.

A session that stayed healthy for 10 minutes resets the host backoff.

## Supported actions

The command protocol accepts these actions only:

- `instance.start` — restore the cloud profile's current revision when
  configured, then start the local profile and report `running`. The restore
  is skipped when the local browser is already running (the start reuses it)
  or when the local directory already derives from that revision. A failed
  restore fails the command with state `offline`; the browser is not started
  on possibly stale data.
- `instance.stop` — stop the local browser, wait up to 15 s for its processes
  to release the profile directory, upload changes when configured, and
  report `offline`.
- `instance.restart` — stop, upload, and then restart the locally bound profile.
- `instance.migrate` — stop and upload on the source device. Only after that
  command completes does the control plane reassign the instance and enqueue
  an idempotent `instance.start` for the destination, which downloads and
  verifies the current encrypted revision before starting.

Instances listed in `bindings` but not in `cloudProfiles` start, stop and
restart without profile synchronization.

Commands must target the configured workspace/device, include a UUID command
and instance ID, an `expectedVersion` of at least 1, and a future deadline.
Start, stop, and restart payloads must be empty (or `{}`). Migration accepts
exactly one `targetDeviceId`; cloud commands cannot supply launch arguments,
proxy overrides, local paths, encryption keys, or shell input. An instance
must have an authorized, non-deleted local binding. Migration additionally
requires a cloud profile binding and valid encryption material; without a
binding it is rejected before the browser is touched. If upload fails after
the source was stopped, the agent best-effort restarts the source and reports
the command failed instead of transferring ownership.

## Deadlines and failure reports

The control plane gives every command on an instance with a cloud profile, and
every migration, a 30-minute deadline; other commands get 2 minutes. The agent
honors the deadline but never runs one command for more than 35 minutes. A
command whose deadline passed before execution fails with `command_expired`.

Failure codes are stable: `invalid_command`, `unsupported_command` and
`command_conflict` (a journal record with the same ID but different contents)
are reported without executing anything; `local_execution_failed`,
`command_expired` and `execution_uncertain` follow an execution attempt.
`failureMessage` is a diagnostic only: a single line of at most 300
characters with local filesystem paths and URL query strings redacted. It is
not persisted in the journal, so a replayed report carries the code alone.

## Profile synchronization

Uploads are incremental: changed files and deletions relative to the revision
the device last synchronized, recorded in
`.ant-profile-sync-<cloudProfileId>.json` beside (never inside) the profile
directory and bound to that directory. A full snapshot is uploaded when there
is no usable baseline or after 32 consecutive deltas. Chromium's
machine-local files are never synchronized: `lockfile`, `Singleton*`,
`DevToolsActivePort`, `BrowserMetrics*`, `Crashpad`, `component_crx_cache`,
`extensions_crx_cache`, `ShaderCache`, `GrShaderCache`, `GraphiteDawnCache`,
and the `Cache`, `Code Cache`, `GPUCache` and `Dawn*Cache` directories of each
profile, plus `*.tmp` files near the root.

Every operation holds the profile's sync lease, renews it in the background
and confirms it once more before committing, restoring or replacing the local
directory, so the revision read under the lease is still current when it is
applied.

The upload base is the revision this device last synchronized, never the
cloud's current revision. If another device committed since (or this device
never synchronized and the cloud already has a revision), the server opens a
conflict instead of accepting an overwrite. The device still uploads its
snapshot, releases the lease and fails the command with the conflict ID.
While the conflict is open every pull and push fails with
`profile_conflict_unresolved`, so starts of that instance fail until it is
resolved in the console:

- `keep_local` promotes the uploaded snapshot; the device adopts it as its
  baseline on the next sync instead of conflicting again.
- `keep_remote` keeps the cloud revision; the next start replaces the local
  directory with it. Local changes made after the conflict are not uploaded
  silently: the next stop opens a new conflict.

## Durable journal and delivery semantics

Each command is journaled below the application data directory at
`data/cloud-commands/<workspaceId>/<deviceId>`. The directory is private to
the desktop process, and journal files are created with restrictive
permissions. Credentials are not stored there.

Before any browser side effect, the client durably writes a `started` intent
and syncs it to disk. It then atomically replaces that record with a
`completed` or `failed` result containing the observed state or failure code.
This gives at-least-once network delivery with local at-most-once side-effect
execution across reconnects and application restarts:

- A duplicate command with the same ID and canonical contents reuses its
  existing receipt and is not executed again.
- A command ID reused with different contents, a corrupt record, or an unsafe
  journal path is rejected rather than silently replaced.
- A `started` record found after a crash is marked
  `execution_uncertain`; the operation is never repeated automatically (in
  particular, a restart is never replayed blindly).
- A server command already marked `accepted` or `running` without a local
  receipt is also treated as `execution_uncertain`; an upgraded/reinstalled
  client cannot silently repeat work begun by an older process.
- A disconnected WebSocket does not mean the local operation failed. The
  journal result remains authoritative while the client reconnects and reports
  the result.

On POSIX systems the client restricts the directory to the current user and
syncs the directory entry around journal creation/replacement. On Windows the
directory inherits the application-data ACL and individual files are flushed
before an atomic same-directory replacement. Operators should preserve this
directory while diagnosing delivery issues.
Deleting it discards the client's deduplication history and can make a future
command execution uncertain.

## CI coverage

The Cloud control plane workflow's `desktop-and-ui` job runs `go test ./...`.
Its path filters include both `backend/**` and `desktop/**`, so changes to the
cloud command startup path, executor, WebSocket client, or durable journal
exercise the same check in pull requests and pushes to `main`.
