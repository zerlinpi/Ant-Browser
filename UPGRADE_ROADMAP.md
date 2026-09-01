# Ant-Browser 商业化升级路线图

> 基线：<code>master@42e8b9a6dd882fdd5e2e634ff305052325b182fb</code>。  
> 目标：在不破坏现有本地浏览器、Profile、代理和自动化能力的前提下，升级为 Cloud SaaS + Desktop Client + Browser Runtime 的商业级跨境电商多账号平台。

## 1. 路线图原则

1. 不一次性重写。现有 Wails/React 桌面应用继续作为可运行基线，按绞杀者模式逐步抽出 Desktop Agent 和 Browser Runtime。
2. Cloud 只管理身份、协作、策略、desired state、同步版本、任务和审计；本机 Agent 负责 Chromium、Profile 文件、代理内核、CDP 和实际执行。
3. 先建立可编译、可测试的服务边界，再决定独立部署。目标服务名全部保留，但早期使用模块化单体 + Worker，避免同时维护 12 个只有 TODO 的微服务。
4. Profile、Cookie、账号密码、2FA、代理凭据均按高敏感资产处理：传输加密、静态加密、租户密钥、最小权限和完整审计。
5. 保持代理连接栈不变量：Xray + sing-box 组合栈与独立 Mihomo 栈不得自动混用。
6. 所有 Cloud 命令必须幂等、可重放、可取消、可审计；网络断开不等于执行失败。
7. 每个 Phase 都必须通过退出门禁，上一阶段未达标不得堆叠下一阶段产品功能。

## 2. 目标架构

~~~mermaid
flowchart TB
    subgraph Cloud[Cloud Platform]
        GW[Gateway Service]
        AUTH[Auth/User/Workspace]
        INST[Browser Instance Service]
        SYNC[Profile Sync Service]
        PROXY[Proxy Service]
        TASK[Task/Automation Service]
        NOTE[Notification Service]
        BILL[Billing/License Service]
        ANA[Analytics Service]
        PG[(PostgreSQL)]
        R[(Redis)]
        N[NATS]
        O[(S3/MinIO)]
    end

    subgraph Desktop[Desktop Client]
        UI[Wails + Vue3 + TypeScript]
        AG[Desktop Agent]
        J[(SQLite Cache + Operation Journal)]
    end

    subgraph Runtime[Browser Runtime]
        BM[Instance Manager]
        PS[Profile Loader]
        FP[Fingerprint Adapter]
        PX[Proxy Supervisor]
        AU[Automation Runner]
        CDP[Authenticated CDP Adapter]
        CH[Chromium]
    end

    UI --> AG
    AG <-->|REST bootstrap| GW
    AG <-->|WebSocket commands/events| GW
    AG <-->|encrypted profile chunks| O
    AG --> J
    AG --> BM
    BM --> PS
    BM --> FP
    BM --> PX
    BM --> CDP
    AU --> CDP
    CDP --> CH
    PS --> CH
    FP --> CH
    PX --> CH

    GW --> AUTH
    GW --> INST
    GW --> SYNC
    GW --> PROXY
    GW --> TASK
    GW --> NOTE
    GW --> BILL
    GW --> ANA
    AUTH --> PG
    INST --> PG
    SYNC --> PG
    SYNC --> O
    TASK --> N
    TASK --> R
~~~

## 3. 服务目录目标

Phase 2 先统一为一个 Go module，并在同一仓库中形成明确 bounded context：

~~~text
server/
├── cmd/
│   ├── control-plane/
│   ├── worker/
│   └── migrate/
├── services/
│   ├── gateway-service/
│   ├── auth-service/
│   ├── user-service/
│   ├── workspace-service/
│   ├── browser-instance-service/
│   ├── profile-sync-service/
│   ├── proxy-service/
│   ├── task-service/
│   ├── automation-service/
│   ├── notification-service/
│   ├── billing-service/
│   └── analytics-service/
├── platform/
│   ├── postgres/
│   ├── redis/
│   ├── nats/
│   ├── objectstore/
│   ├── crypto/
│   └── observability/
├── contracts/
│   ├── openapi/
│   ├── asyncapi/
│   └── events/
├── migrations/
└── tests/
~~~

