export interface User {
  id: string;
  email: string;
  displayName?: string;
  status?: string;
  createdAt?: string;
  updatedAt?: string;
}

/** authservice.TokenPair returned by /auth/login, /auth/register and /auth/refresh. */
export interface AuthTokenPair {
  accessToken: string;
  accessExpiresAt?: string;
  refreshToken: string;
  refreshExpiresAt?: string;
  sessionId?: string;
  user?: User;
}

/** Second login step issued by /auth/login for accounts with two-factor authentication. */
export interface MFAChallenge {
  /** Bearer token for POST /auth/mfa/verify; valid for five minutes and five attempts. */
  token: string;
  expiresAt: string;
  /** "totp" and "recovery_code". */
  methods: string[];
}

/**
 * authservice.LoginResult: without two-factor authentication it is a token
 * pair; otherwise it carries only the challenge.
 */
export interface LoginResult extends Partial<AuthTokenPair> {
  mfaRequired?: boolean;
  mfaChallenge?: MFAChallenge;
}

/** GET /api/v1/me/mfa */
export interface MFAStatus {
  /** False when this server cannot enroll new second factors. */
  available: boolean;
  enabled: boolean;
  enabledAt?: string;
  recoveryCodesRemaining: number;
}

/** POST /api/v1/me/mfa/totp/setup; the secret is shown once. */
export interface TOTPSetup {
  secret: string;
  otpauthUri: string;
  issuer: string;
  accountName: string;
  algorithm: string;
  digits: number;
  period: number;
}

/** Exactly one of `code` (authenticator app) or `recoveryCode`. */
export interface SecondFactorProof {
  code?: string;
  recoveryCode?: string;
}

export type WorkspaceRole = "owner" | "admin" | "manager" | "operator" | "viewer";

export interface Workspace {
  id: string;
  organizationId: string;
  name: string;
  slug: string;
  status: string;
  version: number;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
  /** The caller's role in this workspace. */
  role?: WorkspaceRole | string;
}

export interface WorkspaceMembership {
  id: string;
  workspaceId: string;
  userId: string;
  role: WorkspaceRole | string;
  status: string;
  joinedAt: string;
  updatedAt: string;
  email?: string;
  displayName?: string;
}

export interface WorkspaceInvitation {
  id: string;
  workspaceId: string;
  email: string;
  role: string;
  status: string;
  expiresAt: string;
  createdAt: string;
  revokedAt?: string;
  acceptedAt?: string;
  token?: string;
}

export type DevicePlatform = "windows" | "linux" | "darwin";

export interface AgentDevice {
  id: string;
  workspaceId: string;
  userId: string;
  name: string;
  platform: DevicePlatform | string;
  agentVersion: string;
  capabilities: Record<string, unknown> | null;
  status: string;
  lastSeenAt?: string;
  createdAt: string;
  updatedAt: string;
  revokedAt?: string;
}

/** deviceservice.Registration: the credential is returned exactly once. */
export interface DeviceRegistration {
  device: AgentDevice;
  credential: string;
}

export interface BrowserInstance {
  id: string;
  workspaceId: string;
  version: number;
  name: string;
  platform: string;
  desiredState: string;
  observedState: string;
  assignedDeviceId?: string;
  currentRevision: number;
  profileId?: string;
  proxyAssignmentId?: string;
  fingerprintTemplateId?: string;
  tags: string[] | null;
  lastSeenAt?: string;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
}

export type BrowserInstanceAction = "instance.start" | "instance.stop" | "instance.restart" | "instance.migrate";

export interface BrowserInstanceCommand {
  id: string;
  workspaceId: string;
  instanceId: string;
  deviceId?: string;
  action: BrowserInstanceAction | string;
  idempotencyKey: string;
  expectedVersion: number;
  status: string;
  payload: Record<string, unknown> | null;
  deadline: string;
  createdBy: string;
  createdAt: string;
  acknowledgedAt?: string;
  completedAt?: string;
  failureCode?: string;
  failureMessage?: string;
}

