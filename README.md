# Ant Browser

> 面向多账号隔离、代理绑定、浏览器指纹、配置档案同步与自动化运营的桌面浏览器及云端控制平台。

Ant Browser 已经从早期的本地多实例浏览器管理工具，扩展为一套同时支持本地运行和云端协同的多账号运营平台。

项目当前包含两套可以并行使用的客户端：

- **经典桌面版**：基于 Wails + React，主要负责本地浏览器实例、代理、内核、插件、自动化脚本和本地配置档案管理。
- **云端协同版**：由 Go 控制平面、后台工作进程、PostgreSQL、Redis、NATS、MinIO、Vue 3 管理界面、Wails 桌面客户端和浏览器代理程序组成，提供工作空间、权限、设备、配置档案同步、指纹模板、账号中心、代理中心、工作流、定时任务、通知、分析和商业化基础能力。

当前默认开发分支为 <code>master</code>。

---

## 一、项目架构

~~~text
Ant Browser
├─ 经典桌面版
│  ├─ Wails
│  ├─ React
│  ├─ 本地数据
│  ├─ 浏览器运行时
│  ├─ 代理运行时
│  └─ 自动化 / 插件 / 启动接口
│
└─ 云端协同平台
   ├─ 控制平面（Go）
   ├─ 后台工作进程（Go）
   ├─ PostgreSQL + 行级安全
   ├─ Redis
   ├─ NATS
   ├─ MinIO / S3 兼容对象存储
   ├─ Vue 3 管理界面
   ├─ Wails 云端桌面客户端
   └─ 浏览器代理程序
      ├─ 云端命令
      ├─ 指纹运行时
      ├─ 配置档案同步
      └─ 工作流运行时
~~~

云端负责身份、工作空间、权限、资源状态、任务、配置档案版本、通知和审计；桌面浏览器代理程序负责本机 Chromium、配置档案文件、代理运行时、CDP 和具体执行。

详细架构说明见 [架构文档](docs/architecture.md)。

---

## 二、当前已实现功能

### 1. 浏览器实例管理

经典桌面版已经支持：

- 创建、编辑、启动、停止、重启、克隆和删除浏览器实例
- 每个实例使用独立浏览器用户目录
- 多实例隔离运行
- 浏览器内核版本管理
- 默认内核设置
- 标签、关键字、状态、代理、内核、分组筛选
- 实例快捷编码与快速启动
- 最大内存等运行参数
- 最近会话恢复
- 实例 ZIP 导入和导出
- 完整浏览器用户数据迁移
- 启动接口：实例增删改查、按编码或选择器启动、状态查询、停止和运行时会话
- 统一 CDP 入口，可供外部自动化程序调用

云端协同版在此基础上增加：

- 浏览器管理中心
- 期望状态与实际状态同步
- 设备分配
- 云端命令下发
- 浏览器代理程序执行结果回传
- 实例状态回传
- 命令审计
- 工作空间级资源隔离

### 2. 代理与网络

支持统一代理池和实例级代理绑定。

支持的代理类型包括：

- HTTP / HTTPS
- SOCKS5
- VMess
- VLESS
- Trojan
- Shadowsocks
- Hysteria2
- TUIC
- AnyTLS
- Mihomo
- Clash 配置导入

同时支持：

- 两层链式代理
- 代理测速
- 出口 IP 检测
- 地区检测
- ASN 和网络来源检测
- 代理健康检查
- 代理健康历史
- 云端代理中心
- 代理与浏览器实例绑定
- 代理与账号绑定
- 代理与配置档案绑定

当前连接栈严格分为两类：

- **Xray + sing-box 组合连接栈**
- **Mihomo 独立连接栈**

系统不会在这两套连接栈之间自动降级或混用。

详细说明见：

- [代理连接栈说明](docs/proxy-connector-stacks.md)
- [代理健康检查工作进程](docs/proxy-health-worker.md)

### 3. 指纹模板与指纹运行时

云端已经实现独立的指纹模板服务和桌面指纹运行时。

支持：

- 固定种子模式
- 随机种子模式
- 自定义模式
- 单个模板创建
- 批量模板创建
- 浏览器主版本
- 操作系统平台
- 语言地区
- IANA 时区
- 浏览器窗口尺寸
- 屏幕尺寸
- CPU 并发核心数
- 设备内存
- 色深
- 最大触控点
- 禁止跟踪状态
- 设备缩放比例
- WebRTC 策略
- Canvas 噪声
- Audio 噪声
- Client Rects 噪声
- 字体列表
- WebGL 厂商
- WebGL 渲染器
- 媒体设备
- 电池信息
- Amazon 美国站、TikTok 美国站、Facebook 欧洲等示例预设