初期 <code>control-plane</code> 可以组合多个 services 包；只有出现独立扩缩容、安全域或发布节奏需求时，才拆为独立进程。目录边界从第一天存在，部署边界按数据证明演进。

## 4. 核心架构决策

### ADR-001：保留现有运行时，抽出 Agent

现有 <code>backend/app_instance_*.go</code>、<code>backend/internal/browser</code>、<code>backend/internal/proxy</code>、<code>backend/internal/automation</code> 是真实资产。Phase 3 将其重构到 Agent/Runtime ports，而不是用 <code>browser-runtime/</code> 的 TODO 文件替换。

目标接口：

- ProfileRepository
- InstanceLauncher
- InstanceSupervisor
- CDPProvider
- ProxySupervisor
- AutomationRunner
- LocalOperationJournal
- CloudCommandClient
- ProfileArtifactStore

Wails event、文件对话框和 UI toast 留在桌面适配层；运行时核心不依赖 Wails。

### ADR-002：新商业客户端使用 Wails + Vue3 + TypeScript

用户目标明确要求 Vue3。为避免框架重写阻塞 Cloud：

1. 保留当前 <code>frontend/</code> React 客户端作为兼容基线。
2. 在 <code>desktop-client/</code> 建立新的 Wails/Vue3/TypeScript 客户端。
3. 新客户端只通过 Agent application service 和 Cloud API 工作，不直接访问 SQLite 或 <code>exec.Cmd</code>。
4. Browser Manager、Proxy Center、Automation 等达到功能等价且回归通过后再切换默认入口。
5. React 客户端至少保留一个稳定发布周期作为回滚通道。

### ADR-003：Cloud 使用 PostgreSQL + Redis + NATS + Object Storage

- PostgreSQL：事务数据、租户资源、版本元数据、审计索引。
- Redis：短时 session、限流、幂等缓存、在线 presence；不是权威数据源。
- NATS JetStream：命令/事件、任务分发、重试与消费者租约。
- S3/MinIO：加密 Profile chunk、截图、报告和自动化 artifact。
- WebSocket：Agent 长连接、命令 ACK、进度、状态和通知；不承担大文件传输。

### ADR-004：服务端不保存可直接使用的明文秘密

- <code>accounts</code> 仅保存平台、标识、状态、风险和绑定关系。
- 密码、2FA seed、Cookie artifact key、代理凭据进入 Vault/KMS 或 envelope-encrypted <code>account_secrets</code>。
- 默认 UI 永不返回 secret 原文；读取需高权限、二次验证、理由和审计。
- Profile artifact 每个 workspace 使用独立数据密钥；服务端对象存储只见密文。

## 5. Cloud 数据模型

### 5.1 身份与组织

- users
- oauth_identities
- organizations
- organization_members
- workspaces
- workspace_members
- roles
- permissions
- role_permissions
- member_roles
- sessions
- refresh_tokens
- devices
- device_credentials
- login_events
- audit_events

角色基线：Owner、Admin、Manager、Operator、Viewer。角色是权限集合，不允许用 role 非空代替权限判断。所有业务表必须带 organization_id/workspace_id，并使用复合唯一约束、索引和 PostgreSQL RLS 或等价的 repository tenant guard。

### 5.2 浏览器与 Profile

- browser_instances
- instance_sessions
- instance_commands
- instance_events
- browser_profiles
- profile_revisions
- profile_manifests
- profile_objects
- profile_sync_leases
- profile_conflicts
- fingerprint_templates
- proxy_assignments

<code>browser_instances</code> 至少包含：

- id
- workspace_id
- name
- platform
- fingerprint_template_id
- proxy_assignment_id
- profile_id
- desired_state
- observed_state
- assigned_device_id
- current_revision
- version
- created_at / updated_at / deleted_at

