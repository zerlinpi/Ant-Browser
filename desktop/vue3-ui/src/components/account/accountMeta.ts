import type { RouteLocationRaw } from "vue-router";
import { api, ApiError, describeError } from "@/api/client";
import type { ActionMenuItem } from "@/components/ActionMenu.vue";
import type { Permission } from "@/permissions";
import type { AccountAsset, AccountBindingType, AccountSecretKind, BrowserInstance, CloudProfile, ProxyNode } from "@/types";
import { shortId } from "@/utils/format";

export interface AccountOption<T extends string = string> {
  value: T;
  label: string;
  hint?: string;
}

/** accountservice.Platform values; the API expects the lower-case form. */
export const accountPlatforms: AccountOption[] = [
  { value: "amazon", label: "Amazon" },
  { value: "shopify", label: "Shopify" },
  { value: "tiktok", label: "TikTok" },
  { value: "facebook", label: "Facebook" },
  { value: "google", label: "Google" },
  { value: "ebay", label: "eBay" },
];

/**
 * accountservice.validStatus minus "deleted": deletion must go through DELETE,
 * which also sets deletedAt. paused/risk are legacy values the server accepts.
 */
export const accountStatuses: AccountOption[] = [
  { value: "pending", label: "待接入" },
  { value: "active", label: "正常" },
  { value: "verification_required", label: "待验证" },
  { value: "suspended", label: "已封停" },
  { value: "locked", label: "已锁定" },
  { value: "disabled", label: "已禁用" },
  { value: "error", label: "异常" },
  { value: "paused", label: "已暂停", hint: "旧版状态" },
  { value: "risk", label: "风险", hint: "旧版状态" },
];

export const accountRiskLevels: AccountOption[] = [
  { value: "unknown", label: "未评估" },
  { value: "low", label: "低风险" },
  { value: "medium", label: "中风险" },
  { value: "high", label: "高风险" },
  { value: "critical", label: "严重" },
];

/** Risk events use the workspace-wide severities (analytics filters on the same set); "unknown" is an account level only. */
export const riskEventLevels: AccountOption[] = [
  { value: "info", label: "提示" },
  ...accountRiskLevels.filter((item) => item.value !== "unknown"),
];

/** Suggestions only: the API accepts any non-empty code. */
export const riskEventCodes: AccountOption[] = [
  { value: "login_challenge", label: "登录验证" },
  { value: "identity_verification", label: "身份验证" },
  { value: "policy_warning", label: "政策警告" },
  { value: "listing_removed", label: "商品下架" },
  { value: "payment_issue", label: "付款异常" },
  { value: "ip_mismatch", label: "IP 异常" },
  { value: "suspension_notice", label: "封停通知" },
];

export const secretKinds: AccountOption<AccountSecretKind>[] = [
  { value: "password", label: "登录密码" },
  { value: "cookie", label: "Cookie" },
  { value: "totp_seed", label: "2FA 密钥" },
  { value: "api_key", label: "API Key" },
  { value: "oauth_token", label: "OAuth 令牌" },
];

export interface BindingTypeOption extends AccountOption<AccountBindingType> {
  icon: string;
  /** Required to list candidate targets. */
  readPermission: Permission;
}

export const bindingTypes: BindingTypeOption[] = [
  { value: "browser_instance", label: "浏览器实例", icon: "lucide:monitor", readPermission: "instance.read" },
  { value: "profile", label: "云端档案", icon: "lucide:cloud", readPermission: "profile.read" },
  { value: "proxy", label: "代理", icon: "lucide:globe-2", readPermission: "proxy.read" },
];

const labelFor = (options: readonly AccountOption[], value: string | null | undefined, empty: string) => {
  if (!value) return empty;
  const normalized = value.toLowerCase();
  return options.find((item) => item.value === normalized)?.label ?? value;
};

export const platformLabel = (value?: string | null) => labelFor(accountPlatforms, value, "—");
export const statusLabel = (value?: string | null) => (value?.toLowerCase() === "deleted" ? "已删除" : labelFor(accountStatuses, value, "未知"));
export const riskLabel = (value?: string | null) =>
  value?.toLowerCase() === "info" ? "提示" : labelFor(accountRiskLevels, value || "unknown", "未评估");
export const secretKindLabel = (value?: string | null) => labelFor(secretKinds, value, "—");
export const isBindingType = (value: string): value is AccountBindingType => bindingTypes.some((item) => item.value === value);

/** Listed events carry the stored event type ("account.<code>"); creation echoes the bare code. */
export const riskEventCode = (code?: string | null) => (code || "").replace(/^account\./, "");
export const riskEventCodeLabel = (code?: string | null) => {
  const bare = riskEventCode(code);
  return riskEventCodes.find((item) => item.value === bare)?.label ?? "";
};

const attentionStatuses = new Set(["verification_required", "suspended", "locked", "error", "risk"]);
const attentionRisks = new Set(["high", "critical"]);
export const needsAttention = (account: AccountAsset) =>
  attentionStatuses.has((account.status || "").toLowerCase()) || attentionRisks.has((account.riskLevel || "").toLowerCase());

