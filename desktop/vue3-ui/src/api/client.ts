import type { AuthTokenPair, BatchItemResult, BatchResult } from "@/types";

export interface ApiErrorPayload {
  code?: string;
  message?: string;
  details?: unknown;
  traceId?: string;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code = "request_failed",
    readonly details?: unknown,
    readonly traceId?: string,
    /** Parsed from Retry-After when the header is readable (same origin or CORS-exposed). */
    readonly retryAfterSeconds?: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface RefreshConfig {
  /** Current refresh token; an empty value means the session cannot be renewed. */
  getRefreshToken: () => string;
  /** Persists the rotated token pair (refresh tokens are single-use on the server). */
  onRefreshed: (pair: AuthTokenPair) => void;
}

interface RequestOptions {
  isRetry?: boolean;
}

const AUTH_PREFIX = "/api/v1/auth/";
const REFRESH_PATH = "/api/v1/auth/refresh";

const trimURL = (value: string) => value.trim().replace(/\/+$/, "");

export const defaultAPIBaseURL = () => {
  const configured = trimURL(import.meta.env.VITE_API_BASE_URL || "");
  if (configured) return configured;
  if (typeof window !== "undefined" && !import.meta.env.DEV) {
    const { protocol, hostname, origin } = window.location;
    // The hosted console is served behind the same Nginx origin as /api.
    // Wails uses its own synthetic origin and must continue to target the
    // locally configured control-plane address.
    if ((protocol === "http:" || protocol === "https:") && hostname !== "wails.localhost") return origin;
  }
  return "http://127.0.0.1:8080";
};

const parseRetryAfter = (value: string | null): number | undefined => {
  if (!value) return undefined;
  const seconds = Number(value.trim());
  if (Number.isFinite(seconds) && seconds >= 0) return Math.ceil(seconds);
  const date = Date.parse(value);
  return Number.isNaN(date) ? undefined : Math.max(0, Math.ceil((date - Date.now()) / 1000));
};

const sessionExpiredError = () => new ApiError("登录已过期，请重新登录", 401, "session_expired");

export class ApiClient {
  private accessToken = "";
  private refreshConfig?: RefreshConfig;
  private refreshInFlight: Promise<string> | null = null;
  private readonly sessionExpiredListeners = new Set<() => void>();

  constructor(private baseURL: string) {
    this.baseURL = trimURL(baseURL);
  }

  configure(baseURL: string, accessToken = "", refresh?: RefreshConfig) {
    this.baseURL = trimURL(baseURL);
    this.accessToken = accessToken;
    this.refreshConfig = refresh;
  }

  setAccessToken(value: string) {
    this.accessToken = value;
  }

  /** Registers a callback invoked when token renewal is definitively rejected. */
  onSessionExpired(listener: () => void) {
    this.sessionExpiredListeners.add(listener);
    return () => {
      this.sessionExpiredListeners.delete(listener);
    };
  }

  async request<T>(path: string, init: RequestInit = {}, options: RequestOptions = {}): Promise<T> {
    const token = this.accessToken;
    const response = await this.send(path, init, token);
    if (response.status === 401 && !options.isRetry && token && this.canRefresh(path)) {
      // The session was cleared while this request was in flight.
      if (!this.accessToken) throw sessionExpiredError();
      // Only renew when no concurrent request has already rotated the token.
      if (this.accessToken === token) await this.refreshAccessToken();
      return this.request<T>(path, init, { isRetry: true });
    }
    return this.parse<T>(response);
  }

  get<T>(path: string) {
    return this.request<T>(path);
  }

  /** GET for collection endpoints; a null payload (nil Go slice) becomes an empty list. */
  async list<T>(path: string): Promise<T[]> {
    const value = await this.request<T[] | null>(path);
    return Array.isArray(value) ? value : [];
  }

  post<T>(path: string, body?: unknown, idempotencyKey?: string) {
    const headers: Record<string, string> = {};
    if (idempotencyKey) headers["Idempotency-Key"] = idempotencyKey;
    return this.request<T>(path, { method: "POST", headers, body: body === undefined ? undefined : JSON.stringify(body) });
  }

  patch<T>(path: string, body: unknown) {
    return this.request<T>(path, { method: "PATCH", body: JSON.stringify(body) });
  }

  put<T>(path: string, body: unknown) {
    return this.request<T>(path, { method: "PUT", body: JSON.stringify(body) });
  }

  /** Some DELETE routes decode a JSON body (for example `{ expectedVersion }`). */
  delete<T>(path: string, body?: unknown) {
    return this.request<T>(path, { method: "DELETE", body: body === undefined ? undefined : JSON.stringify(body) });
  }

  private canRefresh(path: string) {
    // Auth endpoints answer 401 for bad credentials or rejected refresh tokens;
    // renewing on those would recurse or mask the real error.
    return Boolean(this.refreshConfig) && !path.startsWith(AUTH_PREFIX);
  }