指纹运行时由两部分组成：

1. Chromium 启动参数，用于语言、时区、窗口尺寸、硬件并发、WebRTC、Canvas 和 Client Rects 等配置。
2. 桌面端动态生成的 Manifest V3 主页面运行环境扩展，在页面脚本开始执行时应用需要覆盖的浏览器属性。

生成的指纹扩展使用内容哈希目录保存，避免浏览器运行过程中读取到未完成更新的扩展文件。

### 4. 浏览器配置档案云同步

云端配置档案系统已经具备：

- 配置档案管理
- 配置档案版本
- 清单和对象元数据
- 加密存储
- MinIO / S3 兼容对象存储
- 增量同步
- 文件分块
- 哈希校验
- 存储配额记账
- 同步租约
- 冲突检测
- 保留本地版本
- 保留远端版本
- 配置档案历史版本
- 历史版本恢复
- 桌面同步运行时
- 跨设备同步基础链路

配置档案的大文件内容不会通过 WebSocket 直接传输。WebSocket 只负责命令、状态和事件，实际文件数据通过对象存储传输。

### 5. 工作空间、团队与权限

云端已经实现工作空间和成员权限体系。

当前角色：

| 角色 | 权限定位 |
| --- | --- |
| 所有者 | 工作空间全部权限，包括计费管理 |
| 管理员 | 成员、实例、配置档案、账号、代理和自动化管理 |
| 经理 | 日常运营管理、成员邀请、资源管理、任务和审计 |
| 运营人员 | 浏览器操作、配置档案同步、任务执行和资源只读 |
| 只读成员 | 主要资源和分析数据只读 |

权限粒度覆盖：

- 工作空间
- 成员
- 浏览器实例
- 配置档案
- 指纹
- 账号
- 代理
- 工作流
- 任务
- 数据分析
- 计费
- 审计

PostgreSQL 持久化模式使用组织和工作空间维度的租户隔离，并结合行级安全策略限制跨租户访问。

### 6. 登录与账号安全

云端身份系统已经支持：

- 用户注册
- 用户登录
- 访问令牌
- 刷新令牌
- 刷新令牌轮换
- 登出
- 登录限流
- 可信反向代理下的真实客户端 IP 识别
- 登录会话列表
- 单个会话撤销
- 撤销其他会话
- TOTP 双因素认证
- TOTP 绑定
- 双因素登录挑战
- 恢复码
- 双因素认证重放保护
- 双因素认证锁定策略
- 账号安全通知

敏感数据通过信封加密等机制处理，服务端不会把可直接使用的敏感值作为普通业务字段返回。

详细说明见 [安全设计](docs/security.md)。

### 7. 设备与浏览器代理程序

云端桌面客户端和浏览器代理程序已经支持：

- 设备注册
- 设备绑定
- 设备凭据
- 心跳
- 在线状态
- 云端命令接收
- 命令确认
- 命令成功结果
- 命令失败结果
- 浏览器启动
- 浏览器停止
- 本地命令去重
- 幂等执行
- 断线重连基础能力
- 工作流运行时
- 配置档案同步运行时
- 指纹运行时

详细说明见 [云端命令协议](docs/cloud-commands.md)。

### 8. 账号中心

云端管理界面已经包含账号中心和账号详情页。

支持：

- 账号创建、查询、更新和删除
- 平台类型
- 外部账号标识
- 显示名称
- 用户名
- 邮箱
- 地区
- 配置档案绑定
- 浏览器实例绑定
- 账号状态
- 风险等级
- 风险事件
- 备注
- 扩展元数据
- 工作空间级唯一性
- 租户隔离

可用于集中管理 Amazon、Shopify、TikTok、Facebook、Google、eBay 等业务账号资产。

### 9. 自动化与工作流

项目同时保留经典桌面自动化和云端工作流两套能力。

经典桌面版支持：

- 自动化脚本包导入
- 脚本参数
- 目标实例选择
- 执行历史
- 外部接口调用
- 示例脚本库

云端版支持：

