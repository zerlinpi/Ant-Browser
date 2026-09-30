# API 参考（当前已注册路由）

本文只记录当前 `server/services/gateway-service` 实际注册的云端路由，以及桌面 Launch API 的实际路由族。接口仍可能随代码演进；请求字段以对应 service 的 Go 类型和 handler 为准。

## 云端 HTTP API

默认前缀为 `/api/v1`。`GET /healthz`、`GET /readyz` 不需要认证；认证注册、登录、刷新和登录第二步 `/api/v1/auth/mfa/verify` 也不需要 Bearer。其余 `/api/v1/` HTTP 路由由 gateway 的 Bearer 中间件保护。

### 健康与认证

| 方法 | 路径 | 认证 | 成功响应 |
|---|---|---|---|
| GET | `/healthz` | 无 | 200，`status=ok`、服务名和 UTC 时间 |
| GET | `/readyz` | 无 | 200 `status=ready`；依赖不可用为 503 |
| POST | `/api/v1/auth/register` | 无 | 201，`{data: TokenPair}`；限流 |
| POST | `/api/v1/auth/login` | 无 | 200，`{data: LoginResult}`：未开启两步验证时即 TokenPair，开启时只有 `mfaRequired` 和 `mfaChallenge`；限流 |
| POST | `/api/v1/auth/mfa/verify` | 挑战令牌 | 200，`{data: TokenPair}`；限流 |
| POST | `/api/v1/auth/refresh` | 无 | 200，`{data: TokenPair}`；refresh token 单次轮换；限流 |
| POST | `/api/v1/auth/logout` | Bearer | 204 |
| GET | `/api/v1/me` | Bearer | 200，`{data: User}` |
| GET | `/api/v1/me/sessions` | Bearer | 200，`{data: SessionSummary[]}` |
| DELETE | `/api/v1/me/sessions/{sessionID}` | Bearer | 204 |
| POST | `/api/v1/me/sessions/revoke-others` | Bearer | 200，`{data: {revoked: n}}` |
| GET | `/api/v1/me/mfa` | Bearer | 200，`{data: {available, enabled, enabledAt?, recoveryCodesRemaining}}` |
| POST | `/api/v1/me/mfa/totp/setup` | Bearer | 200，`{data: TOTPSetup}`；body `{password}`；限流 |
| POST | `/api/v1/me/mfa/totp/confirm` | Bearer | 200，`{data: {recoveryCodes: string[]}}`；body `{code}`；限流 |
| POST | `/api/v1/me/mfa/recovery-codes` | Bearer | 200，`{data: {recoveryCodes: string[]}}`；body `{code}` 或 `{recoveryCode}`；限流 |
| DELETE | `/api/v1/me/mfa` | Bearer | 204；body `{password, code}` 或 `{password, recoveryCode}`；限流 |

Bearer 格式必须是 `Authorization: Bearer <accessToken>`。服务端同时校验 JWT 和数据库 session 是否仍有效。默认 access TTL 为 15 分钟，refresh TTL 为 30 天，可由配置覆盖。

登录会话：每次注册、登录都会新建 session。`GET /me/sessions` 只返回调用者自己未撤销、未过期的会话（最多 200 个，当前会话排在最前并带 `current: true`），字段为 `id`、`deviceId`、`userAgent`、`ipAddress`、`createdAt`、`lastSeenAt`（最近一次刷新令牌的时间）、`expiresAt`。`DELETE /me/sessions/{sessionID}` 撤销其中一个；撤销当前会话等同于退出登录；不存在、属于其他用户、已撤销或已过期的会话一律返回 404 `not_found`，不能用来探测他人会话。`POST /me/sessions/revoke-others` 撤销除当前会话外的全部会话。被撤销会话的访问令牌立即失效、刷新令牌同时作废；它打开的通知 WebSocket 在本节点立即关闭，在其他节点最长约一分钟内关闭。

#### 两步验证（TOTP）

