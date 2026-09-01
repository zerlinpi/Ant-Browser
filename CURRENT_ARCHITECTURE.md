# Ant-Browser 当前架构审计

> 审计基线：<code>zerlinpi/Ant-Browser</code>，<code>master</code>，提交 <code>42e8b9a6dd882fdd5e2e634ff305052325b182fb</code>。  
> 审计日期：2026-09-01。  
> 范围：递归目录树 770 个文件、156 个目录；重点复核桌面入口、Wails 前后端边界、浏览器生命周期、Profile、Chromium/CDP、自动化、指纹、代理、数据存储、Cloud/Enterprise 骨架、测试和发布链路。

## 1. 结论摘要

当前真正可运行、彼此接通的产品是一套本地桌面单体：

- Wails v2 承载 Go 桌面进程并嵌入 React/TypeScript 前端。
- Go 后端在同一进程内管理 SQLite、本地 Profile、Chromium 进程、CDP、本地 Launch API、自动化 Node/Playwright 运行时以及代理桥接进程。
- 每个浏览器实例使用独立 Chromium user-data-dir；运行态同时保存在内存对象中，持久配置主要进入 SQLite。
- 本地 HTTP 服务仅监听 127.0.0.1，提供实例 CRUD、启动/停止、自动化和统一 CDP 反向代理。
- Windows、Linux、macOS 已有不同成熟度的打包链路；Linux/macOS 有 GitHub Actions 发布工作流，macOS 仍是 unsigned 内测链路。

仓库同时存在 <code>server/</code>、<code>desktop-client/</code>、<code>browser-runtime/</code>、<code>fingerprint-engine/</code> 和 <code>enterprise/</code>，但这些目录目前大多是 Phase-0 骨架：包含数据结构、README、SQL 或 TODO，并未接入现有主程序。它们不能被视为已完成的 Cloud SaaS、Desktop Agent、Profile Sync 或商业指纹引擎。

## 2. 完成度口径

| 状态 | 定义 |
| --- | --- |
| 已接入 | 在当前 Wails 主链路中存在调用、状态和持久化闭环 |
| 部分接入 | 主链路可用，但能力范围、隔离、安全或可观测性尚不满足 SaaS |
| 骨架 | 有目录、类型、SQL 或 README，但核心方法为空、TODO 或未被主程序引用 |
| 缺失 | 仓库中没有可工作的实现或交付链路 |

## 3. 当前架构图

~~~mermaid
flowchart LR
    U[桌面用户] --> UI[React 18 + TypeScript + Vite]
    UI -->|Wails bindings| APP[backend.App 单体门面]

    APP --> CFG[config.yaml]
    APP --> DB[(SQLite data/app.db)]
    APP --> BM[browser.Manager]
    APP --> LS[LaunchServer 127.0.0.1]
    APP --> AM[Automation Manager]
    APP --> PM[Proxy Managers]

    BM --> PDIR[本地 Profile user-data-dir]
    BM --> CH[Chromium / fingerprint-chromium]
    CH --> CDP[每实例 CDP 调试端口]
    LS -->|统一反向代理| CDP
    AM --> NODE[Node 22 + playwright-core runner]
    NODE --> LS
    PM --> XR[Xray]
    PM --> SB[sing-box]
    PM --> MH[Mihomo]
    CH --> PM

    subgraph Phase0[尚未接入主链路的企业化骨架]
        CS[server/]
        DC[desktop-client/]
        BR[browser-runtime/]
        FE[fingerprint-engine/]
        ENT[enterprise/]
    end

    CS -.无 HTTP/WS 客户端与命令协议.-> APP
    DC -.无构建入口与主程序连接.-> APP
    BR -.TODO 抽象.-> BM
    FE -.Injector TODO.-> CH
~~~

## 4. 目录与模块

