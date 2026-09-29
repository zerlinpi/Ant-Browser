# Ant Browser Cloud 控制面

`server/` 是独立的 Go 模块（`github.com/zerlinpi/Ant-Browser/server`），按“模块化单体 + Worker”方式部署：一个无状态的 API 进程和可以横向扩展的后台 Worker，共用 PostgreSQL。

启动方式（内存模式、本地 PostgreSQL、Docker Compose 全栈）见根目录 [README 的源码启动指南](../README.md#源码启动指南)。

## 目录

| 路径 | 说明 |
| --- | --- |
| `cmd/control-plane` | HTTP API 与 WebSocket 网关：认证与会话、工作空间与成员、设备、浏览器实例与命令、指纹模板、云端 Profile、代理、账号、工作流、定时计划、任务、通知、数据分析、计费授权、平台管理、批量操作 |
| `cmd/worker` | 任务队列、定时调度与通知投递；配置探测程序后执行代理健康检查 |
| `cmd/migrate` | 执行仓库根目录 `database/migrations` 下的只向前迁移 |
| `cmd/healthcheck` | 容器镜像使用的就绪探针 |
| `services/` | 各业务服务，服务层负责权限校验 |
| `platform/postgres` | PostgreSQL 仓储实现，请求携带租户上下文，配合行级安全（RLS） |
| `platform/memory` | 内存仓储，用于本地体验和测试 |
| `platform/` 其他目录 | 配置、HTTP 工具、Redis、NATS、对象存储、信封加密、SMTP、令牌与密码 |

数据库结构以仓库根目录的 `database/migrations` 为准。

## 常用命令

```powershell
go run ./cmd/control-plane    # 未设置 ANT_DATABASE_URL 时使用内存模式
go run ./cmd/migrate up       # 需要 ANT_MIGRATION_DATABASE_URL
go run ./cmd/worker           # 需要 ANT_WORKER_DATABASE_URL
go test ./...                 # 设置 ANT_TEST_DATABASE_URL 后同时运行 PostgreSQL 集成测试
```

## 环境变量

`ANT_ENV` 为 `development`（默认）时允许内存存储和内置开发密钥；其他取值会启用全部生产检查，缺少必需项时进程拒绝启动。

### 控制面（`cmd/control-plane`）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ANT_ENV` | `development` | 运行环境 |
| `ANT_HTTP_ADDRESS` | `:8080` | 监听地址 |
| `ANT_DATABASE_URL` | 空（内存模式） | 以 `ant_control_plane` 角色连接；非开发环境必填 |
| `ANT_ALLOW_MEMORY_STORE` | `true` | 设为 `false` 时必须配置数据库 |
| `ANT_REDIS_URL` | 空 | 在线状态、命令与通知的跨进程分发；非开发环境必填 |
| `ANT_NATS_URL` | 空 | 任务唤醒信号；非开发环境必填 |
| `ANT_OBJECT_STORE_URL`、`ANT_OBJECT_STORE_BUCKET`、`ANT_OBJECT_STORE_ACCESS_KEY`、`ANT_OBJECT_STORE_SECRET_KEY` | 空 | S3 / MinIO，存放云端 Profile 对象；非开发环境必填 |
| `ANT_OBJECT_STORE_REGION` | `us-east-1` | 对象存储区域 |
| `ANT_OBJECT_STORE_AUTO_CREATE_BUCKET` | `false` | 启动时自动创建存储桶 |
| `ANT_JWT_SECRET` | 开发值 | 访问令牌签名密钥，至少 32 个字符 |
| `ANT_JWT_ISSUER` | `ant-browser-control-plane` | 令牌签发方 |
| `ANT_ACCESS_TOKEN_TTL` | `15m` | 访问令牌有效期，不超过 1 小时 |
| `ANT_REFRESH_TOKEN_TTL` | `720h` | 刷新令牌与会话有效期，至少 1 小时 |
| `ANT_SECRET_MASTER_KEY` | 开发值 | 信封加密主密钥，base64 编码的 32 字节 |
| `ANT_ENCRYPTION_KEY_REF` | 开发值 | 密钥标签，绑定进每个加密数据 |
| `ANT_SECRET_KEY_VERSION` | `v1` | 密钥版本 |
| `ANT_ALLOWED_ORIGINS` | 开发环境为 `http://127.0.0.1:4173,http://localhost:4173,http://wails.localhost` | 允许跨域和 WebSocket 的精确 Origin，逗号分隔；非开发环境必填 |
| `ANT_TRUSTED_PROXY_CIDRS` | 空 | 只信任来自这些地址的 `X-Forwarded-For` / `X-Real-IP` |
| `ANT_SHUTDOWN_TIMEOUT` | `15s` | 优雅退出超时 |

`ANT_SECRET_MASTER_KEY`、`ANT_ENCRYPTION_KEY_REF`、`ANT_SECRET_KEY_VERSION` 一旦投入使用就不能随意修改，否则已加密的账号、代理凭据和两步验证密钥将无法解密。

### Worker（`cmd/worker`）

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `ANT_WORKER_DATABASE_URL` | 开发环境回退到 `ANT_DATABASE_URL` | 以 `ant_worker` 角色连接 |
| `ANT_REDIS_URL`、`ANT_NATS_URL` | 空 | 与控制面相同；非开发环境必填 |
| `ANT_WORKER_ID` | 主机名 | 任务租约的持有者标识 |
| `ANT_TASK_LEASE_TTL` | `45s` | 任务租约时长（10 秒至 30 分钟） |
| `ANT_TASK_POLL_INTERVAL` | `2s` | 轮询间隔（100 毫秒至 1 分钟） |
| `ANT_TASK_PARALLELISM` | `4` | 并发数（1 至 64） |
| `ANT_SMTP_ADDRESS`、`ANT_SMTP_USERNAME`、`ANT_SMTP_PASSWORD`、`ANT_SMTP_FROM` | 空 | 可选的邮件通知 |
| `ANT_SMTP_TLS_MODE` | `starttls` | `starttls`、`tls`，`plain` 仅限开发环境 |
| `ANT_PROXY_PROBE_EXECUTABLE`、`ANT_PROXY_RUNTIME_CONFIG`、`ANT_PROXY_RUNTIME_ROOT`、`ANT_PROXY_PROBE_TARGET` | 空 | 启用代理健康检查；同时需要与控制面一致的三个加密变量 |

### 迁移（`cmd/migrate`）

| 变量 | 说明 |
| --- | --- |
| `ANT_MIGRATION_DATABASE_URL` | 以表 owner 角色连接；未设置时依次回退到 `ANT_DATABASE_URL`、`DATABASE_URL` |
| `ANT_MIGRATIONS_PATH` | 迁移目录；未设置时自动查找 `database/migrations` 或 `../database/migrations` |

生产部署、数据库角色和 Nginx 配置见 [docs/cloud-deployment.md](../docs/cloud-deployment.md)，安全边界见 [docs/security.md](../docs/security.md)。