- 工作流中心
- 工作流编辑器
- 工作流版本
- 工作流发布
- 工作流归档
- 任务
- 任务运行记录
- 任务执行尝试
- 浏览器代理程序工作流运行时
- 定时执行
- 执行结果
- 通知基础链路

详细说明见 [工作流运行时](docs/workflow-runtime.md)。

### 10. 定时计划与后台任务

云端已经实现：

- Cron 表达式
- 时区
- 定时计划启用和停用
- 下一次运行时间计算
- 后台调度
- 任务领取
- 工作进程租约
- 工作进程数据库角色
- Cron 步进语义
- 工作流目标校验
- 代理健康检查任务
- 重试基础能力

管理界面已经提供独立的：

- 定时计划页面
- 任务中心页面

### 11. 通知系统

通知系统已经包含：

- 应用内通知
- WebSocket 实时通知基础能力
- 通知偏好
- 通知投递记录
- SMTP 邮件适配器
- 幂等控制
- 后台工作进程通知发布
- 账号安全通知

详细说明见 [通知系统](docs/notifications.md)。

### 12. 数据分析、计费与管理后台基础模块

服务端已经实现并接入：

- 数据分析服务
- 计费服务
- 管理后台服务
- 套餐目录
- 权益控制
- 使用量统计
- 资源额度限制
- 审计
- 管理运行时

这些模块已经具备商业化基础结构，但最终商业发布、支付、授权、自动更新、签名和正式生产运维能力仍在持续完善。

---

## 三、云端管理界面

当前 Vue 3 管理界面包含：

~~~text
登录
└─ 主界面
   ├─ 仪表盘
   ├─ 工作空间
   ├─ 浏览器
   ├─ 配置档案
   │  └─ 配置档案详情
   ├─ 账号中心
   │  └─ 账号详情
   ├─ 代理中心
   ├─ 指纹中心
   │  ├─ 新建模板
   │  └─ 编辑模板
   ├─ 自动化中心
   │  ├─ 新建工作流
   │  └─ 编辑工作流
   ├─ 定时计划
   ├─ 任务中心
   ├─ 设备
   └─ 设置
~~~

设置页包含账号安全、双因素认证和会话管理等能力。

---

## 四、主要技术栈

| 层级 | 技术 |
| --- | --- |
| 经典桌面客户端 | Wails + React + TypeScript |
| 云端管理界面 | Vue 3 + TypeScript + Vite |
| 云端桌面客户端 | Wails |
| 控制平面 | Go |
| 后台工作进程 | Go |
| 浏览器代理程序 | Go |
| 主数据库 | PostgreSQL 16 |
| 临时状态和限流 | Redis |
| 消息与任务唤醒 | NATS |
| 配置档案对象存储 | MinIO / S3 兼容存储 |
| 本地运行数据 | SQLite / 本地文件 |
| 代理运行时 | Xray + sing-box / Mihomo |
| 浏览器自动化 | CDP + 项目内运行时 |

---

## 五、仓库目录

~~~text
Ant-Browser/
├─ backend/                     # 经典桌面核心逻辑
├─ frontend/                    # 经典 React 界面
├─ desktop/
│  ├─ browser-agent/            # 云端浏览器代理程序
│  ├─ fingerprint-runtime/      # 指纹运行时
│  ├─ profile-sync/             # 配置档案加密与增量同步
│  ├─ vue3-ui/                  # Vue 3 云端管理界面
│  └─ wails-client/             # 云端 Wails 桌面客户端
├─ server/
│  ├─ cmd/
│  │  ├─ control-plane/         # 控制平面
│  │  ├─ worker/                # 后台工作进程
│  │  ├─ migrate/               # 数据库迁移
│  │  └─ healthcheck/           # 健康检查
│  ├─ platform/
│  └─ services/
├─ database/
│  ├─ migrations/
│  ├─ schemas/
│  └─ tests/
├─ deploy/
│  ├─ compose/
│  ├─ docker/
│  ├─ nginx/
│  └─ postgres/
├─ docs/
├─ skills/
├─ tools/
├─ publish/
├─ main.go
└─ UPGRADE_ROADMAP.md
~~~

---

# 六、完整部署指南

## 6.1 推荐部署方式

Windows 用户推荐使用：

~~~text
Windows 11
└─ Docker Desktop
   └─ WSL 2 后端
      └─ Ant Browser 云端服务
~~~

Docker Compose 会一次启动：

