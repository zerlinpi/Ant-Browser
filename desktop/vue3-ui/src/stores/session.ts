import { computed, ref } from "vue";
import { defineStore } from "pinia";
import { api, ApiError, defaultAPIBaseURL } from "@/api/client";
import { hasPermission, type Permission } from "@/permissions";
import type { AuthTokenPair, LoginResult, MFAChallenge, SecondFactorProof, User, Workspace } from "@/types";

const SESSION_KEY = "ant-browser.cloud-session.v1";
const WORKSPACE_KEY = "ant-browser.active-workspace.v1";
const API_URL_KEY = "ant-browser.api-url.v1";

const normalizeURL = (value: string) => value.trim().replace(/\/+$/, "");

function readSession(): Pick<AuthTokenPair, "accessToken" | "refreshToken"> {
  try {
    const value = JSON.parse(sessionStorage.getItem(SESSION_KEY) || "{}");
    return {
      accessToken: typeof value.accessToken === "string" ? value.accessToken : "",
      refreshToken: typeof value.refreshToken === "string" ? value.refreshToken : "",
    };
  } catch {
    return { accessToken: "", refreshToken: "" };
  }
}

export const useSessionStore = defineStore("session", () => {
  const initial = readSession();
  const apiBaseURL = ref(normalizeURL(localStorage.getItem(API_URL_KEY) || defaultAPIBaseURL()));
  const accessToken = ref(initial.accessToken);
  const refreshToken = ref(initial.refreshToken);
  const user = ref<User | null>(null);
  const workspaces = ref<Workspace[]>([]);
  const activeWorkspaceId = ref(localStorage.getItem(WORKSPACE_KEY) || "");
  const ready = ref(false);
  const expiredListeners = new Set<() => void>();
  const authenticated = computed(() => Boolean(accessToken.value));
  const activeWorkspace = computed(() => workspaces.value.find((item) => item.id === activeWorkspaceId.value) ?? null);
  const workspaceBase = computed(() => activeWorkspaceId.value ? `/api/v1/workspaces/${activeWorkspaceId.value}` : "");

  const persistSession = () => {
    if (accessToken.value && refreshToken.value) {
      sessionStorage.setItem(SESSION_KEY, JSON.stringify({ accessToken: accessToken.value, refreshToken: refreshToken.value }));
    } else {
      sessionStorage.removeItem(SESSION_KEY);
    }
    localStorage.setItem(API_URL_KEY, apiBaseURL.value);
  };

  const applyTokens = (pair: AuthTokenPair) => {
    accessToken.value = pair.accessToken;
    refreshToken.value = pair.refreshToken;
    if (pair.user) user.value = pair.user;
    persistSession();
  };

  // The client owns renewal (single-flight); the store only supplies and
  // persists the rotating refresh token.
  const configureClient = () => api.configure(apiBaseURL.value, accessToken.value, {
    getRefreshToken: () => refreshToken.value,
    onRefreshed: applyTokens,
  });

  const clear = (options: { keepWorkspace?: boolean } = {}) => {
    accessToken.value = "";
    refreshToken.value = "";
    user.value = null;
    workspaces.value = [];
    activeWorkspaceId.value = "";
    sessionStorage.removeItem(SESSION_KEY);
    // An expired session keeps the last workspace so re-login returns to it.
    if (!options.keepWorkspace) localStorage.removeItem(WORKSPACE_KEY);
    configureClient();
  };

  api.onSessionExpired(() => {
    clear({ keepWorkspace: true });
    for (const listener of expiredListeners) listener();
  });

  /** Notified after the store has cleared an expired session (used by the router). */
  const onSessionExpired = (listener: () => void) => {
    expiredListeners.add(listener);
    return () => {
      expiredListeners.delete(listener);
    };
  };

  const loadIdentity = async () => {
    const [me, items] = await Promise.all([
      api.get<User>("/api/v1/me"),
      api.list<Workspace>("/api/v1/workspaces"),
    ]);
    user.value = me;
    workspaces.value = items;
    const preferred = activeWorkspaceId.value || localStorage.getItem(WORKSPACE_KEY) || "";
    activeWorkspaceId.value = items.some((item) => item.id === preferred) ? preferred : items[0]?.id || "";
    if (activeWorkspaceId.value) localStorage.setItem(WORKSPACE_KEY, activeWorkspaceId.value);
    else localStorage.removeItem(WORKSPACE_KEY);
  };

  const bootstrap = async () => {
    configureClient();
    if (accessToken.value) {
      try {
        await loadIdentity();
      } catch {
        clear({ keepWorkspace: true });
      }
    }
    ready.value = true;
  };

  const establish = async (pair: AuthTokenPair) => {
    applyTokens(pair);
    configureClient();
    await loadIdentity();
  };

  /**
   * Signs in with a token pair obtained outside `login` (for example an
   * OAuth exchange). Resolves once the identity and workspaces are loaded.
   */
  const adoptTokens = async (pair: AuthTokenPair) => {
    if (!pair?.accessToken || !pair.refreshToken) throw new ApiError("登录响应无效，请稍后重试", 0, "invalid_response");
    await establish(pair);
  };

  /**
   * Checks the password. Resolves to null once signed in, or to the challenge
   * that `verifyMFA` must complete when the account has two-factor
   * authentication.
   */
  const login = async (email: string, password: string, apiURL: string): Promise<MFAChallenge | null> => {
    apiBaseURL.value = normalizeURL(apiURL);
    configureClient();
    const result = await api.post<LoginResult>("/api/v1/auth/login", { email, password });
    if (result?.mfaRequired) {
      if (!result.mfaChallenge?.token) throw new ApiError("登录验证信息无效，请重新登录", 0, "mfa_challenge_invalid");
      return result.mfaChallenge;
    }
    if (!result?.accessToken || !result.refreshToken) throw new ApiError("登录响应无效，请稍后重试", 0, "invalid_response");
    await establish(result as AuthTokenPair);
    return null;
  };

  /** Completes a two-factor login with an authenticator code or a recovery code. */
  const verifyMFA = async (challengeToken: string, proof: SecondFactorProof) => {
    const pair = await api.post<AuthTokenPair>("/api/v1/auth/mfa/verify", { challengeToken, ...proof });
    await establish(pair);
  };

  const register = async (email: string, password: string, displayName: string, apiURL: string) => {
    apiBaseURL.value = normalizeURL(apiURL);
    configureClient();
    const pair = await api.post<AuthTokenPair>("/api/v1/auth/register", { email, password, displayName });
    await establish(pair);
  };

  const selectWorkspace = (id: string) => {
    if (!workspaces.value.some((item) => item.id === id)) return;
    activeWorkspaceId.value = id;
    localStorage.setItem(WORKSPACE_KEY, id);
  };

  /**
   * Switches the control-plane address. Tokens were issued by the previous
   * server, so the session is cleared and the caller must route to login.
   * Returns false when the address did not change.
   */
  const updateAPIBaseURL = (value: string) => {
    const next = normalizeURL(value);
    if (next === apiBaseURL.value) return false;
    // Best-effort revocation on the server that issued the session; the
    // request is dispatched before the client is reconfigured.
    if (accessToken.value) void api.post("/api/v1/auth/logout").catch(() => undefined);
    apiBaseURL.value = next;
    localStorage.setItem(API_URL_KEY, next);
    clear();
    return true;
  };

  const logout = async () => {
    try {
      if (accessToken.value) await api.post("/api/v1/auth/logout");
    } catch {
      // The local session is cleared even when the server cannot be reached.
    } finally {
      clear();
    }
  };

  /** Hides actions the caller's role cannot perform; unknown roles defer to the server. */
  const can = (permission: Permission) => {
    const role = activeWorkspace.value?.role;
    return role ? hasPermission(role, permission) : true;
  };

  return {
    apiBaseURL,
    accessToken,
    user,
    workspaces,
    activeWorkspaceId,
    activeWorkspace,
    workspaceBase,
    authenticated,
    ready,
    bootstrap,
    login,
    verifyMFA,
    adoptTokens,
    register,
    logout,
    selectWorkspace,
    updateAPIBaseURL,
    loadIdentity,
    onSessionExpired,
    can,
  };
});
