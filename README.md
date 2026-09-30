# Ant Browser

> 面向多账号隔离、代理绑定、浏览器指纹、Profile 同步与自动化运营的桌面浏览器 / Cloud 控制平台。

[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-blue)](https://github.com/zerlinpi/Ant-Browser)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![Node.js](https://img.shields.io/badge/Node.js-22+-339933?logo=node.js&logoColor=white)](https://nodejs.org/)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white)](https://www.docker.com/)

Ant Browser 已从早期的本地多实例浏览器管理工具扩展为两条可并行运行的产品线：

- **经典桌面版**：Wails + React，本地管理浏览器实例、代理、内核、插件、自动化脚本和 Profile 数据。
- **Cloud 版**：Go Control Plane + Worker + PostgreSQL / Redis / NATS / MinIO + Vue 3 Console + Wails Desktop + Browser Agent，提供 Workspace、RBAC、设备、Profile 云同步、指纹模板、账号/代理中心、Workflow、调度、任务、通知、分析和商业化基础能力。

当前默认开发分支为 `master`。

---

## 当前架构

```text
Ant Browser
├─ Classic Desktop
│  ├─ Wails
│  ├─ React
│  ├─ Local SQLite / data
│  ├─ Browser Runtime
│  ├─ Proxy Runtime
│  └─ Automation / Plugins / Launch API
│
└─ Cloud Platform
   ├─ Control Plane (Go)
   ├─ Worker (Go)
   ├─ PostgreSQL + RLS
   ├─ Redis
   ├─ NATS
   ├─ MinIO / Object Storage
   ├─ Vue 3 Console
   ├─ Wails Cloud Desktop
   └─ Browser Agent
      ├─ Cloud Commands
      ├─ Fingerprint Runtime
      ├─ Profile Sync
      └─ Workflow Runtime
```

Cloud 负责身份、Workspace、权限、资源状态、任务、Profile 版本和审计；桌面 Agent 负责本机 Chromium、Profile 文件、代理运行时、CDP 和具体执行。

详细架构见 [docs/architecture.md](docs/architecture.md)。

---

## 已实现功能

### 1. 浏览器实例管理

经典桌面版已经支持：

- 创建、编辑、启动、停止、重启、克隆和删除浏览器实例
- 独立浏览器用户目录与实例隔离
- 内核版本管理与默认内核设置
- 标签、关键字、状态、代理、内核、分组筛选
- 实例快捷 Code 与快速启动
- 最大内存等实例运行参数
- 最近会话恢复
- 实例 ZIP 导入 / 导出和完整用户数据迁移
- Launch API：实例 CRUD、按 Code / Selector 启动、状态、停止、Runtime Session
- 统一 CDP 入口，供外部自动化系统调用

Cloud 版在此基础上增加：

- Browser Manager
- Desired State / Observed State
- 设备分配
- Cloud Command 下发
- Agent 执行结果、状态回传和审计
- Workspace 级实例隔离

### 2. 代理与网络

支持统一代理池以及实例级绑定，包括：

- HTTP / HTTPS
- SOCKS5
- VMess / VLESS / Trojan / Shadowsocks 等由 Xray 处理的协议
- Hysteria2 / TUIC / AnyTLS 等由 sing-box 处理的协议
- Mihomo 独立连接栈
- Clash 配置导入
- 两层链式代理
- 代理测速
- IP 健康检查
- 代理/VPN 出口 IP、地区、ASN、网络来源检查
- 代理健康 Worker 与历史状态
- Cloud Proxy Center 与代理分配

连接栈保持两个明确边界：

- **Xray + sing-box 组合栈**
- **Mihomo 独立栈**

不会在两套连接栈之间自动混用。详细约束见 [docs/proxy-connector-stacks.md](docs/proxy-connector-stacks.md) 和 [docs/proxy-health-worker.md](docs/proxy-health-worker.md)。

### 3. 指纹模板与 Fingerprint Runtime

Cloud 已实现独立的指纹模板服务和桌面运行时，支持：

- `seeded` / `fixed` / `custom` 模式
- 单个模板和批量模板创建
- Browser Major / Platform
- Locale / IANA Timezone
- Window / Screen 尺寸
- Hardware Concurrency
- Device Memory
- Color Depth
- Max Touch Points
- Do Not Track
- Device Scale Factor
- WebRTC Policy
- Canvas Noise
- Audio Noise
- Client Rects Noise
- Fonts
- WebGL Vendor / Renderer
- Media Devices
- Battery 信息
- Amazon US、TikTok US、Facebook EU 等示例预设

运行时由两部分组成：

1. Chromium 启动参数，例如语言、时区、窗口尺寸、硬件并发、WebRTC、Canvas / ClientRects 等。
2. 由桌面端生成的内容寻址 **MV3 MAIN World Extension**，在 `document_start` 注入需要页面级覆盖的属性。

生成的指纹扩展采用内容哈希目录，避免运行中的 Chromium 读取到半更新文件。

### 4. Profile 云同步

Cloud Profile 已具备版本化和增量同步基础能力：

- Browser Profile 管理
- Profile Revision
- Manifest / Object 元数据
- 加密 Profile 内容
- MinIO / S3 类对象存储
- Incremental Sync
- 内容分块与哈希校验
- 存储配额记账
- 同步冲突检测
- Keep Local / Keep Remote 等冲突处理
- Profile 历史版本和恢复链路
- 桌面端 Profile Sync Runtime
- 跨设备同步基础链路

Profile 同步不会把 WebSocket 当作大文件传输通道；元数据、命令和状态通过控制面传递，文件内容进入对象存储。

### 5. Workspace、团队与权限

Cloud 已实现 Workspace 和成员权限体系。

当前角色：

| 角色 | 定位 |
| --- | --- |
| Owner | Workspace 所有权限，包括 Billing 管理 |
| Admin | 日常管理、成员、实例、Profile、账号、代理和自动化 |
| Manager | 运营管理、成员邀请、资源管理、任务和审计 |
| Operator | 实例操作、Profile 同步、任务执行和资源只读 |
| Viewer | 只读访问主要资源和 Analytics |

权限粒度覆盖：

- Workspace
- Member
- Instance
- Profile
- Fingerprint
- Account
- Proxy
- Workflow
- Task
- Analytics
- Billing
- Audit

PostgreSQL 持久化模式使用 Workspace / Organization 维度的租户隔离和 RLS 约束。

### 6. 登录与账号安全

Cloud 身份系统已经支持：

- 注册
- 登录
- Access Token / Refresh Token
- Refresh Token 轮换
- 登出
- 登录限流
- Trusted Proxy 下客户端 IP 解析
- 会话列表
- 单个会话撤销
- 撤销其他会话
- TOTP 双因素认证
- TOTP Enrollment
- MFA Login Challenge
- Recovery Codes
- MFA 重放保护
- MFA 锁定策略
- 账号安全通知

敏感数据通过 Envelope Encryption 等机制处理，服务端不会把可直接使用的秘密作为普通业务字段返回。

安全设计见 [docs/security.md](docs/security.md)。

### 7. 设备与 Browser Agent

Cloud Desktop / Browser Agent 已具备：

- 设备注册和绑定
- Device Credential
- 心跳
- 在线状态
- Cloud Command 接收
- 命令 ACK / Completed / Failed
- 实例启动、停止等设备侧执行
- 本地去重和幂等处理
- 断线重连基础能力
- Workflow Runtime
- Profile Sync Runtime
- Fingerprint Runtime

Cloud 命令设计见 [docs/cloud-commands.md](docs/cloud-commands.md)。

### 8. Account Center

Cloud Console 已包含 Account Center 和账号详情页，后端支持：

- Account CRUD
- 平台 / External Identifier
- Profile 绑定
- Browser Instance 绑定
- 状态
- Risk Level
- Risk Events
- Notes / Metadata
- Workspace 级唯一性和租户隔离

用于集中管理 Amazon、Shopify、TikTok、Facebook、Google、eBay 等业务账号资产。

### 9. Automation / Workflow

项目同时保留经典桌面自动化和 Cloud Workflow 两套能力。

经典桌面版支持：

- 自动化脚本包导入
- 脚本参数
- 目标实例选择
- 执行历史
- 外部 API 调用
- Demo Script Library

Cloud 版支持：

- Workflow Center
- Workflow Builder
- Workflow Version
- Workflow Publish / Archive
- Task
- Task Run / Attempt
- Browser Agent Workflow Runtime
- 调度执行
- 执行结果与通知基础链路

Workflow Runtime 说明见 [docs/workflow-runtime.md](docs/workflow-runtime.md)。

### 10. Scheduler / Worker

Cloud 已实现：

- Cron Expression
- Timezone
- Schedule 启用 / 停用
- `nextRunAt` 计算
- Worker 调度
- Task Claim
- Worker Runtime Role
- Vixie / ISC Cron Step 语义
- Workflow 目标校验
- Proxy Health Worker
- 重试和任务执行基础设施

Cloud Console 已提供独立的 **Schedules** 和 **Tasks** 页面。

### 11. Notification

通知系统已经包含：

- 应用内通知
- WebSocket 实时推送基础能力
- Notification Preferences
- Notification Delivery
- SMTP Email Adapter
- Idempotency
- Worker 发布通知
- 账号安全通知

详细说明见 [docs/notifications.md](docs/notifications.md)。

### 12. Analytics、Billing 与 Admin 基础模块

服务端已经存在并接入：

- Analytics Service
- Billing Service
- Admin Service
- Plan Catalog
- Entitlement
- Usage / Resource Entitlement Enforcement
- Audit
- Admin Runtime

这些模块目前属于 Cloud 商业化基础设施的一部分；最终商业发布能力仍按 [UPGRADE_ROADMAP.md](UPGRADE_ROADMAP.md) 持续完善。

---

## Cloud Console 当前页面

Vue 3 Console 当前路由包含：

```text
/login
/
├─ Dashboard
├─ Workspace
├─ Browsers
├─ Profiles
│  └─ Profile Detail
├─ Accounts
│  └─ Account Detail
├─ Proxies
├─ Fingerprints
│  ├─ New Template
│  └─ Edit Template
├─ Automation
│  ├─ New Workflow
│  └─ Edit Workflow
├─ Schedules
├─ Tasks
├─ Devices
└─ Settings
```

Settings 中包含账号安全相关能力，包括 MFA 和 Sessions。

---

## 技术栈

| 层 | 技术 |
| --- | --- |
| 经典桌面客户端 | Wails + React + TypeScript |
| Cloud Console | Vue 3 + TypeScript + Vite |
| Cloud Desktop | Wails |
| Control Plane / Worker | Go |
| Browser Agent | Go |
| 主数据库 | PostgreSQL 16 |
| 临时状态 / 实时辅助 | Redis |
| 消息与任务唤醒 | NATS |
| Profile 对象存储 | MinIO / S3 compatible |
| 本地运行数据 | SQLite / 本地文件 |
| Proxy Runtime | Xray + sing-box / Mihomo |
| Browser Automation | CDP + 项目内 Runtime |

---

## 目录结构

```text
Ant-Browser/
├─ backend/                     # 经典桌面核心逻辑
├─ frontend/                    # 经典 React UI
├─ desktop/
│  ├─ browser-agent/            # Cloud Browser Agent
│  ├─ fingerprint-runtime/      # 指纹运行时与 MV3 Extension
│  ├─ profile-sync/             # Profile 加密 / 增量同步
│  ├─ vue3-ui/                  # Cloud Vue 3 Console
│  └─ wails-client/             # Cloud Wails Desktop
├─ server/
│  ├─ cmd/
│  │  ├─ control-plane/
│  │  ├─ worker/
│  │  ├─ migrate/
│  │  └─ healthcheck/
│  ├─ platform/
│  └─ services/
├─ database/
│  ├─ migrations/
│  ├─ schemas/
│  └─ tests/
├─ deploy/
│  ├─ compose/
│  ├─ docker/
│  └─ nginx/
├─ docs/
├─ skills/
├─ tools/
├─ publish/
├─ main.go
└─ UPGRADE_ROADMAP.md
```

---

## 环境要求

### 经典桌面版

- Windows 10 / 11 64 位
- Linux amd64 / arm64
- macOS amd64 / arm64（当前发布链路仍以 unsigned 内测构建为主）
- 建议内存 8 GB+
- 建议磁盘空间 2 GB+

### 源码开发

- Go 1.25+
- Node.js 22+
- npm
- Wails CLI v2.12
- Docker（Cloud PostgreSQL / Compose / 集成测试需要）

安装 Wails：

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
```

---

## 快速开始

### 1. 运行经典桌面版

Windows：

```powershell
bat\dev.bat
```

热更新：

```powershell
bat\dev.bat live
```

受限内存开发模式：

```powershell
bat\dev.bat limited
```

Linux / macOS：

```bash
./dev.sh
```

热更新：

```bash
./dev.sh live
```

Windows 运行时使用仓库中的 `bin/xray.exe`、`bin/sing-box.exe`。Linux / macOS 对应运行时也按目标架构管理，并通过 `publish/runtime-manifest.json` 做哈希校验。

### 2. 启动 Cloud Control Plane：内存模式

不需要 PostgreSQL，适合快速体验 API：

```bash
cd server
go run ./cmd/control-plane
```

默认开发模式数据保存在内存中，进程退出后丢失，不适合生产环境。

健康检查：

```text
GET /healthz
GET /readyz
```

### 3. 启动本地 PostgreSQL Cloud 环境

启动 PostgreSQL：

```bash
docker run -d --name ant-browser-pg \
  -p 127.0.0.1:5432:5432 \
  -e POSTGRES_DB=ant_browser \
  -e POSTGRES_USER=ant_migration \
  -e POSTGRES_PASSWORD=change-me-migration \
  -v ant-browser-pg:/var/lib/postgresql/data \
  postgres:16-alpine
```

执行迁移：

```bash
cd server
export ANT_MIGRATION_DATABASE_URL="postgres://ant_migration:change-me-migration@127.0.0.1:5432/ant_browser?sslmode=disable"
go run ./cmd/migrate up
```

Windows PowerShell 请把 `export` 换成：

```powershell
$env:ANT_MIGRATION_DATABASE_URL = "..."
```

数据库角色和 RLS 说明见 [docs/postgres-roles.md](docs/postgres-roles.md)。

### 4. 一键启动完整 Cloud Stack

复制环境变量：

```bash
cp deploy/.env.example deploy/.env
```

修改 `deploy/.env` 中所有生产敏感值后启动：

```bash
docker compose   --env-file deploy/.env   -f deploy/compose/docker-compose.yml   up --build
```

Compose 会启动项目当前完整 Cloud 依赖，包括：

- PostgreSQL
- Redis
- NATS
- MinIO
- Migration
- Control Plane
- Worker
- Vue Frontend
- Nginx

部署说明见 [docs/cloud-deployment.md](docs/cloud-deployment.md)。

---

## 浏览器内核

项目推荐配套使用：

[fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium)

本项目的指纹模板会生成对应 Runtime Args，并由 Browser Agent / Fingerprint Runtime 应用到兼容的 Chromium 内核。

经典桌面版也支持在 UI 中管理多个浏览器内核版本。

建议目录：

```text
chrome/
└─ chrom-144/
   ├─ chrome.exe
   └─ ...
```

---

## 自动化脚本包

经典桌面自动化脚本采用可搬运目录：

```text
<script-id>/
├─ automation.script.json
├─ index.cjs
└─ ...
```

仓库内 Demo Library：

```text
backend/internal/automation/demo-library/
```

用户运行时脚本：

```text
data/automation/scripts/
```

运行时脚本和本地用户数据不会作为正常源码提交。

---

## API 与数据库

Cloud API、数据模型和迁移已经单独维护文档：

- [API](docs/api.md)
- [Database](docs/database.md)
- [Architecture](docs/architecture.md)
- [Security](docs/security.md)
- [Cloud Commands](docs/cloud-commands.md)
- [Workflow Runtime](docs/workflow-runtime.md)
- [Notifications](docs/notifications.md)
- [PostgreSQL Roles](docs/postgres-roles.md)
- [Cloud Deployment](docs/cloud-deployment.md)

数据库迁移的权威目录：

```text
database/migrations/
```

迁移为 forward-only，并带版本和校验保护。

---

## 安全设计

项目当前重点安全边界包括：

- PostgreSQL RLS 与 Workspace 租户隔离
- Owner / Admin / Manager / Operator / Viewer RBAC
- Access / Refresh Token 生命周期
- TOTP MFA 与 Recovery Codes
- Session Revoke
- 登录 / MFA 限流
- Sensitive Secret 脱敏
- Envelope Encryption
- Profile 内容加密
- Device Credential
- Cloud Command 幂等
- Profile Conflict 显式处理
- Audit Events
- Trusted Proxy / Client IP 处理
- Proxy Runtime 与 Cloud Control Plane 分离

生产部署不应使用开发环境中的默认 JWT、数据库、加密或对象存储密钥。

---

## 构建与发布

### Windows

项目提供 Windows 桌面构建和发布脚本，可生成安装版和 Portable ZIP。

```powershell
bat\publish.bat zip
bat\publish.bat both
```

### Linux

```bash
bash publish/linux/publish-linux.sh --arch amd64
bash publish/linux/publish-linux.sh --arch arm64
```

### macOS

```bash
bash publish/mac/publish-mac.sh --arch amd64
bash publish/mac/publish-mac.sh --arch arm64
```

当前 macOS 流程以 unsigned 内测构建为主。

---

## 测试与 CI

仓库当前 CI 覆盖：

- Server `go test -race ./...`
- `go vet`
- `gofmt`
- PostgreSQL 集成测试
- Migration 测试
- React 前端构建
- Vue 3 前端构建
- Root Go Tests
- Windows Go Build
- Docker Compose 配置校验
- Control Plane / Worker / Frontend Docker Image Build
- Nginx 配置校验
- Workflow Runtime CI
- Linux / macOS 发布工作流

本地常用测试：

```bash
go test ./...
```

Cloud Server：

```bash
cd server
go test -race ./...
go vet ./...
```

---

## 当前开发方向

项目当前已经具备从“本地指纹浏览器”向“Cloud 多账号运营平台”演进的主要骨架与大量实际功能。后续工作主要集中在：

- Cloud / Desktop 端到端稳定性
- Profile 跨设备恢复矩阵
- 指纹运行时与不同 Chromium 版本兼容验证
- 更完整的 Workflow 节点和执行沙箱
- Analytics 与运营观测
- Billing / License / Admin 商业闭环
- 自动更新
- Windows 代码签名
- macOS 签名与 Notarization
- SBOM / 漏洞扫描 / Release Gate
- 备份恢复、SLO 和灾难演练

完整商业化路线图见 [UPGRADE_ROADMAP.md](UPGRADE_ROADMAP.md)。

---

## Repository

- Source: https://github.com/zerlinpi/Ant-Browser
- Issues: https://github.com/zerlinpi/Ant-Browser/issues
- Roadmap: [UPGRADE_ROADMAP.md](UPGRADE_ROADMAP.md)
- Changelog: [CHANGELOG.md](CHANGELOG.md)

---

## Acknowledgements

Ant Browser 的浏览器内核方向参考并推荐：

- [adryfish/fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium)

感谢相关开源项目和社区提供的基础能力。