- PostgreSQL
- Redis
- NATS
- MinIO
- 数据库迁移任务
- 控制平面
- 后台工作进程
- Vue 3 管理界面
- Nginx

默认只向主机暴露 Nginx 的 HTTP 端口，PostgreSQL、Redis、NATS 和 MinIO 不直接暴露到公网。

---

## 6.2 部署前准备

需要：

- Git
- Docker Desktop 或 Docker Engine
- Docker Compose
- 至少 8 GB 内存
- 建议至少 10 GB 可用磁盘空间

Windows 推荐：

- Windows 10 / 11 64 位
- 开启 WSL 2
- Docker Desktop 使用 WSL 2 后端

确认 Docker 可用：

~~~powershell
docker version
docker compose version
~~~

Linux：

~~~bash
docker version
docker compose version
~~~

---

## 6.3 获取源码

~~~bash
git clone https://github.com/zerlinpi/Ant-Browser.git
cd Ant-Browser
~~~

如果已经存在仓库：

~~~bash
git pull origin master
~~~

---

## 6.4 创建部署环境变量文件

Windows PowerShell：

~~~powershell
Copy-Item deploy\.env.example deploy\.env
~~~

Linux / macOS / WSL：

~~~bash
cp deploy/.env.example deploy/.env
~~~

然后编辑：

~~~text
deploy/.env
~~~

不要把真实的 <code>deploy/.env</code> 提交到 Git。

---

## 6.5 生成生产密钥

推荐使用 OpenSSL。

生成普通密码和 JWT 签名密钥：

~~~bash
openssl rand -hex 32
~~~

每次执行都会生成一个新的随机值。

至少分别生成：

- PostgreSQL 迁移用户密码
- PostgreSQL 控制平面用户密码
- PostgreSQL 后台工作进程用户密码
- Redis 密码
- NATS 密码
- MinIO 密码
- JWT 签名密钥

生成信封加密主密钥：

~~~bash
openssl rand -base64 32
~~~

这个值解码后必须正好是 32 字节。

Windows 如果没有单独安装 OpenSSL，可以使用 Git Bash 或 WSL 执行上述命令。

---

## 6.6 配置 deploy/.env

生产环境保持：

~~~text
APP_ENV=production
~~~

主要配置项：

~~~text
HTTP_PORT=8088

CLOUD_NETWORK_SUBNET=172.28.0.0/16
TRUSTED_PROXY_CIDRS=172.28.0.0/16

POSTGRES_DB=ant_browser
POSTGRES_USER=ant_migration

POSTGRES_PASSWORD=填写随机密码
CONTROL_PLANE_DB_PASSWORD=填写另一个随机密码
WORKER_DB_PASSWORD=填写第三个随机密码

MIGRATION_DATABASE_URL=postgres://ant_migration:迁移密码@postgres:5432/ant_browser?sslmode=disable
CONTROL_PLANE_DATABASE_URL=postgres://ant_control_plane:控制平面密码@postgres:5432/ant_browser?sslmode=disable
WORKER_DATABASE_URL=postgres://ant_worker:后台工作进程密码@postgres:5432/ant_browser?sslmode=disable

REDIS_PASSWORD=填写随机密码
REDIS_URL=redis://:Redis密码@redis:6379/0

NATS_USER=ant-browser
NATS_PASSWORD=填写随机密码
NATS_URL=nats://ant-browser:NATS密码@nats:4222

MINIO_ROOT_USER=ant-browser-storage
MINIO_ROOT_PASSWORD=填写随机密码
S3_ENDPOINT=http://minio:9000
S3_BUCKET=ant-browser-artifacts
S3_REGION=us-east-1

JWT_SIGNING_KEY=填写至少32字符随机密钥
ENCRYPTION_KEY_REF=cloud-kms://ant-browser/profile-artifacts
SECRET_MASTER_KEY=填写base64编码的32字节密钥
SECRET_KEY_VERSION=v1

ALLOWED_ORIGINS=http://wails.localhost,http://localhost:8088
~~~

注意：

