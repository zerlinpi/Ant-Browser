# Ant-Browser Enterprise Architecture

## Goal

Upgrade Ant-Browser from a single-machine fingerprint browser into a commercial multi-account cross-border operation platform.

Architecture:

```
Cloud SaaS
    |
    | WebSocket/API
    |
Desktop Client
    |
    |
Browser Runtime
    |
Chromium Fingerprint Engine
```

## Core Components

### Cloud Server

Technology:

- Go
- PostgreSQL
- Redis
- WebSocket

Services:

```
server/
├── user-service
├── workspace-service
├── instance-service
├── sync-service
├── task-service
└── notification-service
```

Responsibilities:

- Authentication
- Team workspace
- Permission management
- Device binding
- Instance synchronization

Roles:

```
Owner
Admin
Manager
Operator
Viewer
```

## Desktop Client

Technology:

- Wails
- Vue3
- TypeScript

Modules:

```
desktop/
├── auth
├── account
├── instance
├── sync
├── task
└── notification
```

## Browser Runtime

Responsibilities:

- Chromium lifecycle
- Profile loading
- Proxy binding
- Fingerprint configuration
- CDP connection

## Development Order

Phase 1:

Foundation architecture and module boundaries.

Phase 2:

Cloud services.

Phase 3:

Desktop runtime.

Phase 4:

Synchronization.

Phase 5:

Automation and analytics.

## Non Breaking Rule

Existing Ant-Browser browser functionality must remain unchanged.
Enterprise modules are added as independent layers.