算法为 RFC 6238：HMAC-SHA1、30 秒时间步、6 位数字，接受前后各一个时间步的时钟偏差；密钥为 20 字节随机值，以无填充 base32 返回。开启分两步：

1. `POST /me/mfa/totp/setup {password}` 重新校验密码后生成密钥，返回 `secret`、`otpauthUri`（`otpauth://totp/<issuer>:<email>?secret=…&issuer=…&algorithm=SHA1&digits=6&period=30`）、`issuer`、`accountName`、`algorithm`、`digits`、`period`。密钥只在这一次响应中出现；再次调用会替换尚未确认的绑定。未确认的绑定不影响登录。
2. `POST /me/mfa/totp/confirm {code}` 用身份验证器当前的验证码激活，返回 10 个恢复码（格式 `xxxx-xxxx-xxxx-xxxx`，80 bit，只返回这一次）。已登录的其他会话不受影响。

登录：对已开启两步验证的账号，`POST /auth/login` 在密码正确后不签发令牌，而是返回 `{mfaRequired: true, mfaChallenge: {token, expiresAt, methods: ["totp", "recovery_code"]}}`；未开启时响应与之前完全相同。客户端随后调用 `POST /auth/mfa/verify {challengeToken, code}` 或 `{challengeToken, recoveryCode}`（必须二选一）换取 TokenPair；会话沿用登录请求中的 `deviceId`，User-Agent 和 IP 取自 verify 请求。挑战令牌有效 5 分钟，最多尝试 5 次，成功后立即作废。登录成功的安全通知在 verify 完成后发出，payload 带 `secondFactor`（`totp` 或 `recovery_code`）。

- 每个时间步的验证码只能用一次（包括激活时用过的那个），重放返回 422 `mfa_invalid_code`。
- 恢复码大小写与分隔符不敏感，每个只能使用一次。`POST /me/mfa/recovery-codes` 验证验证码或恢复码后生成新的一组，旧恢复码全部失效；用于授权的恢复码也会被消耗。
- `DELETE /me/mfa` 需要密码加验证码或恢复码，丢失手机时可以用恢复码关闭后重新绑定。关闭会删除因子、恢复码和尚未完成的登录挑战。
- 锁定：同一用户连续 5 次校验失败（登录第二步、激活、重新生成和关闭共用计数）后锁定 15 分钟，期间返回 429 `mfa_locked`，带 `Retry-After` 和 `details.retryAfterSeconds`；任一次成功清零计数。该计数持久化在数据库中，对所有副本生效。
- 开启、关闭两步验证和重新生成恢复码都会向该用户所在的每个工作空间发送 `security.event` 通知。
- TOTP 密钥用与账号、代理凭据相同的 envelope 密钥加密（见 [`security.md`](security.md)），并绑定用户 ID。服务端未配置加密时 `available=false`，开启和验证码校验返回 503 `mfa_unavailable`，恢复码仍可使用。更换 `ANT_SECRET_MASTER_KEY`、`ANT_ENCRYPTION_KEY_REF` 或 `ANT_SECRET_KEY_VERSION` 会让已保存的密钥无法解密，用户只能用恢复码登录后重新绑定。

已登录接口上的两步验证失败一律返回 422 而不是 401（`mfa_invalid_code`、`mfa_code_required`、`invalid_password`），避免客户端把它当作访问令牌过期去刷新；只有 `/auth/mfa/verify` 在挑战无效、过期、已用或尝试超限时返回 401 `mfa_challenge_invalid`。状态冲突为 409：`mfa_already_enabled`（已开启时再次 setup/confirm）、`mfa_not_enabled`（未开启时重新生成或关闭）、`mfa_setup_required`（确认前没有 setup，或绑定已被新的 setup 替换）。

#### 限流（429 `rate_limited`）

以下接口使用进程内令牌桶限流，键为解析后的客户端 IP（见 [`security.md`](security.md) 的可信代理说明）；登录额外按规范化（去空白、小写）邮箱限流，用于减缓跨 IP 的撞库/密码猜测：