1. <code>POSTGRES_PASSWORD</code> 必须与 <code>MIGRATION_DATABASE_URL</code> 中的迁移密码一致。
2. <code>CONTROL_PLANE_DB_PASSWORD</code> 必须与 <code>CONTROL_PLANE_DATABASE_URL</code> 中的密码一致。
3. <code>WORKER_DB_PASSWORD</code> 必须与 <code>WORKER_DATABASE_URL</code> 中的密码一致。
4. <code>REDIS_PASSWORD</code> 必须与 <code>REDIS_URL</code> 中的密码一致。
5. <code>NATS_USER</code>、<code>NATS_PASSWORD</code> 必须与 <code>NATS_URL</code> 一致。
6. <code>SECRET_MASTER_KEY</code>、<code>ENCRYPTION_KEY_REF</code>、<code>SECRET_KEY_VERSION</code> 投入使用后不要随意修改，否则已经保存的加密数据可能无法解密。
7. 生产环境不要把 <code>APP_ENV</code> 改成 <code>development</code>。

---

## 6.7 启动完整云端服务

在仓库根目录执行。

Windows PowerShell：

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up -d --build
~~~

Linux / macOS / WSL：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up -d --build
~~~

第一次启动会：

1. 下载 PostgreSQL、Redis、NATS、MinIO 和 Nginx 镜像。
2. 构建控制平面镜像。
3. 构建后台工作进程镜像。
4. 构建 Vue 3 管理界面镜像。
5. 初始化 PostgreSQL。
6. 创建运行时数据库角色。
7. 执行 <code>database/migrations/</code> 下的数据库迁移。
8. 启动控制平面。
9. 启动后台工作进程。
10. 启动管理界面和 Nginx。

---

## 6.8 检查服务状态

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml ps
~~~

正常情况下主要服务应该处于：

~~~text
postgres       healthy
redis          healthy
nats           healthy
minio          healthy
migrate        exited 0
control-plane  healthy
worker         running
frontend       running
nginx          running
~~~

迁移服务正常执行完成后退出是正常行为。

---

## 6.9 健康检查

浏览器访问：

~~~text
http://localhost:8088/healthz
~~~

以及：

~~~text
http://localhost:8088/readyz
~~~

正常情况下应返回成功响应。

Windows PowerShell：

~~~powershell
Invoke-WebRequest http://localhost:8088/healthz
Invoke-WebRequest http://localhost:8088/readyz
~~~

Linux / macOS / WSL：

~~~bash
curl http://localhost:8088/healthz
curl http://localhost:8088/readyz
~~~

管理界面默认地址：

~~~text
http://localhost:8088
~~~

---

## 6.10 查看日志

查看全部服务：

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml logs -f
~~~

只查看控制平面：

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml logs -f control-plane
~~~

只查看后台工作进程：

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml logs -f worker
~~~

只查看 PostgreSQL：

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml logs -f postgres
~~~

只查看 Nginx：

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml logs -f nginx
~~~

---

## 6.11 重启服务

重启全部服务：

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml restart
~~~

只重启控制平面：

~~~powershell
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml restart control-plane
~~~

---

## 6.12 更新代码后重新部署

~~~bash
git pull origin master
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up -d --build
~~~

数据库迁移服务会在控制平面和后台工作进程启动前执行。

---

## 6.13 停止服务

停止并删除容器，但保留数据卷：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml down
~~~

之后再次执行：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up -d
~~~

原来的 PostgreSQL、Redis、NATS 和 MinIO 数据仍会保留。

---

## 6.14 完全删除本地云端数据

如果确定不再需要当前测试数据：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml down -v
~~~

这会删除：

- PostgreSQL 数据卷
- Redis 数据卷
- NATS 数据卷
- MinIO 数据卷

**此操作会永久删除本地云端数据。**

生产环境不要直接执行。

---

## 6.15 复用旧 PostgreSQL 数据卷时的角色问题

数据库运行时角色初始化脚本只会在 PostgreSQL 第一次初始化全新数据目录时自动执行。

如果：

- 复用了以前的 PostgreSQL 数据卷
- 使用外部 PostgreSQL
- 迁移后出现 <code>role "ant_control_plane" is not permitted to log in</code>

需要手动启用两个运行时角色。

先启动 PostgreSQL：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up -d postgres
~~~

执行迁移：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml run --rm --build migrate
~~~

进入 PostgreSQL：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml exec postgres psql -U ant_migration -d ant_browser
~~~

在 PostgreSQL 中执行：

~~~sql
ALTER ROLE ant_control_plane WITH LOGIN PASSWORD '这里填写 CONTROL_PLANE_DB_PASSWORD';
ALTER ROLE ant_worker WITH LOGIN PASSWORD '这里填写 WORKER_DB_PASSWORD';
~~~