### 5.3 账号、任务与商业化

- accounts
- account_bindings
- account_secrets
- account_risk_events
- proxies
- proxy_health_samples
- workflows
- workflow_versions
- schedules
- tasks
- task_runs
- task_attempts
- artifacts
- notifications
- plans
- subscriptions
- entitlements
- usage_counters
- license_activations

## 6. API 与事件契约

### 6.1 REST API

| 域 | 端点示例 |
| --- | --- |
| Auth | <code>POST /api/v1/auth/register</code>、<code>/login</code>、<code>/refresh</code>、<code>/logout</code>、<code>/mfa/verify</code> |
| OAuth | <code>GET /api/v1/oauth/{provider}/authorize</code>、callback |
| User/Session | <code>GET /api/v1/me</code>、<code>GET/DELETE /api/v1/sessions</code> |
| Workspace | <code>/api/v1/workspaces</code>、members、invites、roles、audit-events |
| Devices | <code>/api/v1/devices</code>、bind、revoke、rotate-credential |
| Instances | <code>/api/v1/browser-instances</code>、clone、assign、commands |
| Profiles | <code>/api/v1/profiles</code>、revisions、manifest、restore、conflicts |
| Proxies | <code>/api/v1/proxies</code>、assignments、health-checks |
| Accounts | <code>/api/v1/accounts</code>、bindings、risk-events |
| Automation | <code>/api/v1/workflows</code>、versions、tasks、runs、artifacts |
| Notifications | <code>/api/v1/notifications</code>、preferences |
| Billing | <code>/api/v1/plans</code>、subscription、usage、webhooks |

通用规则：

- OpenAPI 3.1 为同步 API 的唯一事实源。
- 所有写请求使用 Idempotency-Key。
- 资源使用 ULID/UUID，不使用可枚举自增 ID 作为外部 ID。
- 更新使用 version/ETag 的乐观锁。
- 错误使用统一 code、message、details、traceId。
- 分页使用 cursor；审计和事件不可用 offset 做无限翻页。

### 6.2 Agent WebSocket

连接：

<code>GET /api/v1/agent/ws</code>

命令包：

~~~json
{
  "type": "command",
  "commandId": "01...",
  "idempotencyKey": "workspace-instance-action-version",
  "workspaceId": "01...",
  "deviceId": "01...",
  "instanceId": "01...",
  "action": "instance.start",
  "expectedVersion": 12,
  "deadline": "2026-09-01T10:00:00Z",
  "payload": {}
}
~~~

Agent 返回：

- command.accepted
- command.progress
- command.completed
- command.failed
- instance.observed-state
- device.heartbeat
- profile.sync-progress
- proxy.health-result

Cloud 至少一次投递；Agent 通过本地 operation journal 对 commandId/idempotencyKey 去重。断线重连时先同步 lastEventSequence，再接收补发，不允许把 WebSocket 临时断开当作命令失败。

## 7. Profile Sync Engine

### 7.1 同步内容

- Cookies
- LocalStorage
- SessionStorage
- IndexedDB
- Extensions 与实例启用配置
- Bookmarks
- History
- Preferences

### 7.2 一致性策略

1. 运行中 Profile 默认不可直接打包。
2. Agent 先申请 profile_sync_lease。
3. 关闭实例，或执行受支持的 flush/quiesce 流程。
4. 生成规范化 manifest：逻辑路径、大小、SHA-256、chunk 列表、分类、敏感级别。
5. 对 chunk 压缩、按 workspace 数据密钥加密后上传对象存储。
6. 使用 baseRevision + ETag 提交新 revision。
7. Cloud 原子写入 revision 和 manifest，再发布 profile.revision-created。
8. Agent 恢复/启动前校验 manifest、hash、解密和磁盘配额。

第一版先交付一致性全量快照；第二版基于内容寻址 chunk 去重实现增量传输。不要一开始尝试解析和合并所有 Chromium 内部数据库。