| 路径 | 文件量 | 当前职责 | 状态 |
| --- | ---: | --- | --- |
| <code>backend/</code> | 353 | 桌面业务、浏览器生命周期、Profile、代理、CDP、自动化、备份、日志 | 已接入 |
| <code>frontend/</code> | 265 | React 管理界面、Wails RPC 适配、主题和桌面交互 | 已接入 |
| <code>server/</code> | 30 | Gin/PostgreSQL/Redis/JWT 的 Cloud 概念骨架和 7 份 SQL | 骨架 |
| <code>fingerprint-engine/</code> | 14 | 指纹模板类型与空 Injector | 骨架 |
| <code>desktop-client/</code> | 9 | 第二套客户端模型和静态 Dashboard | 骨架 |
| <code>browser-runtime/</code> | 7 | launcher/profile/fingerprint/CDP/session 抽象 | 骨架 |
| <code>publish/</code> | 13 | Windows/Linux/macOS 打包与代理运行时哈希清单 | 部分接入 |
| <code>.github/workflows/</code> | 2 | Linux 和 macOS 发布产物 | 部分接入 |
| <code>enterprise/</code> | 1 | 企业版概念说明 | 骨架 |

主程序入口与关键装配点：

- <code>main.go</code>：确定 appRoot、处理单实例、嵌入前端、启动 Wails、托盘和退出流程。
- <code>backend/app.go</code>：聚合 config、SQLite、browser manager、Xray/Mihomo/sing-box、LaunchServer、automation 和测速调度器。
- <code>backend/app_startup.go</code>：按顺序初始化目录、日志、数据库迁移、DAO、浏览器数据、代理、Launch API、自动化和后台调度。
- <code>backend/app_shutdown.go</code>：支持“仅退出应用并保留浏览器”与“关闭应用和运行时”两种策略。

## 5. 技术栈

| 层 | 当前技术 |
| --- | --- |
| 桌面壳 | Wails v2.12、Go 1.22 |
| 主前端 | React 18、TypeScript 5、Vite 5、React Router 6、Zustand、Tailwind CSS 3、Recharts、Lucide |
| 本地后端 | Go 单体、Wails RPC、本地 HTTP |
| 本地数据 | modernc SQLite、WAL、单连接；YAML 作为配置和历史回退 |
| 浏览器 | 外置 Chromium/fingerprint-chromium 内核，独立 user-data-dir |
| 调试与自动化 | CDP HTTP/WebSocket、Node 22、playwright-core 1.59 |
| 代理 | Xray、sing-box、Mihomo；应用内桥接、测速、健康检查 |
| Cloud 骨架 | Gin、GORM/PostgreSQL、Redis、JWT；尚未形成可运行服务 |
| 发布 | Wails/NSIS、Linux deb/tar.gz、macOS app/zip、GitHub Actions |

需要消除的技术栈漂移：

- 用户目标要求新 Desktop Client 使用 Wails + Vue3 + TypeScript；当前生产客户端是 Wails + React + TypeScript。
- <code>desktop-client/README.md</code> 同时描述 React UI 和 Tauri/Rust，但目录内没有完整 Tauri 工程。
- 根模块名为 <code>ant-chrome</code>；<code>server/go.mod</code> 是独立模块 <code>ant-browser-enterprise/server</code>，部分源码却导入 GitHub 仓库路径；<code>fingerprint-engine/service/generator.go</code> 也使用与根模块不一致的导入路径。进入开发前必须统一模块边界并用编译门禁验证。

## 6. 浏览器实例生命周期

~~~mermaid
sequenceDiagram
    participant UI as React UI / Launch API
    participant App as backend.App
    participant DB as SQLite
    participant Proxy as Proxy Runtime
    participant Chrome as Chromium
    participant CDP as DevTools

    UI->>App: Start(profileId, 临时参数)
    App->>DB: 读取 Profile/Core/Proxy/Extensions
    App->>App: 清理受管启动参数并生成启动计划
    App->>Proxy: 按当前连接栈获取或创建桥接
    App->>Chrome: exec binary + user-data-dir + debug port
    App->>CDP: 轮询 /json/version 与 /json/list
    CDP-->>App: ready 且稳定
    App-->>UI: Wails event / HTTP runtime payload
    UI->>App: Stop(profileId)
    App->>CDP: Browser.close
    alt CDP 关闭失败
        App->>Chrome: 进程树终止
    end
    App->>Proxy: 释放 Profile 桥接引用
    App-->>UI: stopped / crashed event
