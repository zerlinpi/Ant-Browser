# JSON workflow runtime integration

The existing local automation manager now exposes `RunWorkflowTask` alongside
`RunScriptTask`. It uses the same launch API, instance selector, installed runtime,
task-key exclusion, process cancellation and artifact directory handling. Existing
script requests still load their original script module unchanged.

`WorkflowTaskRequest.Definition` carries an `ant-workflow/v1` JSON definition.
The embedded Node runner dispatches `taskType: workflow` to a sequential interpreter.
The current adapter executes **Playwright** and **raw CDP** definitions. The CDP
path sends Chrome DevTools Protocol commands for every workflow action; it does
not relabel Playwright locator calls. Puppeteer definitions remain explicitly
unavailable until the separately versioned `puppeteer-core` runtime is installed.

Implemented actions: navigate, click, input, wait, upload, JavaScript, screenshot,
extract and close. `close` is required to be last and sends `Browser.close` to the
attached instance, not merely a page close. JavaScript `script` is a function body
executed in the browser page with an `args` parameter; it is never evaluated in Node.
Before the first browser operation, the Playwright context installs a fail-closed
request guard. Literal loopback, private/link-local IP ranges, localhost, single-label
hosts, and common local-only DNS suffixes are blocked both for navigation steps and
for page-initiated requests such as `fetch`. This does not replace DNS-rebinding and
egress controls at the host/network boundary.
The adapter uses the official [Page](https://playwright.dev/docs/api/class-page),
[Locator](https://playwright.dev/docs/api/class-locator) and
[Browser](https://playwright.dev/docs/api/class-browser) APIs.

Extracted values remain internal to one execution and may be consumed using
`variable://name`. They are not automatically included in task results. Step
results expose IDs, statuses and stable error codes, never raw browser exceptions.
An ordinary failure may continue when `continueOnError` is set; timeout and
cancellation always stop further steps. A timeout does not undo browser-side
effects already started, especially JavaScript or a submitted form. Do not
automatically retry non-idempotent workflows on that basis.

## Remaining integration work

The cloud `POST /api/v1/workspaces/{workspaceId}/tasks` endpoint now accepts
workflow executions with `taskType: "workflow.execute"`, `workflowId`,
`workflowVersionId`, and `payload: {"instanceId": "<uuid>"}`. Supply an
`Idempotency-Key` header. Task, instance-operation and workflow-read permissions
are required. Only the workflow's currently published version may be accepted;
the target must be a non-deleted instance in the same workspace. Migration 015
enforces these relationships atomically with row locks during insertion.

Accepted tasks retain their version ID even after publication changes. Reusing
their original key and request returns the original task, including after archive;
reusing the key with a different workflow, version or target returns a conflict.
New requests after archive are rejected. Workflow retries default to zero; set a
positive retry limit only for workflows whose effects tolerate repetition.
These checks establish queue acceptance, not end-to-end execution readiness.

Task start and result transitions require the current workspace, worker, run and
attempt identity and an unexpired lease. PostgreSQL locks the task before reading
the current attempt, so a result from an older execution cannot overwrite a retry
even when both attempts use the same worker name. The opt-in PostgreSQL integration
test exercises the repository transitions; memory tests also cover forged scope,
attempt number and expiration at the exact deadline.

- `POST /api/v1/agent/tasks/claim` accepts the existing `X-Device-ID` and
  `Authorization: Device <credential>` headers. It returns 204 when no work is
  available, or a lease plus the pinned workflow version. Device workspace and
  user identity come only from authenticated credentials. Current membership
  must permit task and instance operation and workflow reading. Only non-deleted
  instances assigned to that device are eligible; currently only Playwright
  versions are claimable. Ordinary cloud workers cannot claim these executions.
  The lease lasts 30 minutes. The desktop lifecycle has an opt-in integration;
  the binding UI and production end-to-end validation remain outstanding.
- `POST /api/v1/agent/tasks/{taskId}/report` uses the same device authentication
  and accepts `runId`, `attemptId`, and `status` (`running`, `succeeded`, `failed`).
  It returns 204 after persistence. The server loads the lease itself; clients
  cannot supply worker ownership, workspace, retry policy or a replacement task.
  Terminal reports require a running task. Current membership, lease identity,
  expiry, device revocation and instance assignment are rechecked. Reports after
  instance reassignment are rejected. Failed reports use a server-owned diagnostic
  and do not automatically retry. Arbitrary browser errors are not accepted.
  Repeating the same `running` or terminal report is idempotent when its current
  run and attempt IDs match. A different terminal state or an older attempt is
  rejected. This covers a committed response being lost in transit.
- The interpreter accepts injected `resolveValue` and `resolveArtifact` functions;
  the production runner does not yet supply them. Account-secret and upload
  references therefore fail explicitly. Do not inject unscoped local file paths
  or secrets into workflow definitions to work around this.
- Dynamic URL templates are not implemented and are rejected at execution.
- Puppeteer needs its own runtime adapter, and schedules need dispatch wiring.
- Tests currently use a mocked browser API, not a live Chromium session. Real-browser
  acceptance and full cloud-to-desktop task lifecycle validation remain outstanding.

## Local verification

`desktop/browser-agent` provides an HTTPS-only client with `PollOnce` and serial
`Run` polling. It verifies workspace/device identity, pinned workflow/version IDs,
definition hash, supported engine and lease expiry before reporting `running` and
invoking an injected executor. Redirects are not followed; raw browser errors
never enter reports. Execution is bounded by the lease minus a reporting margin.
Reports retry network errors, HTTP 429 and server errors with bounded backoff; the
running acknowledgment and final report each have a ten-second budget. A retry
only resends run/attempt/status and never re-executes browser steps. Polling stops
after a report/protocol error remains unresolved.

`backend/internal/cloudagent.Executor` adapts the client to the existing automation
manager. Its trusted local binding resolver maps cloud instance IDs to local Profile
IDs. Cloud data cannot select arbitrary local paths, launch URLs or credentials.
The adapter uses a temporary per-run artifact directory and removes it after the
execution returns (including failure and cancellation); retention, upload and encrypted
outbox delivery still need implementation. Cloud polling also requires the local
Launch API's key authentication to be active; the key stays in the Node-side launch
client and is never exposed through the page workflow API. Startup enables polling only when
`ANT_CLOUD_WORKFLOWS_ENABLED=true`, the installed automation runtime is ready, and
`ANT_CLOUD_WORKFLOWS_CONFIG` points to an absolute JSON configuration path. Supply
the device credential through `ANT_CLOUD_DEVICE_CREDENTIAL`; it is not accepted
in JSON and is removed from automation script subprocess environments.

The JSON fields are `baseUrl` (HTTPS), `deviceId`, `workspaceId`, and `bindings`
(cloud instance UUID to distinct local Profile ID). Missing bindings never fall
back to arbitrary profiles. Configuration is read once at startup; restart after
changing it. Invalid configuration leaves local browser functionality available
and logs a non-sensitive startup diagnostic. Application exit cancels polling,
including app-only exit, and waits up to 12 seconds for cancellation/reporting.
Transient claim transport errors, HTTP 429, and server errors use bounded exponential
backoff. A fatal protocol/report error clears the runner generation so a later
initialization can start a fresh client rather than remaining latched until process exit.
No UI for credential enrollment, binding management or restart is available yet.
Tests use a TLS mock server and mock
executor; they do not prove real Chromium or production cloud execution.

```sh
node --test backend/internal/automation/assets/runner_workflow.test.cjs
go test ./backend/internal/automation
go test ./desktop/browser-agent ./backend/internal/cloudagent
```
