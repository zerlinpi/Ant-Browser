# Ant Browser

> 面向多账号隔离、代理绑定和本地环境管理的桌面浏览器工具（Windows / Linux / macOS unsigned）。

[![Release](https://img.shields.io/github/v/release/black-ant/Ant-Browser?sort=semver)](https://github.com/black-ant/Ant-Browser/releases)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20Linux%20%7C%20macOS-blue)](https://github.com/black-ant/Ant-Browser/releases)
[![Issues](https://img.shields.io/github/issues/black-ant/Ant-Browser)](https://github.com/black-ant/Ant-Browser/issues)

## 推荐内核项目

Ant Browser 当前推荐配套使用的浏览器内核，来源于开源项目 [fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium)。

如果你正在寻找可直接下载和维护的指纹内核版本，建议先查看它的 Releases 页面：

- <https://github.com/adryfish/fingerprint-chromium/releases>

这个项目为 Ant Browser 的内核准备提供了直接可用的基础来源，这里先对原项目做明确推荐与致谢。

Ant Browser 的目标很明确：在一台桌面设备上，帮助用户稳定管理多个彼此隔离的浏览器实例，并配合代理池、浏览器内核和快捷启动能力完成日常运营或测试工作。

## 目录

- [项目简介](#项目简介)
- [近期更新](#近期更新)
- [更新日志](CHANGELOG.md)
- [核心特性](#核心特性)
- [界面预览](#界面预览)
- [快速开始](#快速开始)
- [源码启动指南（桌面端与 Cloud 云端版）](#源码启动指南)
- [常用操作](#常用操作)
- [常见问题](#常见问题)
- [Roadmap](#roadmap)
- [贡献](#贡献)
- [支持与反馈](#支持与反馈)
- [License](#license)

## 项目简介

Ant Browser 适合以下场景：

- 多账号环境隔离
- 跨境电商与社媒账号运营
- 需要独立代理出口的本地测试
- 需要统一管理浏览器内核和实例配置的团队

这个项目当前提供的核心价值是：

- 给每个账号分配独立浏览器实例
- 给每个实例绑定独立代理
- 统一管理浏览器内核、标签、关键字和快捷打开码
- 在本地保存配置和运行数据，便于自主控制

## 近期更新

### 未发布

- SOCKS5 优化：完善 SOCKS5 与链式代理连接流程，统一实例启动、测速和健康检测体验
- 体验优化：优化代理池、实例列表、日志查看和异常提示等日常操作细节
- 内存限制：新增实例最大内存配置，支持按实例控制浏览器内存占用

### 1.5.0 · 2026-07-25

- 代理增强：支持 HTTPS 代理桥接，完善代理导入、测速和连接可用性处理
- 实例安全：增加实例删除数据审计，避免误删用户数据目录时缺少确认依据
- 会话恢复：新增实例最近会话恢复配置，创建、复制、更新和启动流程保持一致
- 启动服务：补充启动 API 服务端口设置，设置页可查看当前地址并保存偏好端口
- 界面优化：精简代理池导入流程，新增代理池使用说明入口，减少页面信息堆叠

### 1.4.0 · 2026-07-15

- VPN 优化：完善代理/VPN 检测展示，可直接查看配置代理、认证状态、浏览器出口 IP、ASN/网络、多源归属冲突和来源明细，避免单一 IP 库误判
- 指纹优化：优化指纹检测页的基线、修改前后对比、自动刷新和启动逻辑；Linux / macOS 指纹环境自动切换英文，避免中文字体缺失导致方块字
- 移除控制台栏目，应用启动后直接进入实例列表，减少无用入口
- 增强观测能力：提供指纹识别页面

### 1.3.0 · 2026-06-23

- 自动化增强：完善自动化脚本导入、运行、目标实例选择和执行记录管理，提升多实例自动化编排能力
- 插件管理：新增插件包管理能力，支持插件安装、导入、启停、删除、实例限制和单实例插件配置
- VPN 优化：优化代理/VPN 连接链路，完善 Xray、sing-box、Mihomo 等连接栈的启动、测速、检测和预热能力
- 实例迁移：支持实例导入导出，可将实例配置和完整浏览器用户数据目录打包迁移到新环境
- 代理适配：实例导入时按代理名称匹配本地同名代理，匹配不到或同名不唯一时自动清空代理
- 界面优化：优化实例列表、关键字展示、操作菜单和导入导出入口，减少页面拥挤和无效信息

### 1.2.0 · 2026-05-09

- 重点升级接口调用：Launch API 补齐实例增删改查、按 code / selector 启动、runtime session / status / stop 和统一 CDP 入口，方便外部系统直接调用浏览器能力
- 完善自动化接口链路：脚本执行支持 selector / params 覆盖和 `timeoutMs` 超时控制，双实例 runtime 流程支持超时取消与错误返回
- 增强代理池：新增链式代理导入、编辑和预览能力，支持 HTTP / SOCKS5 两层链路，并优化直连代理批量导入
- 优化代理检测：新增测速目标、IP 健康检测目标和桥接启动超时配置，链式代理也可以参与测速与健康检测
- 改进实例启动：代理异常时支持本次直连启动，不修改实例原有代理配置；默认代理池只保留直连节点
- 升级书签能力：新增 IP 检测站点默认书签，支持设置启动时自动打开，并可同步到已有未运行实例

### 1.1.0 · 2026-03-19

- 完善 Linux 支持：补齐 Linux 环境下的开发、打包、安装、启动与运行链路，并持续修复安装版启动与退出稳定性问题
- 补齐 macOS unsigned 内测构建链路：支持在原生 macOS 主机上打包 `.app` / `.zip`，并将用户状态目录放到 `~/Library/Application Support/ant-browser`
- 新增 SOCKS 代理测试支持：SOCKS 代理能力已进入测试阶段，后续会继续验证稳定性与兼容性
- 实验性支持接口触发浏览器：支持通过接口启动浏览器实例，便于后续接入自动化流程

完整历史版本记录见 [CHANGELOG.md](CHANGELOG.md)。

## 源码分支说明

- `master`：面向开发者的干净基线分支，不提交 `data/app.db`、实例目录或其他用户数据。首次启动时会自动初始化空数据库。
- `user_data`：在 `master` 基础上额外提交一份 `data/app.db` 测试快照，便于演示、联调和复现问题。
- 代理运行时 `bin/xray.exe`、`bin/sing-box.exe` 已随源码仓库提供；开发和发布打包不需要再单独下载这些运行时文件。

## 核心特性

- 实例隔离管理：支持创建、编辑、启动、停止、重启、克隆和删除浏览器实例
- 代理池配置：支持统一维护代理节点，并将代理分配到具体实例
- 多协议支持：支持常见代理配置方式，并支持导入 Clash
- 内核管理：支持维护多个 Chrome 内核版本，并设置默认内核
- 快捷启动：支持通过实例 Code 和 `Ctrl + K` 快速打开目标实例
- 标签与检索：支持按标签、关键字、状态、代理、内核、分组进行筛选
- 自动化脚本：支持脚本导入、运行、目标实例选择、执行记录和外部接口调用
- 插件管理：支持插件安装、导入、启停、删除、实例限制和单实例插件配置
- 实例迁移：支持将实例配置和浏览器用户数据目录导出为 ZIP，并导入为新实例
- VPN / 代理检测：支持连接栈预热、测速、IP 健康检测和代理异常处理
- 本地化存储：配置和实例数据保存在本地，适合长期使用和备份

## 界面预览

### 1. 控制台

<img src="images/readme/001-首页.png" alt="控制台" width="100%" />

对应功能点：

- 查看实例总数、运行中实例、代理节点数量和内核版本
- 从首页快速进入 `实例列表`、`代理池配置`、`内核管理`、`系统设置`
- 查看客户端版本、运行环境、数据存储和当前实例运行状态

### 2. 实例列表

<img src="images/readme/002-实例列表.png" alt="实例列表" width="100%" />

对应功能点：

- 统一查看和管理所有浏览器实例
- 按状态、代理、内核、分组、关键字筛选实例
- 支持 `新建配置`、启动、停止、重启、配置、克隆、删除
- 给实例分配快捷打开码，后续可以直接快速启动

### 3. 代理池配置

<img src="images/readme/003-设置代理池.png" alt="代理池配置" width="100%" />

对应功能点：

- 统一管理代理节点
- 支持按协议、分组筛选代理
- 支持手动维护代理和导入 Clash
- 支持查看延迟、IP 健康并挑选可用节点

代理连接栈规则：

- `default_connector_type` 只有两套连接栈：`xray` 和 `mihomo`。
- `xray` 表示 Xray + sing-box 组合栈：Xray 负责 vmess/vless/trojan/shadowsocks/链式代理等，sing-box 负责 hysteria2/tuic/anytls 等协议。
- `mihomo` 表示独立 Mihomo 栈：需要桥接的代理统一走 mihomo。
- 实例启动、代理测速、真实连通性、IP 健康、预热和插件下载代理必须按当前连接栈执行；不得在 `xray` 组合栈和 `mihomo` 栈之间自动混用。
- 详细约束见 `docs/proxy-connector-stacks.md`。

### 4. 代理生效验证

<img src="images/readme/004-自定义代理.png" alt="代理生效验证" width="100%" />

对应功能点：

- 启动实例后访问 IP 检测网站验证代理是否真正生效
- 检查 IP 地区、ASN、运营商和风险值等信息
- 用于确认当前实例是否已经走目标代理出口

## 快速开始

### 环境要求

- 操作系统：
  - Windows 10 / 11（64 位）
  - Linux（amd64 / arm64）
  - macOS（amd64 / arm64，当前为 unsigned 内测包）
- 建议内存：8 GB 及以上
- 建议磁盘空间：2 GB 以上

### 下载与运行

1. 前往 Releases 页面下载最新版本：<https://github.com/black-ant/Ant-Browser/releases>
2. 安装版直接运行 `AntBrowser-Setup-*.exe`
3. 便携版解压后运行 `ant-chrome.exe`
4. Linux 包下载后可直接安装 `ant-browser_<version>_<arch>.deb`，或解压 `tar.gz` 后运行 `ant-chrome`
5. macOS unsigned 包解压后运行 `AntBrowser-<version>-macos-<arch>.app`；如被 Gatekeeper 拦截，请对本机测试包执行 `xattr -dr com.apple.quarantine <app路径>` 后再打开

### 从源码运行

1. 开发默认使用 `master` 分支；该分支不带测试用户数据，适合作为日常开发基线。
2. 如需带测试库的演示环境，请切换到 `user_data` 分支。
3. Windows 统一执行 `bat\dev.bat`；默认是 `stable` 静态资源模式，需要前端热更新时使用 `bat\dev.bat live`，需要受限内存复现时使用 `bat\dev.bat limited`。macOS / Linux 执行 `./dev.sh`（默认 `stable`）或 `./dev.sh live`。
4. Windows 运行时使用 `bin/xray.exe`、`bin/sing-box.exe`；Linux 运行时使用 `bin/linux-<arch>/xray`、`bin/linux-<arch>/sing-box`；macOS 运行时使用 `bin/darwin-<arch>/xray`、`bin/darwin-<arch>/sing-box`。
5. 运行时文件采用“仓库固定 + 哈希校验”，校验清单在 `publish/runtime-manifest.json`，固定来源清单在 `publish/runtime-sources.json`。
6. 如需刷新 Linux / macOS 运行时，执行 `python3 tools/runtime/sync-runtime.py --target <target>`（会按固定来源下载、校验归档并更新 manifest）。

开发模式说明：

- `bat\dev.bat`：默认 `stable` 模式，先构建 `frontend/dist`，再以静态资源模式启动 Wails，不依赖外部 Vite dev server
- `bat\dev.bat stable`：显式指定 `stable` 模式，效果与默认一致
- `bat\dev.bat live`：启动 Vite watcher，并通过 `-frontenddevserverurl` 接入桌面壳，支持热更新
- `bat\dev.bat limited`：在 `live` 基础上为 watcher 与其子进程附加 Windows Job Object 内存限制
- 如需为依赖下载配置代理，可在启动前设置 `DEV_PROXY_URL`、`DEV_NO_PROXY`、`DEV_GOPROXY`

### 自动化脚本包

自动化脚本现在分成两层：

- 仓库里的可提交 demo 脚本库：`backend/internal/automation/demo-library/`
- 本地运行时 / 用户自定义脚本：`data/automation/scripts/`

规则是：

- 只有 demo 脚本库里的脚本会提交到 git
- `data/automation/scripts/` 下的运行时脚本统一忽略，不提交 git
- 默认只同步三个 demo：`dual-instance-runtime-switch`、`news-query-txt`、`web-image-generate-download`

脚本包采用“一脚本一目录”的可搬运结构：

```text
<script-id>/
├── automation.script.json
├── index.cjs
└── 其他辅助文件
```

其中：

- `automation.script.json`：脚本元数据和默认参数
- `index.cjs`：入口脚本，`entryFile` 也可以改成相对路径，例如 `scripts/index.cjs`
- 其他辅助文件：脚本依赖的本地模块、模板、静态资源

运行时落盘结构和分发结构不同。应用内部会把脚本写到：

```text
data/automation/scripts/<script-id>/
├── config
├── index.cjs
└── 其他辅助文件
```

这里的 `config` 是应用内部持久化格式；对外复制、导入、脚本库管理一律使用 `automation.script.json` 包结构。

### Windows 发布打包（源码）

Windows 发布脚本默认保持原有 NSIS 安装包行为，也可以生成便携 ZIP，或一次生成两种产物：

```powershell
bat\publish.bat zip
bat\publish.bat both
bat\publish.bat -Target WINDOWS -WindowsFormat INSTALLER
bat\publish.bat -Target WINDOWS -WindowsFormat PORTABLE
bat\publish.bat -Target WINDOWS -WindowsFormat BOTH
```

省略 `-WindowsFormat` 时等同于 `INSTALLER`。`zip` 快捷命令只生成便携 ZIP，`both` 快捷命令同时生成安装包和便携 ZIP。安装包和便携 ZIP 输出到 `publish\output\`。

### Linux 发布打包（源码）

Linux 发布脚本位于 `publish/linux/`。

```bash
bash publish/linux/publish-linux.sh --arch amd64
bash publish/linux/publish-linux.sh --arch arm64
```

详细说明见 [publish/linux/README.md](publish/linux/README.md)。

### macOS unsigned 发布打包（源码）

macOS 发布脚本位于 `publish/mac/`，必须在原生 macOS 主机上执行，且目标架构需与主机架构一致。

```bash
bash publish/mac/publish-mac.sh --arch amd64
bash publish/mac/publish-mac.sh --arch arm64
```

脚本会生成 unsigned `.app` 和 `.zip`，适合 PR 验证与内部测试。详细说明见 [publish/mac/README.md](publish/mac/README.md)。

### 准备浏览器内核

代理运行时已经随仓库提供，你只需要准备浏览器内核。

1. 打开应用，进入 `指纹浏览器 > 内核管理`
2. 优先使用应用内下载功能准备内核
3. 如果手动准备内核，请确保目录下存在 `chrome.exe`

建议目录结构：

```text
chrome/
  chrom-142/
    chrome.exe
    ...
```

### 第一次使用建议流程

1. 在 `代理池配置` 中先导入或新增可用代理节点
2. 在 `实例列表` 中点击 `新建配置`
3. 选择实例名称、内核、代理、标签和需要的启动参数
4. 返回实例列表，点击启动按钮运行实例
5. 打开 IP 检测网站，确认代理结果是否符合预期

## 源码启动指南

仓库包含单机桌面版和 Cloud 云端版两条产品线，各组件可以按需单独启动：

| 组件 | 目录 | 说明 |
| --- | --- | --- |
| 经典桌面客户端 | `main.go`、`backend/`、`frontend/` | Wails + React 单机版，即上文介绍的桌面程序 |
| Cloud 控制面 | `server/` | Go 实现的 API 网关（`cmd/control-plane`）、后台 Worker（`cmd/worker`）和迁移工具（`cmd/migrate`） |
| Cloud 控制台 | `desktop/vue3-ui/` | Vue 3 控制台；同一份构建既用于 Web 部署，也嵌入 Cloud 桌面壳 |
| Cloud 桌面壳 | `desktop/wails-client/` | 嵌入 Vue 控制台的 Wails 客户端 |
| 桌面 Agent | `desktop/browser-agent/`、`backend/internal/cloudagent/` | 内置于桌面客户端，执行云端下发的实例启停、迁移和工作流 |

### 环境要求

- Go 1.25 或更高版本（`server/go.mod` 要求 1.25，同一版本也能构建根模块）
- Node.js 22 与 npm（与 CI 一致）
- Docker：PostgreSQL 模式、Compose 全栈和数据库集成测试需要
- Wails CLI v2.12（仅桌面客户端需要）：`go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0`

下文命令以 Windows PowerShell 为例。macOS / Linux 把 `$env:NAME = "value"` 换成 `export NAME="value"`，把行尾续行符 `` ` `` 换成 `\`。

### 1. 经典桌面客户端

按上文[从源码运行](#从源码运行)操作：Windows 执行 `bat\dev.bat`，macOS / Linux 执行 `./dev.sh`，需要热更新时加 `live` 参数。

### 2. Cloud 控制面：内存模式（快速体验）

不依赖数据库和其他服务：

```powershell
cd server
$env:ANT_HTTP_ADDRESS = "127.0.0.1:8080"   # 不设置时监听所有网卡的 :8080
go run ./cmd/control-plane
```

- 访问 `http://127.0.0.1:8080/healthz` 和 `http://127.0.0.1:8080/readyz` 确认服务可用。
- `ANT_ENV` 默认为 `development`：数据只保存在内存中，进程退出即丢失；JWT 密钥和加密主密钥使用内置的开发值，不能用于生产。

### 3. Cloud 控制面：本地 PostgreSQL 模式

数据持久化，并和生产环境一样使用受行级安全（RLS）约束的运行时角色。示例密码只用于本机，请自行替换。

1. 启动 PostgreSQL。`ant_migration` 是表的 owner，只用来执行迁移：

   ```powershell
   docker run -d --name ant-browser-pg -p 127.0.0.1:5432:5432 `
     -e POSTGRES_DB=ant_browser -e POSTGRES_USER=ant_migration -e POSTGRES_PASSWORD=change-me-migration `
     -v ant-browser-pg:/var/lib/postgresql/data postgres:16-alpine
   ```

2. 执行 `database/migrations` 下的迁移（只向前、有校验和保护，可以重复执行）：

   ```powershell
   cd server
   $env:ANT_MIGRATION_DATABASE_URL = "postgres://ant_migration:change-me-migration@127.0.0.1:5432/ant_browser?sslmode=disable"
   go run ./cmd/migrate up
   ```

3. 迁移以 `NOLOGIN` 方式创建 `ant_control_plane` 和 `ant_worker`，为它们开启登录：

   ```powershell
   docker exec ant-browser-pg psql -v ON_ERROR_STOP=1 -U ant_migration -d ant_browser `
     -c "ALTER ROLE ant_control_plane WITH LOGIN PASSWORD 'change-me-control-plane';" `
     -c "ALTER ROLE ant_worker WITH LOGIN PASSWORD 'change-me-worker';"
   ```

4. 以 `ant_control_plane` 身份启动控制面：

   ```powershell
   $env:ANT_HTTP_ADDRESS = "127.0.0.1:8080"
   $env:ANT_DATABASE_URL = "postgres://ant_control_plane:change-me-control-plane@127.0.0.1:5432/ant_browser?sslmode=disable"
   go run ./cmd/control-plane
   ```

5. 在另一个终端以 `ant_worker` 身份启动 Worker（任务队列、定时调度、通知投递）：

   ```powershell
   cd server
   $env:ANT_WORKER_DATABASE_URL = "postgres://ant_worker:change-me-worker@127.0.0.1:5432/ant_browser?sslmode=disable"
   go run ./cmd/worker
   ```

不配置 `ANT_REDIS_URL`、`ANT_NATS_URL`、`ANT_OBJECT_STORE_URL` 时，实时推送和任务唤醒只在各自进程内生效（Worker 仍会轮询 PostgreSQL 执行任务），云端 Profile 的文件内容也不会保存。需要完整链路时使用下面的 Compose 全栈。全部环境变量见 [server/README.md](server/README.md)。

### 4. Cloud 全栈：Docker Compose

一次启动 PostgreSQL、Redis、NATS、MinIO、数据库迁移、控制面、Worker、Vue 控制台和 Nginx：

```sh
cp deploy/.env.example deploy/.env   # 按文件内注释替换所有 replace-with-... 值
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up --build
```

- 控制台和 API 统一通过 `http://localhost:8088` 访问（端口由 `HTTP_PORT` 决定），`/readyz` 返回 200 即就绪。
- 停止：`docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml down`。
- 密钥生成方法、复用数据卷时的角色授权和代理健康探测见 [docs/cloud-deployment.md](docs/cloud-deployment.md)。

### 5. Vue 3 控制台（开发模式）

先按第 2 或第 3 步启动控制面，再执行：

```powershell
cd desktop/vue3-ui
npm ci
npm run dev -- --host 127.0.0.1 --port 4173 --strictPort
```

- 打开 `http://127.0.0.1:4173`，注册账号，在「团队空间」新建工作空间后即可使用。登录页的「连接设置」可以修改 Cloud API 地址，默认是 `http://127.0.0.1:8080`，也可以通过 `VITE_API_BASE_URL` 指定。
- 开发环境的控制面只允许 `http://127.0.0.1:4173`、`http://localhost:4173` 和 `http://wails.localhost` 跨域访问。换端口时需要给控制面设置 `ANT_ALLOWED_ORIGINS`（逗号分隔的完整 Origin）。
- `npm run build` 先执行 `vue-tsc` 类型检查，产物输出到 `desktop/wails-client/frontend/dist`，供 Cloud 桌面壳和前端镜像使用。

### 6. Cloud 桌面壳（Wails + Vue 3）

```powershell
cd desktop/vue3-ui
npm ci
npm run build        # 生成 desktop/wails-client/frontend/dist，go:embed 依赖该目录
cd ../wails-client
wails dev            # 开发模式，前端使用 Vite 热更新
wails build          # 打包，产物位于 desktop/wails-client/build/bin/
```

`frontend/dist` 和 `desktop/wails-client/frontend/dist` 两个 `go:embed` 目录都不入库。在根目录执行 `go build ./...` 或 `go test ./...` 之前，需要先分别在 `frontend/` 和 `desktop/vue3-ui/` 下执行 `npm run build`。

### 7. 桌面 Agent 接入 Cloud

Agent 默认关闭，经典桌面客户端和 Cloud 桌面壳都可以通过环境变量启用：

1. 在控制台的「设备管理」中注册设备，记下设备 ID 和只显示一次的设备凭据。
2. 在任意绝对路径创建配置文件，例如 `D:\ant-cloud\agent.json`：

   ```json
   {
     "baseUrl": "https://cloud.example.com",
     "deviceId": "<设备 ID>",
     "workspaceId": "<工作空间 ID>",
     "bindings": { "<云端实例 ID>": "<本地实例 ID>" }
   }
   ```

   `baseUrl` 必须是不带路径的 HTTPS 地址，本机联调时需要在控制面前加一层 TLS 反向代理。启用云端 Profile 同步时再增加 `cloudProfiles`（云端实例 ID 到云端 Profile ID 的映射）。

3. 在同一个终端设置环境变量，然后启动桌面客户端（例如 `bat\dev.bat`）：

   ```powershell
   $env:ANT_CLOUD_COMMANDS_ENABLED = "true"
   $env:ANT_CLOUD_COMMANDS_CONFIG = "D:\ant-cloud\agent.json"
   $env:ANT_CLOUD_DEVICE_CREDENTIAL = "<设备凭据>"
   # 仅在配置了 cloudProfiles 时需要：
   $env:ANT_CLOUD_PROFILE_ENCRYPTION_KEY = "<base64 编码的 32 字节随机密钥>"
   $env:ANT_CLOUD_PROFILE_ENCRYPTION_KEY_REF = "<与控制面 ANT_ENCRYPTION_KEY_REF 相同>"
   ```

执行云端工作流还需要设置 `ANT_CLOUD_WORKFLOWS_ENABLED=true` 和 `ANT_CLOUD_WORKFLOWS_CONFIG`（可以指向同一个配置文件），并要求本地自动化运行时已安装、Launch API 已开启密钥认证。设备凭据只通过环境变量传入，不要写进配置文件或提交到仓库。

### 8. 运行测试

```powershell
# Cloud 控制面：单元测试与 HTTP 接口测试
cd server
go test ./...

# PostgreSQL 集成测试：只能连接一次性测试库（测试会修改运行时角色的密码）
docker run --rm -d --name ant-browser-pg-test -p 127.0.0.1:55432:5432 `
  -e POSTGRES_DB=ant_browser_test -e POSTGRES_USER=ant_browser -e POSTGRES_PASSWORD=integration-test-only postgres:16-alpine
# 等待数据库就绪（约几秒）后执行
$env:ANT_TEST_DATABASE_URL = "postgres://ant_browser:integration-test-only@127.0.0.1:55432/ant_browser_test?sslmode=disable"
go test ./platform/postgres/...
docker stop ant-browser-pg-test

# 桌面端根模块（需要先构建两套前端，见第 6 步）
cd ..
go test ./...
```

前端分别在 `frontend/` 和 `desktop/vue3-ui/` 下执行 `npm run build`，两者都包含 TypeScript 类型检查。

## 常用操作

| 目标 | 入口 | 说明 |
| --- | --- | --- |
| 新建浏览器实例 | `实例列表 > 新建配置` | 创建一个新的独立浏览器环境 |
| 配置代理池 | `代理池配置` | 维护代理节点并检查延迟、健康状态 |
| 绑定实例代理 | `实例编辑页` | 给指定实例分配目标代理节点 |
| 启动实例 | `实例列表` | 单击启动按钮即可运行目标实例 |
| 快速打开实例 | `Ctrl + K` | 可按 Code、实例名、标签、关键字快速检索 |
| 管理浏览器内核 | `内核管理` | 新增、编辑、删除和设置默认内核 |
| 验证代理结果 | 启动实例后访问 IP 检测网站 | 核对 IP、地区、ASN、风险值 |

## 常见问题

### 1. 应用无法启动怎么办？

先检查浏览器内核路径是否有效，并确认目标目录下存在 `chrome.exe`。

### 2. 实例启动了但代理没有生效怎么办？

先检查代理节点本身是否可用，再确认该实例已经正确绑定代理。建议启动后访问 IP 检测网站复核当前出口。

如果代理池里本地客户端可用节点很多，但 Ant Browser 中“只展示可用”数量明显偏少，先确认当前 `default_connector_type` 是否与本地客户端一致。Ant Browser 不会在 `xray` 组合栈和 `mihomo` 栈之间自动混用；切换连接栈后需要重新测速。

### 3. 实例太多，怎么快速找到目标实例？

可以在 `实例列表` 中按状态、代理、内核、分组、关键字筛选，也可以通过 `Ctrl + K` 使用实例 Code 或名称快速启动。

### 4. 多个账号怎么避免串号？

建议采用一账号一实例、一实例一稳定代理的方式，不要混用浏览器环境，也不要频繁切换同一实例的出口 IP。

## Roadmap

- 完善自动化模块能力
- 持续补充使用文档和接口说明
- 增强实例模板、批量管理和检索体验

## 贡献

欢迎通过 Issue 和 Pull Request 参与改进。

- Bug 反馈：请附带版本号、系统版本、复现步骤和截图
- 功能建议：请说明业务场景、预期行为和现有问题
- 文档优化：欢迎直接提交 README、教程和截图说明相关改进

如果是较大改动，建议先开 Issue 对齐需求再提交 PR。

## 支持与反馈

- Releases：<https://github.com/black-ant/Ant-Browser/releases>
- Issues：<https://github.com/black-ant/Ant-Browser/issues>
- 感谢以下社区的支持：<https://linux.do/>

## License

当前仓库暂未附带独立的 `LICENSE` 文件，后续会补充。
