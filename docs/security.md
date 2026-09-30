# Cloud security baseline

## Identity and tenant isolation

All API and Agent requests must authenticate before reaching a domain handler.
Access tokens are short-lived and refresh tokens are stored only as hashes,
rotated on use, and revoked by session/device. Every repository query takes an
organization/workspace scope; IDs from another tenant must behave as not found.
Use the RBAC tables and explicit permission checks, never “non-empty role” as a
permission test. Bind Agent credentials to a workspace and device, support
revocation, and require heartbeat/presence expiry.

Users can enable TOTP two-factor authentication (RFC 6238). A password login
for such an account yields only a five-minute, five-attempt challenge; tokens
are issued after `/api/v1/auth/mfa/verify` accepts a current code or a
single-use recovery code. Each time step is accepted once, and five failed
second-factor checks lock verification for the user for 15 minutes across all
endpoints and replicas. TOTP secrets are envelope-encrypted and bound to the
user; recovery codes and challenge tokens are stored only as SHA-256 digests.
Enabling or removing the factor requires the password (removal also needs a
code or recovery code), and every change raises a security notification. See
[`api.md`](api.md) for the exact contract.

Device credentials follow membership. Enrolling a device requires
`instance.operate` (Operator and above), because a device credential receives
and executes browser-instance commands. Removing a member revokes that member's
devices and all of their credentials in the workspace in the same transaction
as the membership change. Rotating a device credential revokes every active
credential of the device and stores the new one in one transaction, so the old
secret stops authenticating at commit. Revocation, rotation, and member removal
also close live agent WebSockets, which are not re-authenticated after the
handshake: the local socket closes immediately, and other nodes close theirs
when their next presence refresh finds the entry removed (within about 25
seconds). Role changes do not revoke devices; a member demoted below Operator
keeps previously enrolled devices until they are revoked. When device
authentication cannot complete because of an infrastructure failure, agent
routes answer 503 `dependency_unavailable` rather than 401, so agents retry
instead of discarding a valid credential.

The composite tenant foreign keys in `database/migrations/` prevent accidental
cross-workspace references for instances, profiles, accounts, proxies, tasks,
and notifications. Add PostgreSQL RLS policies after the application role and
request-scoped tenant setting are implemented; migrations must not run the
application as a superuser or give it `BYPASSRLS`.

## Secrets and sensitive artifacts

Passwords are hashed with bcrypt (default cost) in the auth service.
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

## Abuse controls and client addresses

The gateway rate limits credential-consuming endpoints with in-process token
buckets keyed by the resolved client IP: login (10/min, burst 5), register
(5 per 10 min, burst 5), refresh (60/min, burst 30), and invitation acceptance
(10/min, burst 10). Login is also limited per normalized email (5/min, burst
5) to slow password guessing against one account from many addresses; the
email is hashed before it is used as a key. Exhausted buckets return 429
`rate_limited` with `Retry-After` in seconds. Each limiter tracks at most
10,000 keys; buckets that have fully refilled are evicted, and beyond the cap
the least recently used key is dropped (which resets that key's budget).

Known limitations: the limiters are per process, so N control-plane replicas
allow roughly N times the configured rates; exact limits across replicas need a
shared limiter such as Redis, which is not implemented. The per-email limit
means an attacker can deliberately exhaust a victim's login budget for the
duration of an attack; watch for this in authentication-abuse monitoring. The
limits are not a substitute for edge (WAF/CDN) protection against volumetric
attacks.

Client IPs are used for rate limiting, session metadata, and login security
events. By default the TCP peer address is the client, and `X-Forwarded-For`
and `X-Real-IP` are ignored because any client can set them. Set
`ANT_TRUSTED_PROXY_CIDRS` to a comma-separated list of CIDR ranges or single IP
addresses for the reverse proxies in front of the control plane; startup fails
on malformed entries or ranges that cover every address (`/0`). When the peer
is a trusted proxy, `X-Forwarded-For` is read right to left, trusted proxy
addresses are skipped, and the first untrusted address is the client; entries
further left are client-supplied and ignored. If the header yields no untrusted
address, a valid `X-Real-IP` is used, and otherwise the peer. Trusted proxies
must append the connecting address to `X-Forwarded-For` and overwrite (not pass
through) `X-Real-IP`. Only list proxies that the public cannot bypass;
otherwise a client connecting from inside a trusted range could choose its own
address.

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
