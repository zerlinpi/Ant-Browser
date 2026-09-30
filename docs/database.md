# 数据库与持久化（当前实现）

仓库有两套明确分开的数据面：桌面应用使用本地 SQLite；Cloud control-plane/worker 使用 PostgreSQL。不要把 `data/app.db` 的本地结构或完整 Chromium user-data 目录当作云端多租户数据库。

## 本地桌面存储

`config.yaml` 默认 `database.type=sqlite`、路径 `data/app.db`。`backend/app_startup.go` 在启动时创建 SQLite DB、执行本地迁移并注入 Profile、Proxy、Core、Bookmark、Group、Extension 和 LaunchCode DAO。连接启用 WAL、外键约束和单进程访问；运行中的实例状态和部分调度状态仍在内存/进程生命周期内。

浏览器 Profile 的 Cookies、LocalStorage、IndexedDB、扩展、书签、历史和 Preferences 等主要由每个实例隔离的 Chromium `user-data-dir` 保存。桌面备份代码可导出 SQLite（含存在时的 `-wal`/`-shm`）及 Profile 包，但这不是 Cloud profile revision/object 同步协议。

## Cloud PostgreSQL 迁移

迁移文件位于 `database/migrations/`，当前文件序列为 `001`–`029`。迁移程序 `server/cmd/migrate` 只支持前向 `up`；`postgres.Migrate` 按文件名排序，在 `pg_advisory_lock(hashtext('ant-browser-schema-migrations'))` 下逐个事务执行，并把 SHA-256 checksum 写入 `schema_migrations`。已应用文件 checksum 改变会停止迁移，禁止重写历史。

| 迁移 | 内容 |
|---|---|
| 001 | 用户、组织、工作空间、角色/权限、成员、session、refresh token、设备、审计/登录事件 |
| 002 | 浏览器实例期望/观测状态、instance session/command/event |
| 003 | 加密 Profile 修订、manifest、对象、同步租约、冲突和恢复事件 |
| 004 | 工作空间账号、代理、代理凭据/分配、健康数据 |
| 005 | 版本化自动化工作流、任务队列、运行/尝试、租约和 artifacts |
| 006 | 通知、投递、分析、指标和风险事件 |
| 007 | plans、subscriptions、entitlements、usage、license 等商业模型 |
| 008 | workspace/organization RLS 基础策略和租户设置函数 |
| 009 | service contract 兼容列、状态、版本和索引 |
| 010 | task type、错误诊断、每任务一个 durable run 和 worker claim 索引 |
| 011 | fingerprint templates 和 Profile 同步补充状态 |
| 012 | 工作流 latest/published version、乐观版本和归档状态 |
| 013 | 账号/代理中心生命周期、secret envelope、绑定和健康检查约束 |
| 014 | 代理健康任务进入 cancelled/failed/dead-letter 时同步健康请求终态 |
| 015 | 入队时锁定并校验已发布工作流版本和唯一 instance target |
| 016 | 计划 instance target、版本、状态、租约、到期索引和 workflow-version 外键 |
| 017 | 通知幂等键及 `(workspace_id, recipient_user_id, idempotency_key)` 唯一索引 |
| 018 | runtime 数据库角色、FORCE RLS、worker 受限系统操作和设备认证函数 |
| 019 | Free / Professional / Enterprise 套餐与权益目录 |
| 020 | 通知投递领取、重试与受限 worker 函数 |
| 021 | 独立 `platform_admins` 模型及组织状态，对应 `/api/v1/admin/*` 路由 |
| 022 | 审计事件的 FORCE RLS 与平台管理员权限检查 |
| 023 | 新组织默认订阅，实例数量和组织去重成员席位的事务级配额保护 |
| 024 | 单次邀请哈希、有效期、撤销/接受状态及成员/受邀用户行级隔离 |
| 025 | 实例到代理分配的组合租户外键，补齐更新场景的跨工作区引用保护 |
| 026 | Profile 存储台账：组织级已用字节 `profile_storage_usage` 与每个 Profile 当前文件集 `profile_storage_files`（组织级 FORCE RLS），供修订提交和 keep_local 提升按净增量扣减 `storage_bytes` 配额 |
| 027 | 实例、代理、指纹模板、云端 Profile 名称及账号（平台内）标识改为只在未删除行之间唯一、不区分大小写（`lower()` 部分唯一索引），软删除后名称可重用 |
| 028 | worker 运行时权限：租户 RLS 辅助函数改为 SECURITY DEFINER；工作流目标校验与调度派发的 FOR SHARE 行锁改由 NOLOGIN 的 `ant_workflow_guard` 角色执行（`schedule_target_executable`） |
| 029 | TOTP 两步验证：`user_mfa_factors`（每用户一个 pending/active 因子、密封后的密钥、最近使用的时间步、失败计数与锁定时间）、`user_mfa_recovery_codes`（恢复码哈希，随因子级联删除）、`mfa_login_challenges`（登录第二步的挑战令牌哈希、设备 ID、尝试次数、有效期与消费时间） |