然后退出：

~~~text
\q
~~~

重新启动完整服务：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up -d --build
~~~

数据库角色详细权限说明见 [PostgreSQL 角色文档](docs/postgres-roles.md)。

---

## 6.16 Docker 网络冲突

默认云端网络：

~~~text
172.28.0.0/16
~~~

如果这个网段与：

- 公司局域网
- VPN
- WSL 网络
- 其他 Docker 网络
- 宿主机路由

发生冲突，需要同时修改：

~~~text
CLOUD_NETWORK_SUBNET
TRUSTED_PROXY_CIDRS
~~~

例如：

~~~text
CLOUD_NETWORK_SUBNET=172.29.0.0/16
TRUSTED_PROXY_CIDRS=172.29.0.0/16
~~~

不要只修改其中一个。

---

## 6.17 修改默认访问端口

默认：

~~~text
HTTP_PORT=8088
~~~

如果端口被占用，例如改为：

~~~text
HTTP_PORT=8090
~~~

重新创建服务：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml up -d
~~~

访问：

~~~text
http://localhost:8090
~~~

同时应根据实际地址更新允许来源配置。

---

## 6.18 启用真实代理健康检查

基础 Compose 默认不把 Xray、sing-box 和 Mihomo 二进制程序放入后台工作进程。

需要代理健康检查时，先准备经过审核的 Linux 版本：

~~~text
xray
sing-box
mihomo
~~~

然后设置：

~~~text
PROXY_CONNECTOR_DIR=/opt/ant-browser/connectors
PROXY_PROBE_TARGET=https://api.ipify.org?format=json
~~~

使用附加配置启动：

~~~bash
docker compose --env-file deploy/.env   -f deploy/compose/docker-compose.yml   -f deploy/compose/proxy-health.yml   up -d --build
~~~

开启该功能后，代理探测程序需要和控制平面使用完全相同的：

- <code>ENCRYPTION_KEY_REF</code>
- <code>SECRET_MASTER_KEY</code>
- <code>SECRET_KEY_VERSION</code>

否则已经加密的代理凭据无法解密。

连接栈仍严格保持：

~~~text
xray  → Xray + sing-box
mihomo → Mihomo 独立连接栈
~~~

不会跨连接栈自动降级。

---

## 6.19 SMTP 邮件通知

不配置 SMTP 时：

- 应用内通知仍可工作
- WebSocket 实时通知仍可工作

需要邮件通知时设置：

~~~text
SMTP_ADDRESS=
SMTP_USERNAME=
SMTP_PASSWORD=
SMTP_FROM=
SMTP_TLS_MODE=starttls
~~~

生产环境推荐：

~~~text
SMTP_TLS_MODE=starttls
~~~

或：

~~~text
SMTP_TLS_MODE=tls
~~~

生产环境不要使用明文 SMTP。

---

## 6.20 正式 Linux 服务器部署建议

生产服务器可以继续使用当前 Docker 镜像，但推荐：

~~~text
公网
 ↓
负载均衡 / HTTPS Nginx
 ↓
Ant Browser Nginx
 ↓
控制平面
 ├─ PostgreSQL
 ├─ Redis
 ├─ NATS
 └─ S3 / MinIO
 ↓
后台工作进程
~~~

生产环境要求：

- 所有公网 HTTP 和 WebSocket 使用 HTTPS / WSS
- PostgreSQL 不直接暴露公网
- Redis 不直接暴露公网
- NATS 不直接暴露公网
- MinIO 管理端口不直接暴露公网
- 数据库密码和 JWT 密钥使用密钥管理系统
- 不把生产 <code>.env</code> 提交到 Git
- PostgreSQL 开启定期备份和时间点恢复
- 定期验证数据库恢复流程
- 对象存储开启版本控制和生命周期策略
- 对敏感对象开启服务端加密
- 控制平面可以无状态横向扩容
- 后台工作进程根据任务队列压力扩容
- 配置 CPU 和内存限制
- 使用非 root 容器
- 建立结构化日志
- 建立指标、追踪和告警
- 监控数据库连接池
- 监控任务积压
- 监控 WebSocket 连接异常
- 监控配置档案上传和下载失败

如果外层还有云负载均衡或反向代理，不要把可信代理网段扩大到整个互联网，只信任实际连接到控制平面的代理地址范围。