### 7.3 冲突规则

- 同一 Profile 同时只能有一个写 lease。
- baseRevision 不匹配时创建 conflict，不静默覆盖。
- Cookies、IndexedDB、History 默认不做字段级自动合并。
- Bookmarks 和非敏感 Preferences 可在明确规则下三方合并。
- 用户可以保留本地、保留云端、复制为新 Profile 或恢复历史版本。
- 所有解决动作写入 audit_events。

## 8. 分阶段实施

### Phase 1：架构基线与路线图

目标：冻结当前真实架构、问题和升级边界，不修改业务运行时。

| 交付项 | 本阶段结果 |
| --- | --- |
| 修改文件 | 无业务代码修改 |
| 新增文件 | <code>CURRENT_ARCHITECTURE.md</code>、<code>UPGRADE_ROADMAP.md</code> |
| 数据库变化 | 无 |
| API 设计 | 记录当前本地 Launch/CDP；定义 Cloud REST/Agent WS 方向 |
| 测试方案 | GitHub 目录树复核、关键文件证据检查、Markdown 结构和提交回读 |
| 运行结果 | 已扫描 770 个文件、156 个目录、37 个 Go 测试文件；企业化目录标记为骨架 |

退出门禁：

- 两份文档进入 master。
- 所有现有能力、骨架和缺失项使用一致口径。
- 后续 Phase 不再直接在占位目录继续堆 TODO。

### Phase 2：Cloud Backend 基线

目标：交付可运行的身份、工作空间、设备和实例控制平面。

拟修改/新增：

- 重建 <code>server/go.mod</code>、<code>server/cmd</code>、<code>server/services</code>、<code>server/platform</code> 和 <code>server/contracts</code>。
- 保留有价值的 SQL 概念，但重新编号不可变 migrations；废弃重复 <code>workspace_members</code> 定义。
- 新增 <code>deploy/compose/docker-compose.yml</code>、服务 Dockerfile、环境模板和迁移命令。
- 新增根 CI：Go fmt/vet/test、server test、migration test、frontend build、依赖和 secret scan。

数据库变化：

- users、oauth_identities、organizations、workspaces、members。
- roles、permissions、sessions、refresh_tokens。
- devices、device_credentials、login_events、audit_events。
- browser_instances、instance_commands、instance_events。

API：

- 注册、登录、JWT access token、旋转 refresh token、注销、MFA/OAuth 基线。
- 工作空间 CRUD、邀请、成员、RBAC。
- 设备绑定/吊销/心跳。
- 实例 desired state 和 Agent WebSocket。

测试：

- repository/service/handler 单元测试。
- Testcontainers PostgreSQL/Redis/NATS 集成测试。
- migration up/down/forward-only 重放测试。
- RBAC 权限矩阵和跨租户越权测试。
- JWT 重放、refresh token 轮换、设备吊销和限流测试。
- OpenAPI/AsyncAPI contract test。

运行结果门禁：

- Docker Compose 一条命令启动 PostgreSQL、Redis、NATS、MinIO、control-plane、worker。
- health/readiness 可用，迁移可重复执行。
- 两个 workspace 的数据相互不可见。
- Agent 可绑定、心跳、接收命令并 ACK；命令重复投递不重复执行。

### Phase 3：Desktop Agent 与 Wails/Vue3 Client

目标：把本地运行时从 Wails 单体中抽出，并交付新商业客户端基础页面。

拟修改/新增：

- 新增 <code>agent/</code>：application、runtime、cloudclient、journal、security、updater。
- 从现有 backend 抽出 Browser/Proxy/CDP/Automation adapters；先保持行为一致。
- 重建 <code>desktop-client/</code> 为 Wails + Vue3 + TypeScript。
- 页面：Login、Dashboard、Workspace、Browser Manager、Settings。
- React 客户端保留兼容入口和功能回归套件。

数据库变化：