迁移 025 会校验既有引用；如存在失效或跨租户的 `browser_instances.proxy_assignment_id`，迁移会失败并回滚，不能静默清空用户配置。上线前需由管理员核对和修复异常引用，再重新执行迁移。

迁移 026 之前，PostgreSQL 上的每次修订提交都会因台账表缺失而以 `profile storage quota exceeded` 失败，因此不存在需要回填的当前文件集。台账缺失或损坏时提交按超额拒绝（fail closed），不会从零开始计费。

迁移 027 会先检查未删除行中只有大小写不同的重名（例如实例 `Shop` 与 `shop`，或同平台账号标识 `A@x.com` 与 `a@x.com`）。存在时迁移失败并回滚，错误信息给出表名和工作空间 ID（不输出名称，账号标识可能是邮箱），需先改名或删除重复项再重跑；迁移不会自动改名。

迁移 028 之前，`ant_worker` 连接对任何带租户 RLS 的表的查询都会报 `permission denied for table workspaces`（021 起 RLS 辅助函数以调用者权限读取 `workspaces`），任务领取、调度派发和代理健康检查在生产角色下都无法运行；集成测试此前以表所有者身份运行，未能发现。`ant_workflow_guard` 与 023 的 `ant_billing_enforcer` 一样是 NOLOGIN、BYPASSRLS 且没有成员的角色，只作为这两个校验函数的所有者；`schedule_target_executable` 只接受一个真实存在且指向同一工作流版本和实例的计划，worker 无法借它探测任意目标。

迁移 029 的三张表与 `sessions` 一样按用户归属，不属于任何工作空间，因此没有租户 RLS 策略，只显式授权给 `ant_control_plane`（`ant_worker` 无权读取）。TOTP 密钥由应用层 envelope 加密后以自描述文本存入 `secret_envelope`，AAD 绑定用户 ID，数据库管理员看不到明文，也不能把密钥挪给其他用户；恢复码（80 bit 随机值）和挑战令牌只保存 SHA-256 摘要。`last_used_step` 保证每个时间步的验证码只能用一次；`failed_attempts`/`locked_until` 是持久化的按用户锁定计数，跨挑战、跨接口和跨副本生效。`status='active'` 与 `confirmed_at` 由 CHECK 约束保持一致。

`workspace_invitations` 只存储 256-bit 随机邀请令牌的 SHA-256 哈希，不存明文。接受时锁定邀请记录，并核对持久化用户邮箱、用户和租户状态；成员写入、席位配额检查、邀请消费在同一事务内完成。列表不返回令牌，创建接口仅返回一次。当前邀请由管理员自行安全传递，尚未接入邮件邀请投递。

主要表按域分组：

- 身份/租户：`users`、`organizations`、`organization_members`、`workspaces`、`workspace_members`、`roles`、`permissions`、`role_permissions`、`sessions`、`refresh_tokens`、`user_mfa_factors`、`user_mfa_recovery_codes`、`mfa_login_challenges`、`devices`、`device_credentials`、`audit_events`、`login_events`、`platform_admins`。
- 浏览器/同步：`browser_instances`、`instance_sessions`、`instance_commands`、`instance_events`、`browser_profiles`、`profile_revisions`、`profile_manifests`、`profile_objects`、`profile_sync_leases`、`profile_conflicts`、`profile_restore_events`、`fingerprint_templates`、`artifacts`。
- 运营/执行：`accounts`、`account_secrets`、`account_secret_access_events`、`account_bindings`、`proxies`、`proxy_credentials`、`proxy_assignments`、`proxy_health_samples`、`proxy_health_checks`、`workflows`、`workflow_versions`、`workflow_permissions`、`tasks`、`task_runs`、`task_attempts`、`schedules`。
- 通知/观测/商业：`notification_preferences`、`notifications`、`notification_deliveries`、`analytics_events`、`metric_rollups`、`risk_events`、`plans`、`plan_entitlements`、`subscriptions`、`subscription_events`、`entitlements`、`usage_counters`、`usage_reservations`、`release_channels`、`license_activations`。