| 接口 | 键 | 默认速率 | 突发 |
|---|---|---|---|
| POST `/api/v1/auth/login` | 客户端 IP | 10 次/分钟 | 5 |
| POST `/api/v1/auth/login` | 邮箱 | 5 次/分钟 | 5 |
| POST `/api/v1/auth/register` | 客户端 IP | 5 次/10 分钟 | 5 |
| POST `/api/v1/auth/refresh` | 客户端 IP | 60 次/分钟 | 30 |
| POST `/api/v1/auth/mfa/verify` | 客户端 IP | 10 次/分钟 | 5 |
| 两步验证管理：POST `/me/mfa/totp/setup`、`/me/mfa/totp/confirm`、`/me/mfa/recovery-codes`，DELETE `/me/mfa` | 用户 | 10 次/分钟（四个接口共享） | 5 |
| POST `/api/v1/workspaces/{workspaceID}/invitations/accept` | 客户端 IP | 10 次/分钟 | 10 |

超限返回 429，`error.code = "rate_limited"`，`error.message = "Too many requests; retry later"`，并带 `Retry-After` 响应头（整数秒，至少 1）；CORS 已暴露该响应头。所有请求（包括成功请求）都消耗令牌。计数只在单个进程内有效，多副本部署时实际上限约为副本数倍，精确限流需要共享限流器（如 Redis），当前未实现。

### 工作空间、成员、设备和实例

下列路由均需 Bearer；服务层按 workspace 成员角色/权限检查，且请求在进入 repository 前设置租户上下文。