export type AccountStateField = "status" | "riskLevel";

/** Lifecycle menu shared by the account list rows and the detail header. */
export const accountMenuItems = (account: AccountAsset): ActionMenuItem[] => [
  account.status === "locked"
    ? { key: "unlock", label: "解除锁定", icon: "lucide:lock-open", hint: "恢复为正常" }
    : { key: "lock", label: "锁定账号", icon: "lucide:lock" },
  { key: "status", label: "更改状态…", icon: "lucide:circle-dot" },
  { key: "risk", label: "调整风险等级…", icon: "lucide:shield-alert" },
  { key: "delete", label: "删除账号", icon: "lucide:trash-2", danger: true },
];

// accountservice validation errors reach the client as English 422 messages.
const validationMessages: Record<string, string> = {
  "email is invalid": "邮箱格式不正确",
  "valid account name and identifier are required": "名称和账号标识不能为空，名称不超过 120 个字符",
  "valid account name is required": "名称不能为空，且不超过 120 个字符",
  "account identifier is required": "账号标识不能为空，且不超过 255 个字符",
  "shopify identifier must be a valid store URL": "Shopify 账号标识须为有效的店铺 URL",
  "platform must be amazon, shopify, tiktok, facebook, google, or ebay": "不支持该平台",
  "invalid account status": "账号状态无效",
  "invalid account risk level": "风险等级无效",
  "expectedVersion must be positive": "缺少版本信息，请刷新后重试",
  "secret kind is invalid": "凭据类型无效",
  "encrypted secret envelope is required": "凭据内容不能为空",
  "bindingType and targetId are required": "请选择绑定类型和目标",
  "risk event level must be info, low, medium, high or critical, and a code is required": "请选择风险等级并填写事件代码",
};

export const describeAccountError = (error: unknown, fallback: string, overrides: Record<string, string> = {}) => {
  if (error instanceof ApiError && error.code === "validation_failed" && !overrides.validation_failed) {
    return validationMessages[error.message] ?? `提交内容未通过校验（${error.message}）`;
  }
  return describeError(error, fallback, {
    internal_error: "服务端处理失败，请稍后重试",
    invalid_request: "请求内容无效，请刷新页面后重试",
    ...overrides,
  });
};

export const isNotFoundError = (error: unknown) => error instanceof ApiError && error.status === 404;

/** The cached account is stale: reload before the user retries. */
export const isStaleAccountError = (error: unknown) =>
  error instanceof ApiError && (error.code === "version_conflict" || error.status === 404);

export const accountConflictMessages: Record<string, string> = {
  version_conflict: "账号已被其他成员更新，已刷新数据，请重试",
  not_found: "账号不存在或已被删除",
};

export interface BindingTarget {
  id: string;
  name: string;
  detail: string;
}

const live = (item: { deletedAt?: string }) => !item.deletedAt;
const fromInstance = (item: BrowserInstance): BindingTarget => ({
  id: item.id,
  name: item.name || shortId(item.id),
  detail: [item.platform, shortId(item.id)].filter(Boolean).join(" · "),
});
const fromProfile = (item: CloudProfile): BindingTarget => ({
  id: item.id,
  name: item.name || shortId(item.id),
  detail: shortId(item.id),
});
const fromProxy = (item: ProxyNode): BindingTarget => ({
  id: item.id,
  name: item.name || shortId(item.id),
  detail: [item.protocol, item.port ? `${item.host}:${item.port}` : item.host].filter(Boolean).join(" · "),
});

/** Candidate targets for POST .../bindings, read from the workspace resource lists. */
export const listBindingTargets = async (base: string, type: AccountBindingType): Promise<BindingTarget[]> => {
  if (type === "browser_instance") return (await api.list<BrowserInstance>(`${base}/browser-instances`)).filter(live).map(fromInstance);
  if (type === "profile") return (await api.list<CloudProfile>(`${base}/profiles`)).filter(live).map(fromProfile);
  return (await api.list<ProxyNode>(`${base}/proxies`)).filter(live).map(fromProxy);
};

/** Resolves a bound target for display; null means it was soft-deleted (a 404 is left to the caller). */
export const getBindingTarget = async (base: string, type: AccountBindingType, id: string): Promise<BindingTarget | null> => {
  const segment = encodeURIComponent(id);
  if (type === "browser_instance") {
    const item = await api.get<BrowserInstance>(`${base}/browser-instances/${segment}`);
    return live(item) ? fromInstance(item) : null;
  }
  if (type === "profile") {
    const item = await api.get<CloudProfile>(`${base}/profiles/${segment}`);
    return live(item) ? fromProfile(item) : null;
  }
  const item = await api.get<ProxyNode>(`${base}/proxies/${segment}`);
  return live(item) ? fromProxy(item) : null;
};

export const bindingTargetRoute = (type: AccountBindingType, targetId: string, target?: BindingTarget): RouteLocationRaw => {
  if (type === "profile") return { name: "profile-detail", params: { profileId: targetId } };
  if (type === "browser_instance") return target ? { name: "browsers", query: { q: target.name } } : { name: "browsers" };
  return { name: "proxies" };
};