---

# 七、本地开发

## 7.1 经典桌面版

源码开发需要：

- Go 1.25+
- Node.js 22+
- npm
- Wails 2.12
- Windows / Linux / macOS

安装 Wails：

~~~bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
~~~

Windows：

~~~powershell
bat\dev.bat
~~~

热更新：

~~~powershell
bat\dev.bat live
~~~

受限内存开发模式：

~~~powershell
bat\dev.bat limited
~~~

Linux / macOS：

~~~bash
./dev.sh
~~~

热更新：

~~~bash
./dev.sh live
~~~

Windows 运行时使用仓库中的：

~~~text
bin/xray.exe
bin/sing-box.exe
~~~

Linux 和 macOS 对应运行时按目标架构管理，并通过 <code>publish/runtime-manifest.json</code> 进行哈希校验。

---

## 7.2 控制平面内存开发模式

如果只想快速测试接口，不需要启动 PostgreSQL：

~~~bash
cd server
go run ./cmd/control-plane
~~~

开发模式未配置数据库时使用内存仓储。

注意：

- 进程退出后数据会丢失
- 不适合生产环境
- 只用于开发和快速验证

默认健康检查：

~~~text
GET /healthz
GET /readyz
~~~

---

## 7.3 本地 PostgreSQL 开发模式

单独启动 PostgreSQL：

~~~bash
docker run -d --name ant-browser-pg   -p 127.0.0.1:5432:5432   -e POSTGRES_DB=ant_browser   -e POSTGRES_USER=ant_migration   -e POSTGRES_PASSWORD=change-me-migration   -v ant-browser-pg:/var/lib/postgresql/data   postgres:16-alpine
~~~

执行数据库迁移：

Linux / macOS / WSL：

~~~bash
cd server
export ANT_MIGRATION_DATABASE_URL="postgres://ant_migration:change-me-migration@127.0.0.1:5432/ant_browser?sslmode=disable"
go run ./cmd/migrate up
~~~

Windows PowerShell：

~~~powershell
cd server
$env:ANT_MIGRATION_DATABASE_URL = "postgres://ant_migration:change-me-migration@127.0.0.1:5432/ant_browser?sslmode=disable"
go run ./cmd/migrate up
~~~

---

# 八、浏览器内核

项目推荐配套使用：

[fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium)

本项目的指纹模板会生成对应运行参数，并由浏览器代理程序和指纹运行时应用到兼容 Chromium 内核。

经典桌面版也支持在界面中管理多个浏览器内核版本。

建议目录：

~~~text
chrome/
└─ chrom-144/
   ├─ chrome.exe
   └─ ...
~~~

---

# 九、自动化脚本包

经典桌面自动化脚本采用可搬运目录：

~~~text
<script-id>/
├─ automation.script.json
├─ index.cjs
└─ ...
~~~

仓库内示例脚本库：

~~~text
backend/internal/automation/demo-library/
~~~

用户运行时脚本：

~~~text
data/automation/scripts/
~~~

本地用户数据和运行时脚本不会作为正常源码提交。

---

# 十、安全设计

当前主要安全边界包括：

- PostgreSQL 行级安全
- 工作空间租户隔离
- 所有者 / 管理员 / 经理 / 运营人员 / 只读成员权限体系
- 访问令牌和刷新令牌生命周期
- TOTP 双因素认证
- 恢复码
- 登录会话撤销
- 登录限流
- 双因素认证限流
- 敏感值脱敏
- 信封加密
- 配置档案内容加密
- 设备凭据
- 云端命令幂等
- 配置档案冲突显式处理
- 审计事件
- 可信代理和真实客户端 IP 处理
- 代理运行时与云端控制平面隔离

生产环境不要使用开发环境中的默认 JWT、数据库、对象存储或加密密钥。

---

# 十一、构建与发布

## Windows

生成 Windows 安装版或便携版：

~~~powershell
bat\publish.bat zip
bat\publish.bat both
~~~

## Linux

AMD64：

~~~bash
bash publish/linux/publish-linux.sh --arch amd64
~~~

ARM64：

~~~bash
bash publish/linux/publish-linux.sh --arch arm64
~~~

## macOS

AMD64：

~~~bash
bash publish/mac/publish-mac.sh --arch amd64
~~~

ARM64：

~~~bash
bash publish/mac/publish-mac.sh --arch arm64
~~~

