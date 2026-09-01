# Ant-Browser Enterprise Commercial Upgrade Roadmap

## Goal

Upgrade Ant-Browser into a commercial multi-account cross-border management platform.

Architecture:

```
Cloud SaaS
  Go + PostgreSQL + Redis + WebSocket

        |

Desktop Client
  Wails + Vue3 + TypeScript

        |

Browser Runtime
  Chromium Fingerprint Engine
```

## Development Order

1. Cloud foundation
- user-service
- workspace-service
- RBAC
- instance-service

2. Instance cloud management
- profile sync
- cookies
- localStorage
- indexedDB
- extensions
- bookmarks
- version control

3. Fingerprint engine
- canvas
- webgl
- audio
- fonts
- timezone
- navigator
- device hardware
- WebRTC

4. Account Center
- Amazon
- Shopify
- TikTok
- Facebook
- Google

5. Operations
- batch manager
- scheduler
- automation workflow

6. Analytics
- dashboard
- risk monitoring
- notifications

7. Desktop Client
- login
- workspace
- instances
- tasks
- accounts
- sync

## Engineering Rules

- Do not break existing browser functionality.
- Keep modules isolated.
- Each module requires tests before integration.
- Release targets: Windows, macOS, Linux.