## 租户、角色和 RLS

组织是上层租户，工作空间是绝大多数运行资源的隔离边界。workspace 资源带 `workspace_id`，商业/订阅资源带 `organization_id`。预置 RBAC 角色为 `owner`、`admin`、`manager`、`operator`、`viewer`；角色/权限检查在 service 层执行，数据库 RLS 是第二道边界。`platform_admins` 是独立的平台权限，不从组织 owner 或 workspace 角色推导。

迁移 008 为 workspace/organization 资源启用 RLS；迁移 018 对运行角色使用 `FORCE ROW LEVEL SECURITY`。`server/platform/postgres/tenant.go` 将请求上下文中的 workspace、organization、user 和受限 system operation 设置为**事务本地** `app.current_*` 参数，避免连接池泄漏租户身份。空、非法 UUID、冲突租户或任意 system operation 都会在 repository 前拒绝。

生产连接必须使用非 owner、`NOBYPASSRLS` 角色：

| 进程 | 变量 | 角色/权限 |
|---|---|---|
| migrate 一次性任务 | `ANT_MIGRATION_DATABASE_URL` | schema owner；只用于迁移 |
| control-plane | `ANT_DATABASE_URL` | `ant_control_plane`；普通 API CRUD + RLS |
| worker | `ANT_WORKER_DATABASE_URL` | `ant_worker`；任务/计划/代理健康等最小表权限 |
| 设备认证函数 | — | `ant_device_authenticator` NOLOGIN；仅由 `authenticate_device(UUID,TEXT)` SECURITY DEFINER 使用 |

worker 的跨工作空间操作只允许固定 `task_claim`、`schedule_dispatch` system operation；HTTP 输入不能设置它。设备在尚不知道工作空间时只能走受限 `authenticate_device` 函数，不能获得通用凭据读取权限。

## 密钥与对象存储

账号/代理等敏感值通过 envelope 加密后落在 PostgreSQL；Profile/automation 对象字节使用 S3/MinIO。生产 `server/platform/config` 要求数据库、Redis、NATS、对象存储、`ANT_ENCRYPTION_KEY_REF`、非开发 `ANT_SECRET_MASTER_KEY` 和 JWT secret；开发可退回 memory/metadata 模式，但配置代码明确将其标记为不适合生产。

## 备份、恢复与部署原则

应用和迁移代码不执行自动备份、PITR 或恢复；这些是运维责任。依据 `docs/cloud-deployment.md`、Compose 和角色文档，生产应遵守：

- PostgreSQL 是权威源，启用加密备份和 PITR；迁移/破坏性变更前保留备份，并定期做恢复演练。
- Redis 只保存短期 cache、presence 和 fan-out，不是事实源，丢失后应可重建。
- Compose 为 NATS 打开 JetStream 并持久化 `/data`，但当前 `server/platform/natstask` 实现使用普通 Publish/QueueSubscribe 发送任务唤醒信号；不要把该信号总线当作任务事实源，任务最终状态和可恢复性仍以 PostgreSQL 为准。
- S3/MinIO 对 Profile/automation 对象启用版本化、加密和生命周期策略，并将对象备份与 PostgreSQL manifest/revision 一起恢复。
- Compose 新建 PostgreSQL volume 时由 `deploy/postgres/init-runtime-roles.sh` 创建 runtime LOGIN 角色；已有数据库不会自动轮换凭据，切换 URL 前必须显式 provision/rotate。
- 不向公网暴露 PostgreSQL、Redis、NATS、MinIO 管理端口；公网 HTTP/WebSocket 应在 TLS 反代后，迁移作为串行 release job 运行。

恢复顺序应先恢复 PostgreSQL 和 schema migration 元数据，再恢复对象存储并核对 Profile manifests/revisions，最后重建 Redis presence/cache 并启动 worker 重新扫描任务。不要通过把 control-plane 或 worker 改用 migration owner 来绕过 RLS。