~~~

关键实现：

- <code>backend/app_instance_start*.go</code>：解析内核、user-data、指纹、插件、启动页、代理和调试端口，启动后等待 CDP 稳定。
- <code>backend/app_instance_debug_ready.go</code>：可从分配端口、进程输出或 <code>DevToolsActivePort</code> 恢复调试端口。
- <code>backend/app_instance_monitor.go</code>：启动器退出但 CDP 仍存活时切换为 detached 端口监控。
- <code>backend/app_instance_stop.go</code>：优先 CDP <code>Browser.close</code>，再降级到系统进程控制。
- <code>backend/browser_runtime_state.go</code>：运行态保存在内存 Profile 和 <code>exec.Cmd</code> 中，并通过 Wails event 通知前端。

现有限制：

- 运行态不是持久化 desired/observed state；应用崩溃恢复依赖本机进程与调试端口扫描。
- 启动互斥、代理引用、自动化目标游标均位于单进程内存，不能直接扩展为多设备/多 Agent。
- 缺少 commandId、幂等键、重试记录和可恢复操作日志。

## 7. Profile 存储与迁移

### 7.1 元数据

<code>backend/internal/database/sqlite.go</code> 维护 1 到 14 的本地迁移：

- <code>browser_profiles</code>：实例元数据、user-data-dir、内核、指纹参数、代理、启动参数、标签、分组、会话恢复、内存限制、回收站时间。
- <code>browser_proxies</code>：代理配置、来源、测速、健康、首选内核。
- <code>browser_cores</code>、<code>browser_bookmarks</code>、<code>browser_groups</code>。
- <code>browser_extensions</code> 与实例插件绑定。
- <code>launch_codes</code>。

SQLite 使用 WAL、foreign_keys pragma 和单连接。列表型字段主要序列化为 JSON TEXT，适合单机，但不具备服务端字段级查询、版本化校验和多租户隔离。

### 7.2 浏览器状态

- 默认 user-data 根目录为 <code>data/</code>。
- Profile 未指定目录时使用 Profile UUID 作为子目录名。
- user-data-dir 保存 Cookies、LocalStorage、SessionStorage、IndexedDB、扩展、书签、历史和 Preferences 等 Chromium 状态。
- Linux 不可写安装目录和 macOS App Bundle 会切换到用户可写状态目录；Windows 仍以安装/运行目录策略为主。

### 7.3 导入导出与删除

- <code>backend/app_profile_package_api.go</code> 导出 manifest、profiles.json 和完整 user-data 目录到 ZIP；运行中实例不可导出。
- 导入使用 staging、UUID 重映射、目录 rename 和代理名称匹配，已有 Zip Slip 防护。
- <code>backend/internal/browser/profile_delete.go</code> 默认软删除并保留 72 小时；永久删除会清理启动码、插件设置、user-data 和快照，并检查路径边界。

风险：

- Profile 包没有静态加密、签名、总解压量/文件数上限或恶意内容扫描。
- 完整 user-data 包含高敏感会话资产，不能直接作为云同步协议。
- 允许绝对 UserDataDir 会扩大读写边界；Cloud Agent 必须使用受管根目录或显式白名单。

## 8. Chromium 启动方式

当前由 Go 使用 <code>exec.Command</code> 直接拉起选定内核。系统会剔除用户输入中的受管参数，再生成：

- <code>--user-data-dir</code>
- <code>--remote-debugging-port</code>
- 代理或直连参数
- 插件加载参数
- 指纹参数
- 会话恢复和启动页
- Profile 与单次启动附加参数