  /** Single-flight: concurrent 401 responses share one refresh request. */
  private refreshAccessToken(): Promise<string> {
    if (!this.refreshInFlight) {
      this.refreshInFlight = this.performRefresh().finally(() => {
        this.refreshInFlight = null;
      });
    }
    return this.refreshInFlight;
  }

  private async performRefresh(): Promise<string> {
    const refreshToken = this.refreshConfig?.getRefreshToken() ?? "";
    if (!refreshToken) {
      this.expireSession();
      throw sessionExpiredError();
    }
    // Sent directly (never through request()) and without a bearer token, so a
    // rejected refresh can never trigger another refresh.
    const response = await this.send(REFRESH_PATH, { method: "POST", body: JSON.stringify({ refreshToken }) }, "");
    if (response.status === 429 || response.status >= 500) {
      // Transient (rate limited or server unavailable): keep the session so a
      // later request can renew it, and surface the server message.
      throw await this.toError(response);
    }
    if (!response.ok) {
      this.expireSession();
      throw sessionExpiredError();
    }
    const pair = await this.parse<AuthTokenPair>(response).catch(() => undefined);
    if (!pair?.accessToken || !pair.refreshToken) {
      this.expireSession();
      throw sessionExpiredError();
    }
    this.accessToken = pair.accessToken;
    this.refreshConfig?.onRefreshed(pair);
    return pair.accessToken;
  }

  private expireSession() {
    this.accessToken = "";
    for (const listener of this.sessionExpiredListeners) {
      try {
        listener();
      } catch {
        // A failing listener must not mask the authentication error.
      }
    }
  }

  private async send(path: string, init: RequestInit, token: string): Promise<Response> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body != null && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (token) headers.set("Authorization", `Bearer ${token}`);
    try {
      return await fetch(`${this.baseURL}${path}`, { ...init, headers });
    } catch (error) {
      if (error instanceof DOMException && error.name === "AbortError") throw error;
      throw new ApiError("无法连接 Cloud API，请检查网络或 API 地址", 0, "network_error");
    }
  }

  private async parse<T>(response: Response): Promise<T> {
    if (!response.ok) throw await this.toError(response);
    if (response.status === 204) return undefined as T;
    const raw: unknown = await response.json().catch(() => undefined);
    if (raw && typeof raw === "object" && "data" in raw) return (raw as { data: T }).data;
    return raw as T;
  }

  private async toError(response: Response): Promise<ApiError> {
    const raw: unknown = await response.json().catch(() => undefined);
    const envelope = raw && typeof raw === "object" && "error" in raw ? (raw as { error: unknown }).error : raw;
    const payload = (envelope && typeof envelope === "object" ? envelope : {}) as ApiErrorPayload;
    const message = typeof payload.message === "string" && payload.message.trim() ? payload.message.trim() : `请求失败 (${response.status})`;
    const code = typeof payload.code === "string" && payload.code ? payload.code : response.status === 429 ? "rate_limited" : "request_failed";
    return new ApiError(message, response.status, code, payload.details, payload.traceId, parseRetryAfter(response.headers.get("Retry-After")));
  }
}

export const api = new ApiClient(defaultAPIBaseURL());