- 本地 Agent SQLite 新增 operations、cloud_resources、sync_cursors、device_identity、command_results。
- Cloud 新增 agent_versions、device_capabilities、client_releases。

API：

- Agent bootstrap/config、device capability、release/update。
- instance create/update/start/stop/clone/delete command。
- 在线/离线状态与 observed state。

测试：

- Runtime ports 的 fake adapter 单测。
- 现有启动/停止/Profile/代理回归。
- Agent 离线队列、断线重连、命令去重和崩溃恢复。
- Vue component、路由、表单、权限可见性和 E2E。
- Windows/Linux/macOS smoke test。

运行结果门禁：

- 新客户端能登录、切换 workspace、显示实例并通过 Agent 启停本机 Chromium。
- 断网期间保留本地只读与安全队列；恢复后状态收敛。
- 现有代理连接栈和本地 Launch API 回归不下降。
- React 兼容客户端仍可构建和回滚。

### Phase 4：Instance Cloud Sync 与 Profile 版本

目标：交付安全、可恢复、可审计的 Profile 云同步。

拟修改/新增：

- <code>profile-sync-service</code>、Agent snapshot/chunk/encryption 模块。
- S3/MinIO adapter、manifest schema、revision API、冲突中心。
- 新客户端增加同步状态、版本历史、冲突解决和恢复界面。

数据库变化：

- browser_profiles、profile_revisions、profile_manifests。
- profile_objects、profile_sync_leases、profile_conflicts。
- instance_sessions、profile_restore_events。

API：

- 创建上传 session、获取 presigned chunk URL、commit revision。
- 获取 revision/manifest、恢复历史版本、解决冲突。
- WebSocket 同步进度和冲突通知。

测试：

- manifest/chunk/hash/encryption golden test。
- 大 Profile、断点续传、重复 chunk、磁盘不足、对象缺失、损坏密文。
- 同版本并发写、lease 超时和冲突解决。
- Windows/Linux/macOS 与不同 Chromium 版本恢复矩阵。
- 包含 Cookies、Storage、IndexedDB、Extensions、Bookmarks、History、Preferences 的样本验证。

运行结果门禁：

- Profile A 可在设备 1 停止、同步，在设备 2 恢复并启动。
- 失败上传不会产生可见半成品 revision。
- 冲突可见且可恢复，不静默丢失数据。
- 对象存储、数据库和日志中不出现明文秘密。

### Phase 5：Account Center、Proxy Center 与批量运营

目标：围绕跨境电商账号资产建立团队协作和批量操作。

拟修改/新增：

- <code>account-service</code> 完整 repository/service/handler。
- 平台枚举与扩展机制：Amazon、Shopify、TikTok、Facebook、Google、eBay。
- Proxy Center 云策略、Agent 执行适配和健康上报。
- Account Center、Proxy Center、批量操作页面。

数据库变化：

- accounts、account_bindings、account_secrets、account_risk_events。
- proxies、proxy_credentials、proxy_assignments、proxy_health_samples。
- batch_operations、batch_operation_items。

API：

- Account CRUD、实例/代理绑定、状态和风险事件。
- Secret 写入/轮换/受控读取。
- Proxy 导入、分组、分配、测速/健康命令。
- 批量创建/启动/停止/修改代理/绑定账号。

测试：

- Secret 加密与脱敏、二次验证、权限和审计。
- 账号/实例/代理所有权一致性。
- 大批量部分成功、重试、取消和幂等。
- 代理协议回归，严格覆盖 Xray+sing-box 和 Mihomo 两套栈。
- DNS/WebRTC 泄漏和失败降级策略验证。

运行结果门禁：

- Operator 能管理获授权账号但不能读取 secret。
- 批量任务每一项都有独立结果和重试记录。
- Cloud 不执行代理协议；Agent 仅按当前连接栈运行并上报脱敏结果。
- 账号异常、冻结、待验证状态有审计和通知。

### Phase 6：Automation、Task Scheduler、Analytics 与通知

目标：交付可视化工作流、队列化执行、Cron 调度和运营观测。