| 方法 | 路径 |
|---|---|
| GET, POST | `/api/v1/workspaces` |
| GET, PATCH | `/api/v1/workspaces/{workspaceID}` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/members` |
| PATCH, DELETE | `/api/v1/workspaces/{workspaceID}/members/{userID}` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/invitations` |
| DELETE | `/api/v1/workspaces/{workspaceID}/invitations/{invitationID}` |
| POST | `/api/v1/workspaces/{workspaceID}/invitations/accept` |
| GET, POST | `/api/v1/devices` |
| DELETE | `/api/v1/devices/{deviceID}` |
| POST | `/api/v1/devices/{deviceID}/rotate-credential` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/browser-instances` |
| GET, PATCH, DELETE | `/api/v1/workspaces/{workspaceID}/browser-instances/{instanceID}` |
| POST | `/api/v1/workspaces/{workspaceID}/browser-instances/{instanceID}/clone` |
| POST | `/api/v1/workspaces/{workspaceID}/browser-instances/{instanceID}/commands` |

实例命令为异步接受（202），必须带 `Idempotency-Key`（非空，最长 200 字符）；相同工作空间和键仅在实例、动作、预期版本及 JSON payload 一致时复用已有命令，否则返回 409 `idempotency_conflict`。数据库对 `(workspace_id, idempotency_key)` 唯一约束。实例必须绑定同工作区、未撤销的设备；start/stop/restart 不接受 payload，migrate 只接受 `targetDeviceId`。命令优先投递在线 Agent，否则通过 realtime 总线按设备投递；命令状态由 Agent 事件推进。设备重新连接时，过期的未完成命令先原子转为 `expired`，每次最多下发 100 条有效命令。

工作空间对象包含 `role`（可省略）：调用者自己在该工作空间的角色。`GET /api/v1/workspaces`（列表）和 `GET /api/v1/workspaces/{workspaceID}` 总是返回；创建（固定为 `owner`）与 PATCH 更新响应也返回调用者角色。该字段是按调用者计算的投影，不会持久化。成员对象（`GET .../members` 列表和成员 PATCH 响应）额外包含 `email`、`displayName`（可省略）；读取成员列表需要 `member.read`。

成员管理：

- `PATCH /api/v1/workspaces/{workspaceID}/members/{userID}`，body `{ "role": "admin" }`，200 返回 `{data: Membership}`。只接受 `admin`、`manager`、`operator`、`viewer`；`owner` 或其他值返回 422 `invalid_role`（Owner 不能通过此接口授予）。
- `DELETE /api/v1/workspaces/{workspaceID}/members/{userID}`，204 无 body。同一事务内撤销该成员在此工作空间的全部设备及其设备凭据，并关闭这些设备的在线 Agent WebSocket（本节点立即关闭；其他节点在下一次 presence 刷新时关闭，最长约 25 秒）。成员行保留为 `removed` 状态，之后可重新通过 POST members 或邀请加入，但已撤销的设备不会恢复。
- 两者都需要 `member.manage`（Owner、Admin），否则 403 `forbidden`；目标当前角色为 Owner 时，调用者也必须是 Owner（否则 403）。降级或移除最后一个 Owner 返回 409 `last_owner`。目标不是活跃成员（含格式非法的 userID）返回 404 `not_found`。

邀请创建 body 为 `{ "email": "member@example.com", "role": "operator" }`，需要 `member.invite` 权限，不允许邀请为 Owner；默认有效期 7 天。201 响应的 `data.token` 仅返回一次，管理员应通过可信渠道交给受邀者。接受接口 body 为 `{ "token": "..." }`，需要受邀用户的 Bearer，但无需预先加入工作区；服务端用数据库用户邮箱核对邀请。过期、撤销、重复消费或身份不匹配返回 422 `invitation_invalid`，席位不足返回 402 `quota_exceeded` 且不消费邀请。当前不自动发送邀请邮件。

设备（执行 Agent）：

- `POST /api/v1/devices` 注册设备需要目标工作空间的 `instance.operate` 权限（Operator 及以上）；Viewer 返回 403 `forbidden`。201 响应 `{data: {device, credential}}` 中的 `credential` 仅返回一次，服务端只保存其哈希。
- `POST /api/v1/devices/{deviceID}/rotate-credential`（无 body）为设备签发新凭据，200 返回 `{data: {device, credential}}`，响应带 `Cache-Control: no-store`。与撤销相同，只有注册该设备的用户可以操作（否则 404 `not_found`）；已撤销的设备返回 403 `resource_disabled`。同一事务内撤销该设备所有有效凭据并写入新凭据，旧凭据立即失效；用旧凭据建立的在线 WebSocket 会被关闭（跨节点最长约 25 秒），Agent 需用新凭据重连。
- `DELETE /api/v1/devices/{deviceID}` 撤销设备后同样会关闭其在线 WebSocket。

实例 PATCH/DELETE 要求 JSON body 携带正整数 `expectedVersion`。PATCH 支持名称、标签和运行配置引用；运行中的实例禁止更换运行配置。clone 当前仅复制配置，不复制 Profile 数据或代理分配，避免共享可变环境。`instance.migrate` 的 payload 必须只包含同工作区、未撤销的 `targetDeviceId`，且实例必须绑定云端 Profile。源设备完成加密上传后，服务端才切换设备归属，并幂等创建目标设备的 start 命令；目标设备拉取并校验当前修订后才启动。

### 批量、授权与平台管理

批量接口使用 `/api/v1/workspaces/{workspaceID}/batch` 前缀：POST `/browser-instances`、`/browser-instances/start`、`/browser-instances/stop`、`/account-bindings`、`/proxy-assignments`、`/workflow-executions`。每项独立返回结果，不是跨项原子事务；字段以 `batch-service` 输入类型为准。

公开套餐目录为 GET `/api/v1/billing/plans` 和 `/api/v1/billing/release-channels`。组织授权接口前缀为 `/api/v1/organizations/{organizationID}/billing`，需 Bearer 和组织权限：GET `/subscription`、GET `/entitlements`、POST `/licenses/activate`、POST `/licenses/validate`、DELETE `/licenses/{activationID}`。这不是支付网关或自动扣款接口。

平台管理员接口需要独立 `platform_admins` 权限，工作区 Owner 不自动拥有：GET `/api/v1/admin/users`、PATCH `/api/v1/admin/users/{userID}/status`、GET `/api/v1/admin/organizations`、PATCH `/api/v1/admin/organizations/{organizationID}/status`、GET `/api/v1/admin/workspaces`、PUT/DELETE `/api/v1/admin/platform-admins/{userID}`。

### 任务与工作流

| 方法 | 路径 |
|---|---|
| GET, POST | `/api/v1/workspaces/{workspaceID}/tasks` |
| GET | `/api/v1/workspaces/{workspaceID}/tasks/{taskID}` |
| POST | `/api/v1/workspaces/{workspaceID}/tasks/{taskID}/cancel` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/workflows` |
| GET | `/api/v1/workspaces/{workspaceID}/workflows/{workflowID}` |
| POST | `/api/v1/workspaces/{workspaceID}/workflows/{workflowID}/versions` |
| GET | `/api/v1/workspaces/{workspaceID}/workflows/{workflowID}/versions/{version}` |
| POST | `/api/v1/workspaces/{workspaceID}/workflows/{workflowID}/publish` |
| POST | `/api/v1/workspaces/{workspaceID}/workflows/{workflowID}/archive` |

