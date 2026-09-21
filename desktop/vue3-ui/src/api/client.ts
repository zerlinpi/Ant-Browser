export interface ApiErrorPayload {
  code?: string;
  message?: string;
  details?: unknown;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
    readonly code = "request_failed",
    readonly details?: unknown,
  ) {
    super(message);
  }
}

type RefreshHandler = () => Promise<string | null>;

const trimURL = (value: string) => value.trim().replace(/\/+$/, "");

export class ApiClient {
  private accessToken = "";
  private refreshHandler?: RefreshHandler;

  constructor(private baseURL: string) {
    this.baseURL = trimURL(baseURL);
  }

  configure(baseURL: string, accessToken = "", refreshHandler?: RefreshHandler) {
    this.baseURL = trimURL(baseURL);
    this.accessToken = accessToken;
    this.refreshHandler = refreshHandler;
  }

  setAccessToken(value: string) {
    this.accessToken = value;
  }

  async request<T>(path: string, init: RequestInit = {}, allowRefresh = true): Promise<T> {
    const headers = new Headers(init.headers);
    headers.set("Accept", "application/json");
    if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (this.accessToken) headers.set("Authorization", `Bearer ${this.accessToken}`);

    const response = await fetch(`${this.baseURL}${path}`, { ...init, headers });
    if (response.status === 401 && allowRefresh && this.refreshHandler) {
      const token = await this.refreshHandler();
      if (token) {
        this.accessToken = token;
        return this.request<T>(path, init, false);
      }
    }

    const raw = response.status === 204 ? undefined : await response.json().catch(() => undefined);
    if (!response.ok) {
      const error = (raw?.error ?? raw) as ApiErrorPayload | undefined;
      throw new ApiError(error?.message || `请求失败 (${response.status})`, response.status, error?.code, error?.details);
    }
    return (raw?.data ?? raw) as T;
  }

  get<T>(path: string) {
    return this.request<T>(path);
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

  delete<T>(path: string) {
    return this.request<T>(path, { method: "DELETE" });
  }
}

export const api = new ApiClient(import.meta.env.VITE_API_BASE_URL || "http://127.0.0.1:8080");

export const idempotencyKey = (scope: string) => {
  const random = crypto.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(16).slice(2)}`;
  return `${scope}-${random}`;
};