export const idempotencyKey = (scope: string) => {
  const random = crypto.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${scope}-${random}`;
};

// Localized messages for stable server error codes. Codes without an entry fall
// back to the server message so nothing actionable is hidden.
const errorCodeMessages: Record<string, string> = {
  network_error: "无法连接 Cloud API，请检查网络或 API 地址",
  rate_limited: "操作过于频繁，请稍后重试",
  session_expired: "登录已过期，请重新登录",
  unauthorized: "登录已过期，请重新登录",
  invalid_access_token: "登录已过期，请重新登录",
  invalid_credentials: "邮箱或密码错误",
  invalid_password: "密码不正确",
  mfa_invalid_code: "验证码不正确。请确认手机时间准确，并输入最新的验证码",
  mfa_code_required: "请输入验证码或恢复码",
  mfa_challenge_invalid: "登录验证已过期或尝试次数过多，请重新登录",
  mfa_locked: "验证码错误次数过多，两步验证已暂时锁定",
  mfa_already_enabled: "两步验证已经开启",
  mfa_not_enabled: "两步验证尚未开启",
  mfa_setup_required: "绑定信息已失效，请重新开始绑定",
  mfa_unavailable: "服务端暂不支持两步验证，请联系管理员",
  forbidden: "当前角色没有执行此操作的权限",
  resource_disabled: "账号或设备已被停用",
  origin_forbidden: "当前客户端来源未被 Cloud API 允许",
  not_found: "资源不存在或已被删除",
  version_conflict: "数据已被更新，请刷新后重试",
  version_precondition_required: "缺少版本信息，请刷新后重试",
  idempotency_conflict: "请求标识冲突，请刷新后重试",
  quota_exceeded: "套餐配额已用尽或功能未开通",
  invitation_invalid: "邀请码无效、已过期、已撤销或已被使用",
  last_owner: "工作空间至少需要保留一位所有者",
  proxy_in_use: "代理仍分配给实例、账号或档案，请先解除分配",
  proxy_assignment_conflict: "该目标已分配其他代理，请先解除原分配",
  instance_name_conflict: "已有同名实例（不区分大小写），请更换名称",
  proxy_name_conflict: "已有同名代理（不区分大小写），请更换名称",
  profile_name_conflict: "已有同名云端档案（不区分大小写），请更换名称",
  workflow_name_conflict: "已有同名工作流（含已归档），请更换名称",
  account_identifier_conflict: "同平台已存在相同标识的账号（不区分大小写），请更换账号标识",
  invalid_cron_expression: "Cron 表达式无效，请使用五段格式：分 时 日 月 周",
  invalid_timezone: "时区无效，请填写 IANA 时区，例如 Asia/Shanghai",
  instance_state_conflict: "实例正在运行或迁移中，请先停止后再操作",
  task_state_conflict: "任务已结束或正在被执行，无法取消",
  schedule_state_conflict: "计划已变化，或工作流发布版本不可用于定时调度",
  profile_revision_state: "冲突已处理或云端版本已变化，请刷新后重试",
  profile_lease_held: "档案正被设备同步，请等待同步结束后重试",
  profile_conflict_unresolved: "档案存在未解决的同步冲突，请先在云端档案中处理",
  profile_object_unavailable: "设备上传的版本数据不完整，只能保留云端版本",
  batch_too_large: "单次批量操作最多 100 项",
  dependency_unavailable: "服务暂不可用，请稍后重试",
  service_unavailable: "所需服务暂不可用，请稍后重试",
  batch_dependency_unavailable: "批量服务暂不可用，请稍后重试",
};

/**
 * Adds localized messages for a feature's error codes. Feature modules call
 * this once at import time instead of editing the shared table above.
 */
export const registerErrorMessages = (messages: Record<string, string>) => {
  Object.assign(errorCodeMessages, messages);
};

const formatWait = (seconds: number) => (seconds < 60 ? `${seconds} 秒` : `约 ${Math.ceil(seconds / 60)} 分钟`);

/** User-facing message for any thrown value; `overrides` maps error codes to context-specific copy. */
export const describeError = (error: unknown, fallback: string, overrides: Record<string, string> = {}): string => {
  if (error instanceof ApiError) {
    const localized = overrides[error.code] || errorCodeMessages[error.code];
    if (error.status === 429 || error.code === "rate_limited") {
      // Keep the wait from Retry-After next to the (localized) reason.
      const reason = localized || error.message || fallback;
      return error.retryAfterSeconds ? `${reason}（${formatWait(error.retryAfterSeconds)}后可重试）` : reason;
    }
    return localized || error.message || fallback;
  }
  if (error instanceof Error && error.message.trim()) return error.message;
  return fallback;
};

const batchErrorMessages: Record<string, string> = {
  forbidden: "权限不足",
  not_found: "资源不存在或已删除",
  version_conflict: "版本已变化，请刷新后重试",
  state_conflict: "当前状态不允许该操作",
  unsupported: "不支持该操作",
  duplicate_item: "重复的批量项",
  name_conflict: "名称或标识已被使用（可能与本批次前面的项重复）",
  cancelled: "已取消",
  deadline_exceeded: "执行超时",
};

export interface BatchFailure {
  index: number;
  itemId?: string;
  message: string;
}

export interface BatchOutcome {
  total: number;
  succeeded: number;
  failed: number;
  firstError: string;
  failures: BatchFailure[];
}

export const batchItemMessage = (item: BatchItemResult<unknown>) => {
  if (item.error) return batchErrorMessages[item.error.code] || item.error.message || "执行失败";
  return item.status === "cancelled" ? "已取消" : "执行失败";
};

/**
 * Inspects a BatchResult. The server answers 207 with the same payload when any
 * item fails, so callers must not assume a resolved request means success.
 */
export const summarizeBatch = <T>(result: BatchResult<T> | null | undefined): BatchOutcome => {
  const items = Array.isArray(result?.items) ? result.items : [];
  const failures = items
    .filter((item) => item.status !== "succeeded")
    .map((item) => ({ index: item.index, itemId: item.itemId, message: batchItemMessage(item) }));
  const summary = result?.summary;
  return {
    total: summary?.total ?? items.length,
    succeeded: summary?.succeeded ?? items.length - failures.length,
    failed: summary ? summary.failed + summary.cancelled : failures.length,
    firstError: failures[0]?.message ?? "",
    failures,
  };
};

export const formatBatchOutcome = (outcome: BatchOutcome) =>
  outcome.failed
    ? `成功 ${outcome.succeeded} / 失败 ${outcome.failed}${outcome.firstError ? `：${outcome.firstError}` : ""}`
    : `成功 ${outcome.succeeded} 项`;