任务创建为 202，必须带 `Idempotency-Key`（最长 200）；工作流执行 payload 当前必须是只含 `instanceId` 的对象，并在数据库触发器中再次验证已发布版本和实例。`limit` 查询参数用于任务列表，非法/超范围时 service 使用默认值。

### 计划、分析和通知

| 方法 | 路径 |
|---|---|
| GET, POST | `/api/v1/workspaces/{workspaceID}/schedules` |
| GET, PATCH, DELETE | `/api/v1/workspaces/{workspaceID}/schedules/{scheduleID}` |
| POST | `/api/v1/workspaces/{workspaceID}/schedules/{scheduleID}/enable` |
| POST | `/api/v1/workspaces/{workspaceID}/schedules/{scheduleID}/disable` |
| GET | `/api/v1/workspaces/{workspaceID}/analytics/dashboard` |
| GET | `/api/v1/workspaces/{workspaceID}/analytics/events` |
| GET | `/api/v1/workspaces/{workspaceID}/analytics/audit-events` |
| GET | `/api/v1/workspaces/{workspaceID}/analytics/risk-events` |
| GET | `/api/v1/workspaces/{workspaceID}/notifications` |
| GET | `/api/v1/workspaces/{workspaceID}/notifications/unread-count` |
| POST | `/api/v1/workspaces/{workspaceID}/notifications/{notificationID}/read` |
| POST | `/api/v1/workspaces/{workspaceID}/notifications/read-all` |
| POST | `/api/v1/workspaces/{workspaceID}/notifications/socket-ticket` |
| GET (Upgrade) | `/api/v1/notifications/ws` |
| GET | `/api/v1/workspaces/{workspaceID}/notification-preferences` |
| PUT | `/api/v1/workspaces/{workspaceID}/notification-preferences` |

通知列表返回 `{items,nextOffset,unreadCount}`，支持 `limit`、`offset`、`unreadOnly=true`；通知 HTTP 面没有创建接口，可信生产者在服务内部使用幂等键。浏览器先以 Bearer 凭据取得一分钟有效的短时 socket ticket，再以服务端返回的 `ant-browser-notifications.v1` 子协议和 `ant-browser-ticket.<ticket>` 子协议升级连接；不得把 access token 放入 URL。计划更新使用版本字段做乐观并发控制；计划执行由 worker 内置 scheduler 完成。Cron 表达式无效（字段数不是五段、取值越界等）或永远不会触发（如 `0 0 30 2 *`）返回 422 `invalid_cron_expression`；时区不是 IANA 名称（包括空值和 `Local`）返回 422 `invalid_timezone`。`POST .../schedules/{scheduleID}/disable` 暂停计划并清空 `nextRunAt`；`/enable`（也用于恢复 `error` 状态）按保存的规则从当前时间重新计算 `nextRunAt`，暂停期间错过的运行不会补跑；对已在运行的计划调用 `/enable` 保留其待执行的下一次运行。修改暂停中计划的规则不会生成 `nextRunAt`。

### Profile 同步与指纹模板