当前 macOS 发布流程仍以未签名内测构建为主，正式发行仍需要完成代码签名和 Apple 公证流程。

---

# 十二、测试与持续集成

仓库当前持续集成覆盖：

- 服务端竞态检测测试
- Go 单元测试
- Go 静态检查
- Go 格式检查
- PostgreSQL 集成测试
- 数据库迁移测试
- React 前端构建
- Vue 3 前端构建
- 根模块 Go 测试
- Windows Go 构建
- Docker Compose 配置校验
- 控制平面镜像构建
- 后台工作进程镜像构建
- 前端镜像构建
- Nginx 配置校验
- 工作流运行时检查
- Linux 发布流程
- macOS 发布流程

本地常用测试：

~~~bash
go test ./...
~~~

服务端：

~~~bash
cd server
go test -race ./...
go vet ./...
~~~

---

# 十三、常见部署问题

### 1. 8088 端口被占用

修改：

~~~text
HTTP_PORT=8090
~~~

然后重新启动。

### 2. Docker 网络与 VPN 冲突

同时修改：

~~~text
CLOUD_NETWORK_SUBNET
TRUSTED_PROXY_CIDRS
~~~

### 3. 控制平面一直无法就绪

查看：

~~~bash
docker compose --env-file deploy/.env -f deploy/compose/docker-compose.yml logs control-plane
~~~

然后检查：

- PostgreSQL 是否健康
- Redis 是否健康
- NATS 是否健康
- MinIO 是否健康
- 数据库连接字符串
- JWT 密钥
- 主加密密钥
- 允许来源配置

### 4. 数据库角色无法登录

按照“复用旧 PostgreSQL 数据卷时的角色问题”章节重新启用：

~~~text
ant_control_plane
ant_worker
~~~

### 5. 修改主加密密钥后旧数据无法读取

不要随意修改：

~~~text
SECRET_MASTER_KEY
ENCRYPTION_KEY_REF
SECRET_KEY_VERSION
~~~

这三个值参与已经保存敏感数据的解密。

### 6. 清理 Docker 后数据仍然存在

普通：

~~~bash
docker compose ... down
~~~

会保留数据卷。

只有：

~~~bash
docker compose ... down -v
~~~

才会删除持久化数据卷。

---

# 十四、项目文档

- [架构设计](docs/architecture.md)
- [接口说明](docs/api.md)
- [数据库说明](docs/database.md)
- [安全设计](docs/security.md)
- [云端命令协议](docs/cloud-commands.md)
- [工作流运行时](docs/workflow-runtime.md)
- [通知系统](docs/notifications.md)
- [PostgreSQL 运行时角色](docs/postgres-roles.md)
- [代理连接栈](docs/proxy-connector-stacks.md)
- [代理健康检查](docs/proxy-health-worker.md)
- [云端部署补充说明](docs/cloud-deployment.md)
- [商业化升级路线图](UPGRADE_ROADMAP.md)
- [版本变更记录](CHANGELOG.md)

README 已经包含完整的本地和 Docker 部署流程；其他文档主要用于记录更深入的架构、安全和实现细节。

---

# 十五、当前开发方向

后续重点包括：

- 云端和桌面端端到端稳定性
- 配置档案跨设备恢复兼容矩阵
- 不同 Chromium 版本的指纹运行时兼容验证
- 更完整的工作流节点
- 工作流执行沙箱
- 数据分析和运营观测
- 计费、授权和管理后台完整商业闭环
- 自动更新
- Windows 代码签名
- macOS 代码签名和公证
- 软件物料清单
- 漏洞扫描
- 发布门禁
- 数据库备份恢复演练
- 服务等级目标
- 灾难恢复演练

完整路线图见 [商业化升级路线图](UPGRADE_ROADMAP.md)。

---

# 十六、仓库地址

- 源代码：https://github.com/zerlinpi/Ant-Browser
- 问题反馈：https://github.com/zerlinpi/Ant-Browser/issues
- 路线图：[UPGRADE_ROADMAP.md](UPGRADE_ROADMAP.md)
- 版本记录：[CHANGELOG.md](CHANGELOG.md)

---

# 十七、致谢

Ant Browser 的浏览器内核方向参考并推荐：

- [adryfish/fingerprint-chromium](https://github.com/adryfish/fingerprint-chromium)

感谢相关开源项目和社区提供的基础能力。