拟修改/新增：

- 正式 JSON Workflow DSL 和版本化 schema。
- Workflow Builder：打开网页、输入、点击、等待、上传、截图、执行受控 JS、关闭浏览器。
- task-service、automation-service、notification-service、analytics-service。
- Worker lease、重试/退避、死信、取消、artifact 和报告。

数据库变化：

- workflows、workflow_versions、workflow_permissions。
- schedules、tasks、task_runs、task_attempts。
- artifacts、notifications、notification_deliveries。
- metric_rollups、risk_events。

API：

- Workflow CRUD/发布/回滚。
- Cron schedule、立即执行、取消、重跑、日志和 artifacts。
- Email/WebSocket/Push 通知偏好。
- Dashboard：账号数、在线实例、代理质量、成功率、风险和登录记录。

测试：

- DSL schema、版本迁移和节点级单元测试。
- Playwright/CDP adapter contract test；Puppeteer 仅在独立 adapter 完成后启用。
- Worker 崩溃、租约过期、重复消息、超时取消和死信。
- 脚本沙箱、路径/网络权限、资源配额和恶意输入。
- Cron 时区、夏令时和漏跑补偿。

运行结果门禁：

- 每天 9 点启动 20 个 Amazon 账号的示例能调度、分片、执行、截图并生成报告。
- 重复事件不会重复产生不可逆动作。
- 任意脚本执行默认禁止；发布工作流需要权限、版本和审计。
- Dashboard 指标可追溯到任务/事件来源。

### Phase 7：商业授权、Billing、Admin 与正式发布

目标：完成 Free、Professional、Enterprise 商业化和三平台正式交付。

拟修改/新增：

- billing-service、license/entitlement、usage meter。
- 管理后台：组织、套餐、账单、设备、风险、审计、系统健康。
- 支付 webhook 与对账、通知模板、支持工具。
- 自动更新、release manifest、代码签名、公证和回滚。

数据库变化：

- plans、subscriptions、subscription_events。
- entitlements、usage_counters、usage_reservations。
- invoices、payments、webhook_events。
- license_activations、release_channels。

配额：

- 实例数量
- 团队人数
- 自动化次数/并发
- Profile 存储与流量
- 设备数量

API：

- Plan/Subscription/Usage/Entitlement。
- Billing webhook 幂等处理。
- License activate/refresh/deactivate 和离线宽限。
- Admin audit、impersonation-with-consent、risk action。

测试：

- 套餐变更、升级/降级、超额、并发预占和退款。
- webhook 重放、乱序、签名和对账。
- 授权绕过、时钟回拨、离线宽限和设备克隆。
- 备份恢复、灾难演练、负载、故障注入和安全渗透。
- 安装/升级/降级/卸载、Profile 保留和回滚。

运行结果门禁：

- Free/Professional/Enterprise 权益在 Cloud、Agent 和 UI 一致生效。
- Windows 生成签名 NSIS 安装包和便携 ZIP。
- Linux 生成 amd64/arm64 deb 与 tar.gz。
- macOS 生成签名并 notarized 的 amd64/arm64 app/zip 或 dmg。
- 发布由 CI 产生 SBOM、校验和、签名、漏洞报告和可回滚 update manifest。
- 管理后台、SaaS 平台、Desktop Agent 和 Browser Runtime 达到生产 SLO。

## 9. 部署拓扑

开发/单机验收 Docker Compose：

- PostgreSQL
- Redis
- NATS JetStream
- MinIO
- control-plane
- worker
- web/admin frontend
- migration job

生产：

- Gateway/Control Plane 无状态横向扩容。
- Worker 按队列和租户配额扩容。
- PostgreSQL 高可用、PITR 和定期恢复演练。
- Redis 不承载权威状态。
- NATS 使用持久流、消费者租约和死信流。
- 对象存储启用版本、生命周期、服务端密文和不可变备份策略。
- 全链路 metrics、structured logs、trace、audit 和 alert。

