export interface User {
  id: string;
  email: string;
  displayName?: string;
}

export interface Workspace {
  id: string;
  organizationId: string;
  name: string;
  slug?: string;
  role?: string;
}

export interface BrowserInstance {
  id: string;
  name: string;
  status: string;
  desiredState?: string;
  connectorType?: "xray" | "mihomo";
  proxyId?: string;
  accountId?: string;
  fingerprintTemplateId?: string;
  tags?: string[];
  updatedAt?: string;
}

export interface AccountAsset {
  id: string;
  platform: string;
  username: string;
  status: string;
  riskLevel?: string;
  email?: string;
  updatedAt?: string;
}

export interface ProxyNode {
  id: string;
  name: string;
  protocol: string;
  connectorType?: "xray" | "mihomo";
  host?: string;
  port?: number;
  countryCode?: string;
  status?: string;
  latencyMs?: number;
  updatedAt?: string;
}

export interface Workflow {
  id: string;
  name: string;
  status: string;
  engine?: "playwright" | "puppeteer" | "cdp";
  publishedVersion?: number;
  updatedAt?: string;
}

export interface TaskRun {
  id: string;
  kind: string;
  status: string;
  attempt?: number;
  scheduledAt?: string;
  updatedAt?: string;
  lastError?: string;
}

export interface NotificationItem {
  id: string;
  type: string;
  title: string;
  message: string;
  readAt?: string;
  createdAt?: string;
}

export interface Entitlement {
  key: string;
  limitValue?: number;
  usedValue?: number;
  featureEnabled?: boolean;
}