| 方法 | 路径 |
|---|---|
| GET, POST | `/api/v1/workspaces/{workspaceID}/profiles` |
| GET | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}` |
| POST | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/lease` |
| POST | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/lease/renew` |
| DELETE | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/lease` |
| POST | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions` |
| GET | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions` |
| GET | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}` |
| POST | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}/objects/{objectID}/upload` |
| GET | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}/objects/{objectID}/download` |
| POST | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}/commit` |
| POST | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/revisions/{revisionID}/restore` |
| GET | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/conflicts` |
| POST | `/api/v1/workspaces/{workspaceID}/profiles/{profileID}/conflicts/{conflictID}/resolve` |

后台设备代理使用等价的 `/api/v1/agent/profiles/{profileID}/...` 同步路由，认证头为 `Authorization: Device <credential>` 与 `X-Device-ID`。设备权限不是工作区成员权限：网关只允许访问当前分配给该设备的浏览器实例所引用的 Profile，并拒绝 body 中伪造的其他 `deviceId`。
| GET | `/api/v1/workspaces/{workspaceID}/fingerprint-presets` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/fingerprint-templates` |
| POST | `/api/v1/workspaces/{workspaceID}/fingerprint-templates/batch` |
| GET, PATCH, DELETE | `/api/v1/workspaces/{workspaceID}/fingerprint-templates/{templateID}` |

`fingerprint-presets` 返回只读的 Amazon US、TikTok US 与 Facebook EU 示例；读取需要 `fingerprint.read`。模板写操作需要 `fingerprint.manage`。批量创建一次接受 1–100 项，使用 `namePrefix`、`count`、模式、起始种子和一份完整配置原子写入；任一名称冲突或校验失败时不会留下部分模板。超过上限返回 413 `fingerprint_batch_too_large`，字段或数量无效返回 422 `fingerprint_validation_failed`，重名返回 409 `fingerprint_name_conflict`。

上传/下载路由由 profile service 准备对象存储操作；实际对象字节不经过数据库。租约、修订版本和冲突是云端同步协议，不能等同于桌面完整 user-data 目录已经自动同步。

同步协议要点：

- 租约：其他设备持有有效租约时返回 423 `profile_lease_held`。设备凭据请求（`/agent/profiles/...`）可以直接替换本设备自己的有效租约，崩溃后无需等租约过期；Bearer 用户请求不能抢占设备租约。
- 冲突阻断：Profile 存在 `open` 冲突时，申请租约和开始修订都返回 409 `profile_conflict_unresolved`，任何设备都不能拉取、推送或恢复，直到冲突被处理。
- 开始修订时 `baseRevisionId` 不是当前修订（或云端已有修订而未提供 base），返回 409 `profile_revision_conflict`：`data` 仍是完整计划（`uploading` 修订、manifest、对象和 `conflict`），`error.details.conflictId` 为冲突 ID。此时 Profile 状态为 `conflict`，该修订不能提交；设备仍可用同一租约上传快照对象，然后必须释放租约。
- 处理冲突：`POST .../conflicts/{conflictID}/resolve`，body `{ "resolution": "keep_local" | "keep_remote" }`，需要 `profile.sync`，审计动作为 `profile.conflict.resolve`。`keep_local` 先校验本地修订的全部对象（缺失或不匹配返回 422 `profile_object_unavailable`，此时只能 `keep_remote`），再把该快照提升为当前修订；要求本地修订仍为 `uploading`、manifest 为 `snapshot`、当前修订仍是冲突记录的远端修订（否则 409 `profile_revision_state`），且没有有效租约（否则 423 `profile_lease_held`）。`keep_remote` 把本地修订标记为 `superseded`。冲突已处理同样返回 409 `profile_revision_state`。
- 存储配额：提交或提升修订会按组织 `storage_bytes` 权益计算净增量，超限或权益不可用返回 402 `quota_exceeded`。

### 账号与代理中心

| 方法 | 路径 |
|---|---|
| GET, POST | `/api/v1/workspaces/{workspaceID}/accounts` |
| GET, PATCH, DELETE | `/api/v1/workspaces/{workspaceID}/accounts/{accountID}` |
| POST | `/api/v1/workspaces/{workspaceID}/accounts/{accountID}/status` |
| POST | `/api/v1/workspaces/{workspaceID}/accounts/{accountID}/risk-level` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/accounts/{accountID}/secrets` |
| DELETE | `/api/v1/workspaces/{workspaceID}/accounts/{accountID}/secrets/{secretID}` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/accounts/{accountID}/bindings` |
| DELETE | `/api/v1/workspaces/{workspaceID}/accounts/{accountID}/bindings/{bindingID}` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/accounts/{accountID}/risk-events` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/proxies` |
| GET, PATCH, DELETE | `/api/v1/workspaces/{workspaceID}/proxies/{proxyID}` |
| POST | `/api/v1/workspaces/{workspaceID}/proxies/{proxyID}/assignments` |
| GET | `/api/v1/workspaces/{workspaceID}/proxy-assignments` |
| GET, DELETE | `/api/v1/workspaces/{workspaceID}/proxy-assignments/{targetType}/{targetID}` |
| GET, POST | `/api/v1/workspaces/{workspaceID}/proxies/{proxyID}/health-checks` |
| GET | `/api/v1/workspaces/{workspaceID}/proxy-health-checks/{checkID}` |

账号/代理敏感凭据由服务端 envelope 加密后存储；健康检查可排入 `proxy.health_check` 任务，是否能执行取决于 worker 是否配置代理探针。

- 名称唯一性：实例、代理、云端 Profile、指纹模板的名称，以及账号在同一平台下的标识，只在工作空间内**未删除**的资源之间唯一，且不区分大小写；删除（软删除）后名称立即可重用。冲突返回 409：`instance_name_conflict`、`proxy_name_conflict`、`profile_name_conflict`、`fingerprint_name_conflict`、`account_identifier_conflict`。工作流只归档不删除，名称在含已归档工作流内唯一（区分大小写），冲突为 409 `workflow_name_conflict`。批量创建中的同类冲突在单项结果里报告为 `name_conflict`。
- 代理分配读取需要 `proxy.read`（所有角色都有）。`GET .../proxy-assignments` 返回全部未删除分配，可用 `proxyId` 和 `targetType`（`browser_instance`、`account`、`profile`）过滤；`GET .../proxy-assignments/{targetType}/{targetID}` 返回某目标当前的分配，没有时 404。分配对象带 `proxyName`（读取时解析的代理当前名称，不单独存储）；解除分配所需的 `version` 从这里读取。目标已分配其他代理时，POST 分配返回 409 `proxy_assignment_conflict`；同一代理重复分配幂等返回现有分配。
- 风险事件 `level` 只接受 `info`、`low`、`medium`、`high`、`critical`（与 `risk_events.severity` 和分析接口的 `severity` 过滤一致），大小写不敏感；`unknown` 只用于账号自身的风险等级，用在风险事件上返回 422 `validation_failed`。

### Agent 接口

| 方法 | 路径 | 认证/协议 |
|---|---|---|
| GET (Upgrade) | `/api/v1/agent/ws` | `X-Device-ID` + `Authorization: Device <credential>`；WebSocket 子协议 `ant-browser-agent.v1` |
| GET | `/api/v1/agent/instances/{instanceID}/runtime-config` | Device 凭据；仅限分配给当前设备的实例 |
| POST | `/api/v1/agent/tasks/claim` | Device 凭据 |
| POST | `/api/v1/agent/tasks/{taskID}/report` | Device 凭据 |

WebSocket 连接接收 `server.hello`、`command.dispatch`，回传 `agent.hello`/`agent.heartbeat`、`command.accepted|running|completed|failed` 和 `instance.observed`。缺少子协议返回 426；设备凭据无效/撤销返回 401 `invalid_device_credential`。所有 Agent 路由（含 WebSocket 握手）在凭据校验因基础设施故障（如数据库不可用）而无法完成时返回 503 `dependency_unavailable`，此时 Agent 应保留凭据并退避重试，而不是把凭据当作已失效。桌面命令客户端为显式 opt-in，并使用本地持久执行日志避免重连/重启后重复副作用，配置见 [`cloud-commands.md`](cloud-commands.md)。Agent 在执行 start/restart/migrate 启动前通过 `runtime-config` 取得当前指纹模板；响应身份、平台、Chromium 主版本、启动参数和高级配置均由桌面再次校验，且客户端拒绝重定向以避免设备凭据泄漏。Agent 路由不是普通用户的 Bearer API。

## 错误和请求约定

云端 JSON 错误统一为：

```json
{
  "error": {
    "code": "validation_failed",
    "message": "human-readable message",
    "details": {},
    "traceId": "request-id"
  }
}
```

`details` 可省略。`X-Request-ID` 长度不在 8–128 字符时由服务端生成 UUID，并在响应头回写；该值也是 `traceId`。JSON body 最大 1 MiB，拒绝未知字段和尾随 JSON 值，解析失败为 400 `invalid_request`。

常见状态码/错误码包括：401 `unauthorized`、`invalid_access_token`、`invalid_credentials`、`invalid_device_credential`、`mfa_challenge_invalid`；409 `mfa_already_enabled`、`mfa_not_enabled`、`mfa_setup_required`；422 `mfa_invalid_code`、`mfa_code_required`、`invalid_password`；429 `mfa_locked`（带 `Retry-After`）；503 `mfa_unavailable`；403 `forbidden`、`resource_disabled`；404 `not_found`；402 `quota_exceeded`；409 `resource_conflict`、`last_owner`、`version_conflict`、`task_state_conflict`、`schedule_state_conflict`、`command_transition_conflict`、`notification_conflict`、`profile_lease_invalid`、`profile_revision_conflict`、`profile_conflict_unresolved`、`profile_revision_state`、`instance_name_conflict`、`proxy_name_conflict`、`profile_name_conflict`、`fingerprint_name_conflict`、`workflow_name_conflict`、`account_identifier_conflict`、`proxy_assignment_conflict`；403 `profile_device_scope`；423 `profile_lease_held`；422 `validation_failed`、`invalid_role`、`weak_password`、`profile_object_unavailable`、`invalid_cron_expression`、`invalid_timezone`；429 `rate_limited`（带 `Retry-After`）；503 `dependency_unavailable`/`service_unavailable`；未映射异常为 500 `internal_error`。

## 桌面本地 Launch API

桌面 `LaunchServer` 默认仅绑定 `127.0.0.1`，默认端口 `19876`。API key 认证默认关闭；配置 `launch_server.auth.enabled=true` 且 key 非空后，受保护的 `/api/*` 路由要求配置的 header（默认 `X-Ant-Api-Key`）。统一 CDP 根代理 `/` 不经过该 API key 校验，但仍受回环监听限制。桌面错误格式是 `{ok:false,error:"..."}`，与云端 `error.code/message/traceId` 格式不同。

实际路由族：

- `GET /api/health`
- `GET, POST /api/profiles`，以及 `GET, PUT, DELETE /api/profiles/{id}`（具体方法由 handler 按请求分派）
- `GET /api/runtime/active`、`POST /api/runtime/session`、`POST /api/runtime/status`、`POST /api/runtime/stop`
- `GET /api/launch/{code}`、`POST /api/launch`、`GET /api/launch/logs`
- `GET /api/automation/scripts`、`GET /api/automation/scripts/{id}`、`POST /api/automation/scripts/run`、`GET /api/automation/scripts/runs`、`POST /api/automation/hooks/{hook}`
- `/`：转发到当前活动实例 CDP；没有活动且 ready 的实例时不可用。

`POST /api/launch` 支持按 code、profileId、名称、关键字、标签、分组或 selector 选择实例，也支持一次选择全部匹配项；可携带启动参数、起始 URL、proxy override。该 API 是本机自动化/快捷启动接口，不是 Cloud SaaS 公共 API，也没有跨租户认证语义。