启动后通过 CDP 探针确认就绪和稳定窗口；失败时区分浏览器进程提前退出、调试端口待就绪和 detached 运行。该实现是未来 Desktop Agent 最有价值的可复用资产，但需要先从 Wails App、全局锁和 UI event 中抽成可测试的 Runtime 接口。

## 9. CDP 与本地 Launch API

<code>backend/internal/launchcode/</code> 提供本地服务，默认端口 19876：

| 路径 | 能力 |
| --- | --- |
| <code>GET /api/health</code> | 本地服务健康检查 |
| <code>GET/POST /api/profiles</code> | Profile 列表与创建 |
| <code>GET/PUT/DELETE /api/profiles/{id}</code> | Profile 详情、更新、删除 |
| <code>/api/profiles/{id}/status</code>、<code>/stop</code> | 实例状态和停止 |
| <code>POST /api/launch</code>、<code>GET /api/launch/{code}</code> | 按 ID、Code、名称、标签、关键字、分组启动；支持批量匹配 |
| <code>GET /api/runtime/active</code> | 当前统一 CDP 目标 |
| <code>POST /api/runtime/session</code> | 启动并等待 CDP ready |
| <code>POST /api/runtime/status</code>、<code>/stop</code> | 运行时控制 |
| <code>/api/automation/*</code> | 脚本、运行记录与公开 Hook |
| 非 <code>/api/*</code> 根路径 | 反向代理到当前活动实例 CDP |

安全现状：

- 服务监听 127.0.0.1，并有 localhost middleware。
- API key 仅在配置 enabled 且 key 非空时生效，默认配置关闭。
- API key 只保护 <code>/api/*</code>；统一 CDP 根代理不经过该 key 检查。
- 六位 LaunchCode 是便捷选择器，不是 SaaS 凭据。

因此 Cloud 版不得公开转发当前端口。应使用每实例、短时、受工作空间授权的 Agent tunnel，并对 CDP 命令和会话寿命做控制。

## 10. 自动化能力

已接入：

- Node 22 + playwright-core 运行时按需安装和自检。
- 可导入目录、ZIP、Git 或内联脚本包。
- 支持实例选择器、目标预启动、运行超时、取消、进程终止、日志、结果和 artifact 目录。
- Launch API 可列出/运行脚本，并提供显式启用的公开 Hook。
- 仓库包含 demo library 和脚本包格式。

未接入：

- 可视化 Workflow Builder 与正式 JSON DSL。
- 云端任务队列、Cron 调度、Worker 租约、重试/死信、并发配额。
- Puppeteer 适配层。
- 多租户脚本沙箱、包签名、依赖许可策略和网络/文件最小权限。

## 11. 指纹能力

当前生产链路依赖定制 Chromium 启动参数、能力矩阵和检测页，而不是独立 <code>fingerprint-engine</code>：

- 已验证的受管维度包括品牌/版本、平台/版本、语言、时区、窗口尺寸、硬件并发、WebRTC/UDP 策略，以及 Canvas/ClientRects 噪声开关。
- 后端包含指纹能力检查、矩阵、检测页和测试。
- 指纹实际效果取决于所选 fingerprint-chromium 内核是否支持对应参数。

<code>fingerprint-engine/</code> 中 Canvas、WebGL、Audio、Fonts、Navigator、Hardware、Timezone、Language、Media 和 WebRTC 目前主要是类型定义；<code>runtime.Injector.Inject</code> 仍是 TODO，未接入启动链路。不能宣称 AudioContext、Battery、MediaDevices、Fonts、GPU 等所有维度已形成商业级一致性引擎。

## 12. 代理模块

必须保持仓库 <code>AGENTS.md</code> 的连接栈不变量：

- <code>browser.default_connector_type=xray</code> 表示 Xray + sing-box 组合栈。
- Xray 负责 vmess、vless、trojan、shadowsocks、链式代理等。
- sing-box 负责 hysteria2、tuic、anytls 等。
- <code>browser.default_connector_type=mihomo</code> 表示独立 Mihomo 栈。
- 实例启动、测速、真实连通性、IP 健康、预热和代理下载都必须使用当前选定栈，不得在两套栈间自动混用。

当前模块包含协议解析、Clash 导入、桥接进程复用、清理/恢复、测速、IP 健康、诊断、来源刷新和大量协议回归测试。未来 Cloud 只管理 desired proxy assignment、策略和脱敏健康结果；协议执行、凭据与真实连通性必须留在 Desktop Agent。

## 13. 前端与产品信息架构

当前主前端已有：

- 实例列表/详情/编辑/复制/日志。
- 代理池、内核、扩展、书签、标签。
- 自动化脚本列表和详情。
- 设置、Profile、图表和 Launch API 文档。
- 浏览器崩溃与代理桥接事件通知、Ctrl/Cmd+K 快速启动、托盘/退出流程。

现有设计系统位于 <code>frontend/src/shared/components</code>、<code>shared/theme</code> 和 <code>shared/layout</code>。Phase 3 新客户端必须复用交互语言和业务概念，但按商业平台重新组织为 Login、Dashboard、Workspace、Browser Manager、Account Center、Proxy Center、Automation、Task Center、Settings；复杂表单使用独立页或向导，不把运营表格和长表单混在同一屏。

## 14. Cloud/Enterprise 骨架的真实状态

<code>server/</code> 已有 users、workspaces、members、roles、browser_instances、devices、profile_versions/files/sync_records 和 accounts 的 SQL 草案，但：

- gateway 仅有 health、register/login 占位返回。
- JWT、repository、auth handler、instance service、sync handler 多为 TODO 或空返回。
- RBAC 的 <code>HasPermission</code> 只判断 role 非空，不构成权限模型。
- 002 与 004 都定义 <code>workspace_members</code>，迁移治理不完整。
- 数据表缺少完整外键、唯一约束、索引、租户隔离、审计和密钥管理。
- accounts 草案含 password 字段，但没有加密/Vault 边界。
- 没有 Desktop HTTP/WS client、设备注册、心跳、命令确认、离线队列或 Profile 同步 Worker。
- 没有 Docker Compose、对象存储、NATS/RabbitMQ、可运行 worker、OpenAPI/AsyncAPI 或部署清单。

结论：<code>server/</code> 是 Phase-0 模型草稿，Cloud 功能不能按“已有服务继续补完”估算，而应在保留概念的前提下重新建立工程基线和契约。

## 15. 主要问题与优先级

### P0：进入 Cloud 开发前必须解决

1. 统一仓库模块与可编译边界，消除根模块、server 模块和企业骨架导入路径漂移。
2. 明确 Cloud Control Plane 与 Desktop Agent 的所有权；禁止 Cloud 直接持有本机 PID、CDP 端口或代理运行时。
3. 重做身份、Refresh Token、OAuth、设备凭据、RBAC、租户隔离和审计。
4. Profile/账号/2FA/Cookie/代理凭据必须采用分层加密、Vault 和最小权限，禁止明文数据库设计。
5. 定义 WebSocket 命令协议、commandId、幂等、ACK、进度、重试、超时和 observed state。
6. 将完整 user-data ZIP 搬运升级为一致性快照、manifest、chunk/hash、版本和冲突协议。

### P1：可扩展性与可靠性

1. <code>backend.App</code> 高耦合，Wails、进程、存储、代理和自动化缺少 ports/adapters。
2. 运行态和锁只在内存；没有持久操作日志和崩溃后的幂等恢复。
3. SQLite 单连接和 JSON TEXT 适合单机，不适合服务端并发与查询。
4. Launch API 默认无 key，CDP 根代理未纳入 key 保护。
5. Profile ZIP 缺少加密、签名、配额与恶意内容防护。
6. CI 只有发布工作流，没有统一 lint、Go test、frontend build、server integration、迁移和安全门禁。

### P2：产品与交付一致性

1. React 主客户端、Vue3 目标、Tauri README 和第二套 desktop-client 目录互相冲突。
2. 已有企业化 README 容易把骨架描述为完成能力。
3. macOS 缺少签名/公证；Windows 公共发布签名和自动更新链路未形成统一门禁。
4. Cloud 没有 SLO、metrics、trace、告警、备份恢复或灾难演练。

## 16. 推荐改造边界

~~~mermaid
flowchart TB
    CP[Cloud Control Plane]
    API[REST API + WebSocket Gateway]
    BUS[NATS]
    PG[(PostgreSQL)]
    REDIS[(Redis)]
    OBJ[(S3/MinIO Object Storage)]
    AG[Desktop Agent]
    CACHE[(Local SQLite Cache + Operation Journal)]
    RT[Browser Runtime]
    PR[Proxy Supervisor]
    AU[Automation Runner]
    CH[Chromium]

    CP --> API
    CP --> PG
    CP --> REDIS
    CP --> BUS
    CP --> OBJ
    API <-->|短时设备会话、命令 ACK、事件| AG
    AG --> CACHE
    AG --> RT
    AG --> PR
    AG --> AU
    RT --> CH
    AU --> RT
    PR --> CH
    AG <-->|加密 manifest/chunk| OBJ
~~~

所有权原则：

- Cloud：身份、工作空间、RBAC、desired state、Profile 元数据/版本、调度、审计、计费和通知。
- Agent：OS 二进制、user-data、代理内核/凭据、Chromium PID/CDP、真实健康检查、离线执行。
- 共享契约：版本化资源、幂等命令、事件序列、短时凭据、加密 Profile artifact。

## 17. Phase 1 交付记录

| 项目 | 结果 |
| --- | --- |
| 修改文件 | 无业务代码修改 |
| 新增文件 | <code>CURRENT_ARCHITECTURE.md</code>、<code>UPGRADE_ROADMAP.md</code> |
| 数据库变化 | 无；仅审计本地 SQLite 迁移 1-14 和 Cloud SQL 草案 001-007 |
| API 设计 | 记录当前 Launch API/CDP；目标 Cloud API 与 WebSocket 契约见路线图 |
| 测试方案 | 文档结构检查、证据路径回读、GitHub 提交回读；后续阶段测试矩阵见路线图 |
| 运行结果 | 完成 master 递归扫描和关键源码复核；本阶段不改运行时代码，不宣称 Cloud/server 骨架已构建通过 |

## 18. 关键证据索引

- 桌面入口：<code>main.go</code>、<code>wails.json</code>
- 主应用装配：<code>backend/app.go</code>、<code>backend/app_startup.go</code>、<code>backend/app_shutdown.go</code>
- 本地数据库：<code>backend/internal/database/sqlite.go</code>
- Profile：<code>backend/internal/browser/profile_*.go</code>、<code>backend/app_profile_package_api.go</code>
- 生命周期：<code>backend/app_instance_*.go</code>、<code>backend/browser_runtime_state.go</code>
- CDP/API：<code>backend/app_cookie.go</code>、<code>backend/internal/launchcode/</code>
- 自动化：<code>backend/internal/automation/</code>、<code>backend/automation_script_*.go</code>
- 代理：<code>backend/internal/proxy/</code>、<code>docs/proxy-connector-stacks.md</code>
- 前端：<code>frontend/src/routes/AppRoutes.tsx</code>、<code>frontend/src/config/navigation.config.ts</code>
- Cloud 草案：<code>server/</code>
- 企业化骨架：<code>desktop-client/</code>、<code>browser-runtime/</code>、<code>fingerprint-engine/</code>
- 发布：<code>publish/</code>、<code>.github/workflows/publish-linux.yml</code>、<code>.github/workflows/publish-macos.yml</code>
