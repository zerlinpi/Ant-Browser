# Ant-Browser Enterprise Cloud Server

目标：将 Ant-Browser 升级为 Cloud SaaS + Desktop Client + Browser Runtime 架构。

## Architecture

```
server
├── gateway
├── user-service
├── workspace-service
├── instance-service
├── sync-service
├── task-service
├── notification-service
├── shared
└── migrations
```

## Technology

- Go
- PostgreSQL
- Redis
- WebSocket
- JWT

## Phase 1 Services

### user-service

负责：
- 用户注册
- 登录
- JWT认证
- Refresh Token

### workspace-service

负责：
- 团队空间
- 成员管理
- RBAC权限

Roles:

- Owner
- Admin
- Manager
- Operator
- Viewer

### instance-service

负责：
- 浏览器实例
- Desktop设备绑定
- 在线状态

## Database Planning

users

```
id
email
password_hash
status
created_at
updated_at
```

workspaces

```
id
name
owner_id
created_at
```

workspace_members

```
id
workspace_id
user_id
role
```

browser_instances

```
id
workspace_id
profile_id
device_id
status
last_online
```

## API Planning

POST /api/v1/auth/login
GET /api/v1/user/profile
GET /api/v1/workspaces
GET /api/v1/instances

## Testing

- Unit tests
- API integration tests
- Desktop connection tests
