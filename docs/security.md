# Cloud security baseline

## Identity and tenant isolation

All API and Agent requests must authenticate before reaching a domain handler.
Access tokens are short-lived and refresh tokens are stored only as hashes,
rotated on use, and revoked by session/device. Every repository query takes an
organization/workspace scope; IDs from another tenant must behave as not found.
Use the RBAC tables and explicit permission checks, never “non-empty role” as a
permission test. Bind Agent credentials to a workspace and device, support
revocation, and require heartbeat/presence expiry.

The composite tenant foreign keys in `database/migrations/` prevent accidental
cross-workspace references for instances, profiles, accounts, proxies, tasks,
and notifications. Add PostgreSQL RLS policies after the application role and
request-scoped tenant setting are implemented; migrations must not run the
application as a superuser or give it `BYPASSRLS`.

## Secrets and sensitive artifacts

Passwords use a memory-hard password hashing policy in the auth service.
Refresh/device/lease/license tokens are compared by hash. Account passwords,
cookies, TOTP seeds, OAuth tokens, and proxy credentials are envelope-encrypted
with a per-secret random AES-256-GCM data key. The self-hosted provider wraps
that data key using `ANT_SECRET_MASTER_KEY` (32 bytes, base64 encoded), injected
through the deployment secret manager. `ANT_ENCRYPTION_KEY_REF` is a key label;
it does not connect to KMS or Vault. A managed KMS/Vault provider and historical
key-version lookup remain to be implemented. Preserve the current master key
and key version with encrypted backups; replacing them currently prevents
decryption of existing envelopes. The database stores ciphertext, nonce,
wrapped data key, algorithm, key version, and key reference. API secret lists
must return metadata only. Runtime secret reads require an explicit
permission, a purpose, optional step-up authentication, and an audit event.

Profile user-data and automation artifacts can contain active sessions and
must be encrypted in transit and at rest, scoped to a workspace object prefix,
validated against a signed manifest/hash, bounded by size/file quotas, and
served through short-lived presigned URLs. Never log plaintext secrets,
cookies, Authorization headers, or artifact contents.

## Network and runtime

Expose only the TLS edge. Keep PostgreSQL, Redis, NATS, and MinIO private;
restrict egress from workers to approved destinations; and use the connector
selection carried by each proxy assignment. `xray` means the Xray + sing-box
combination (Xray for vmess/vless/trojan/shadowsocks/chains and sing-box for
hysteria2/tuic/anytls), while `mihomo` is an independent stack. Do not silently
fall back between stacks.

Agent WebSockets require device authentication, origin/endpoint policy,
message-size limits, ping/pong deadlines, command deadlines, command IDs,
idempotency keys, expected-version checks, and replay from a persisted event
sequence. WebSocket disconnects are not command failure; the Agent journal and
server command state provide at-least-once delivery with deduplication.

## Operations and supply chain

Run containers as non-root with pinned image digests in production, generate an
SBOM, scan Go/Node/container dependencies and licenses, sign release artifacts,
and keep a vulnerability response path. Emit structured audit events for auth,
RBAC, secret access, profile restore/conflict resolution, task execution,
billing webhooks, license changes, and administrative actions. Monitor and
alert on authentication abuse, tenant-policy denials, queue lag, failed
deliveries, database errors, and unusual secret/artifact access.

The standalone repository license and third-party attribution inventory must be
resolved before commercial distribution; package metadata currently reports
`NOASSERTION` and is not a substitute for a license policy.