/** Response of POST .../browser-instances/{id}/commands and batch start/stop item values. */
export interface InstanceCommandResult {
  command: BrowserInstanceCommand;
  instance: BrowserInstance;
}

export interface AccountAsset {
  id: string;
  workspaceId: string;
  platform: string;
  name: string;
  identifier: string;
  externalId?: string;
  username?: string;
  email?: string;
  region?: string;
  status: string;
  riskLevel: string;
  notes?: string;
  metadata?: Record<string, string>;
  version: number;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
  displayName?: string;
  externalIdentifier?: string;
  profileId?: string;
  /** Legacy column on the account row; bindings live in /accounts/{id}/bindings. */
  browserInstanceId?: string;
  createdBy?: string;
}

export type AccountSecretKind = "password" | "cookie" | "totp_seed" | "api_key" | "oauth_token";

/** Safe secret metadata (gateway accountSecretResponse); secret values are write-only. */
export interface AccountSecretMetadata {
  id: string;
  accountId: string;
  workspaceId: string;
  kind: AccountSecretKind | string;
  createdAt: string;
  updatedAt: string;
  version: number;
}

export type AccountBindingType = "browser_instance" | "profile" | "proxy";

export interface AccountBinding {
  id: string;
  accountId: string;
  workspaceId: string;
  bindingType: AccountBindingType | string;
  targetId: string;
  status: string;
  createdAt: string;
  updatedAt: string;
}

export interface AccountRiskEvent {
  id: string;
  accountId: string;
  workspaceId: string;
  level: string;
  code: string;
  description?: string;
  createdBy: string;
  createdAt: string;
}

export type ProxyConnectorType = "xray" | "mihomo";
export type ProxyKernel = "direct" | "xray" | "sing-box" | "mihomo";

export interface ProxyNode {
  id: string;
  workspaceId: string;
  name: string;
  protocol: string;
  connectorType: ProxyConnectorType;
  kernel: ProxyKernel | string;
  host: string;
  port: number;
  username?: string;
  hasCredentials: boolean;
  status: string;
  version: number;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
}

export type ProxyAssignmentTargetType = "browser_instance" | "account" | "profile";

/** One active sign-in of the current user (GET /api/v1/me/sessions). */
export interface AccountSession {
  id: string;
  deviceId?: string;
  userAgent?: string;
  ipAddress?: string;
  createdAt: string;
  lastSeenAt: string;
  expiresAt: string;
  /** The session this client is using. */
  current: boolean;
}

export interface ProxyAssignment {
  id: string;
  workspaceId: string;
  proxyId: string;
  /** The assigned proxy's current name, resolved by the server on every read. */
  proxyName?: string;
  targetId: string;
  targetType: ProxyAssignmentTargetType | string;
  version: number;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
}

export interface ProxyHealthCheck {
  id: string;
  workspaceId: string;
  proxyId: string;
  /** Task ID of the queued proxy.health_check task. */
  requestId: string;
  connectorType: ProxyConnectorType;
  kernel: ProxyKernel | string;
  /** queued → succeeded | failed */
  status: string;
  ip?: string;
  latencyMs?: number;
  errorCode?: string;
  errorMessage?: string;
  createdBy: string;
  createdAt: string;
  completedAt?: string;
}

export interface Workflow {
  id: string;
  workspaceId: string;
  name: string;
  status: string;
  latestVersion: number;
  publishedVersionId?: string;
  version: number;
  createdBy?: string;
  createdAt?: string;
  updatedAt?: string;
  archivedAt?: string;
}

export type WorkflowEngine = "playwright" | "puppeteer" | "cdp";
export type WorkflowAction = "navigate" | "click" | "input" | "wait" | "upload" | "javascript" | "screenshot" | "extract" | "close";

export interface WorkflowStep {
  id: string;
  action: WorkflowAction;
  timeoutMs?: number;
  continueOnError?: boolean;
  parameters: Record<string, unknown>;
}