## 10. CI/CD 门禁

现有 <code>publish-linux.yml</code> 和 <code>publish-macos.yml</code> 保留并纳入统一门禁。新增：

1. gofmt、go vet、staticcheck、Go tests。
2. frontend TypeScript、lint、unit、build。
3. server unit/integration、migration、OpenAPI compatibility。
4. Agent contract、offline/reconnect 和跨平台 smoke。
5. secret、SCA、license、container、SBOM scan。
6. Profile compatibility golden suite。
7. 签名与发布只从受保护 tag 和受控 environment 执行。

任何 migration、API breaking change、权限矩阵变更和 Profile format 变更都必须有向前兼容测试与回滚说明。

## 11. 发布和回滚策略

- 所有 Cloud 新能力使用 workspace feature flag。
- Agent 先 shadow 接收命令但不执行，用于验证契约。
- Profile Sync 先只上传验证，再开启恢复；最后才允许跨设备迁移。
- 每个客户端保留上一个稳定 Runtime 和 schema reader。
- 数据库采用 expand/contract migration，不在同一发布删除旧字段。
- React 客户端在 Vue3 达到功能等价后仍保留一个发布周期。
- Cloud 不可用时允许受策略约束的本地模式；不得绕过到期授权之外的安全策略。

## 12. 关键风险与缓解

| 风险 | 缓解 |
| --- | --- |
| 12 个微服务造成交付失控 | 先模块化单体 + Worker，按 SLO 拆部署 |
| Profile 体积和 Chromium 文件锁 | 停止/静默一致性快照、chunk 去重、配额、断点续传 |
| Cookie/2FA/密码泄露 | Vault/KMS、envelope encryption、脱敏、二次验证、审计 |
| 多设备双写冲突 | lease、baseRevision、ETag、显式冲突，不静默覆盖 |
| Cloud 命令重复/乱序 | commandId、idempotencyKey、本地 journal、expectedVersion |
| CDP 被本机或网络接管 | loopback、每实例短时 token、Agent tunnel、会话和命令策略 |
| 代理连接栈被错误混用 | 将 connectorType 写入 assignment/command，Agent 强校验并拒绝跨栈降级 |
| Vue3 重写影响现有用户 | 双客户端并行、功能等价测试、一个周期回滚 |
| 三平台发布不可复现 | 固定运行时、hash/SBOM、签名、公证、CI artifact 验证 |

## 13. 最终商业级验收

功能：

- 用户、组织、Workspace、邀请、Owner/Admin/Manager/Operator/Viewer 权限。
- 浏览器实例创建、删除、复制、启停、迁移、同步。
- Profile 增量版本、冲突、恢复和跨设备。
- 指纹模板随机/固定/模板/批量生成，且有内核能力验证。
- Account Center、Proxy Center、批量运营。
- Workflow Builder、Cron、队列、报告。
- Dashboard、风险、登录日志、通知。
- Free/Professional/Enterprise 权益、计费、授权。

非功能：

- 租户隔离、敏感资产加密、审计和安全评审。
- 明确 SLO、容量、限流、备份恢复和灾难演练。
- Windows、Linux、macOS 正式安装与自动更新。
- 可观测性、SBOM、签名、漏洞管理和发布回滚。

只有同时满足功能与非功能门禁，才能宣称达到商业级紫鸟替代能力。

## 14. 每阶段交付模板

每个 Phase 的 PR/Release 必须附带：

1. 修改文件：路径、目的、兼容性。
2. 新增文件：路径、所有权、是否进入构建。
3. 数据库变化：migration 编号、forward/rollback、数据回填。
4. API 设计：OpenAPI/AsyncAPI diff、权限、幂等和错误。
5. 测试方案：新增测试、回归范围、安全和性能。
6. 运行结果：实际命令、环境、通过/失败、artifact 与已知限制。

“目录已创建”“接口已预留”或“TODO 已添加”不计为完成；只有可运行、可测试、可观测并通过退出门禁的能力才计入交付。