export interface WorkflowDefinition {
  schemaVersion: "ant-workflow/v1";
  engine: WorkflowEngine;
  steps: WorkflowStep[];
}

export interface WorkflowVersion {
  id: string;
  workspaceId: string;
  workflowId: string;
  version: number;
  schemaVersion: "ant-workflow/v1";
  definition: WorkflowDefinition;
  contentHash: string;
  createdAt: string;
}

export interface TaskRun {
  id: string;
  workspaceId: string;
  taskType: string;
  workflowId?: string;
  workflowVersionId?: string;
  requestedBy?: string;
  idempotencyKey: string;
  status: string;
  priority: number;
  payload: Record<string, unknown>;
  retryLimit: number;
  availableAt: string;
  leaseOwner?: string;
  leaseExpiresAt?: string;
  createdAt: string;
  updatedAt: string;
  completedAt?: string;
  errorCode?: string;
  errorMessage?: string;
}

export interface Schedule {
  id: string;
  workspaceId: string;
  workflowId: string;
  workflowVersionId: string;
  instanceId: string;
  cronExpression: string;
  timezone: string;
  enabled: boolean;
  status: string;
  nextRunAt?: string;
  lastRunAt?: string;
  lastError?: string;
  leaseOwner?: string;
  leaseExpiresAt?: string;
  createdAt: string;
  updatedAt: string;
  version: number;
}

/** profilesyncservice.Profile (cloud browser profile). */
export interface CloudProfile {
  id: string;
  workspaceId: string;
  ownerUserId?: string;
  name: string;
  fingerprintTemplateId?: string;
  currentRevisionId?: string;
  /** active | syncing | conflict */
  status: string;
  version: number;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
}

/** profilesyncservice.Revision */
export interface ProfileRevision {
  id: string;
  workspaceId: string;
  profileId: string;
  revision: number;
  baseRevisionId?: string;
  contentHash: string;
  /** uploading | committed | superseded */
  status: string;
  deviceId?: string;
  createdBy?: string;
  createdAt: string;
  committedAt?: string;
}

export type ProfileConflictResolution = "keep_local" | "keep_remote";

/** profilesyncservice.Conflict */
export interface ProfileConflict {
  id: string;
  workspaceId: string;
  profileId: string;
  localRevisionId: string;
  remoteRevisionId: string;
  /** open | resolved */
  status: string;
  resolution?: ProfileConflictResolution | string;
  resolvedBy?: string;
  createdAt: string;
  resolvedAt?: string;
}

export interface BatchItemError {
  code: string;
  message: string;
  retryable: boolean;
}

export interface BatchItemResult<T> {
  index: number;
  itemId?: string;
  idempotencyKey?: string;
  status: "succeeded" | "failed" | "cancelled";
  value?: T;
  error?: BatchItemError;
}

export interface BatchResult<T> {
  operation: string;
  atomic: false;
  replaySafe: boolean;
  summary: { total: number; succeeded: number; failed: number; cancelled: number };
  items: BatchItemResult<T>[];
}

export interface AnalyticsWindow {
  from: string;
  to: string;
}

export interface AnalyticsDashboard {
  window: AnalyticsWindow;
  accounts: { total: number; active: number; atRisk: number; byStatus: Record<string, number> };
  instances: { total: number; online: number; offline: number; starting: number; failed: number };
  proxies: { total: number; healthy: number; unhealthy: number; samples: number; successRate: number; averageLatencyMs: number };
  tasks: { total: number; completed: number; succeeded: number; failed: number; successRate: number };
  risks: { open: number; bySeverity: Record<string, number> };
  team: { activeMembers: number; activeActors: number; eventsByActor: Record<string, number> };
}

export interface AnalyticsRiskEvent {
  id: string;
  workspaceId: string;
  accountId?: string;
  proxyId?: string;
  severity: string;
  eventType: string;
  details?: Record<string, unknown>;
  createdAt: string;
  resolvedAt?: string;
}

export interface AnalyticsRiskPage {
  items: AnalyticsRiskEvent[];
  nextOffset?: number;
}

export interface NotificationItem {
  id: string;
  workspaceId?: string;
  recipientUserId?: string;
  eventType: string;
  title: string;
  body: string;
  payload?: Record<string, unknown>;
  readAt?: string;
  createdAt: string;
}

export interface NotificationPage {
  items: NotificationItem[];
  nextOffset?: number;
  unreadCount: number;
}

export interface NotificationPreference {
  workspaceId: string;
  userId: string;
  channel: "in_app" | "email" | "websocket" | "push";
  eventType: string;
  enabled: boolean;
  updatedAt: string;
}

export interface BillingPlanEntitlement {
  planId: string;
  code: string;
  limit?: number;
  featureEnabled: boolean;
  metadata?: Record<string, string>;
}

export interface BillingPlan {
  id: string;
  code: string;
  name: string;
  currency: string;
  amountMinor: number;
  billingInterval: string;
  active: boolean;
  createdAt: string;
  updatedAt: string;
  entitlements?: BillingPlanEntitlement[];
}

export interface BillingSubscription {
  id: string;
  organizationId: string;
  planId: string;
  status: string;
  provider: string;
  providerSubscriptionRef: string;
  currentPeriodStart: string;
  currentPeriodEnd: string;
  cancelAtPeriodEnd: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface Entitlement {
  id: string;
  organizationId: string;
  code: string;
  sourceSubscriptionId?: string;
  limit?: number;
  featureEnabled: boolean;
  consumed: number;
  validFrom: string;
  validUntil?: string;
  metadata?: Record<string, string>;
}

export type FingerprintMode = "seeded" | "fixed" | "custom";
export type FingerprintPlatform = "windows" | "linux" | "macos";

export interface FingerprintMediaDevices {
  audioInputs: number;
  videoInputs: number;
  audioOutputs: number;
}

export interface FingerprintBattery {
  charging: boolean;
  level: number;
  chargingTimeSeconds: number;
  dischargingTimeSeconds: number;
}

export interface FingerprintConfiguration {
  platformVersion?: string;
  windowWidth?: number;
  windowHeight?: number;
  hardwareConcurrency?: number;
  deviceMemory?: number;
  colorDepth?: number;
  maxTouchPoints?: number;
  doNotTrack?: "" | "0" | "1" | "unspecified";
  screenWidth?: number;
  screenHeight?: number;
  deviceScaleFactor?: number;
  webrtcPolicy?: "" | "default" | "default_public_and_private_interfaces" | "default_public_interface_only" | "disable_non_proxied_udp";
  canvasNoise: boolean;
  audioNoise: boolean;
  clientRectsNoise: boolean;
  fonts?: string[];
  webglVendor?: string;
  webglRenderer?: string;
  mediaDevices?: FingerprintMediaDevices;
  battery?: FingerprintBattery;
}

export interface FingerprintCreateInput {
  name: string;
  mode: FingerprintMode;
  browserMajor: number;
  platform: FingerprintPlatform;
  seed?: string;
  locale: string;
  timezone: string;
  configuration: FingerprintConfiguration;
}

export interface FingerprintTemplate extends Omit<FingerprintCreateInput, "seed"> {
  id: string;
  workspaceId: string;
  browserFamily: "chromium" | string;
  seed: string;
  runtimeArgs: string[];
  version: number;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
  deletedAt?: string;
}

export interface FingerprintPreset {
  key: string;
  name: string;
  description: string;
  input: FingerprintCreateInput;
}

export interface FingerprintBatchCreateInput {
  namePrefix: string;
  count: number;
  mode: FingerprintMode;
  browserMajor: number;
  platform: FingerprintPlatform;
  seedStart?: string;
  locale: string;
  timezone: string;
  configuration: FingerprintConfiguration;
}
