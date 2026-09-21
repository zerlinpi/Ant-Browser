import { computed, ref } from "vue";
import { defineStore } from "pinia";
import { api } from "@/api/client";
import type { User, Workspace } from "@/types";

interface TokenResponse {
  accessToken: string;
  refreshToken: string;
  accessExpiresAt?: string;
  refreshExpiresAt?: string;
  user?: User;
}

const SESSION_KEY = "ant-browser.cloud-session.v1";
const WORKSPACE_KEY = "ant-browser.active-workspace.v1";
const API_URL_KEY = "ant-browser.api-url.v1";

function readSession(): Pick<TokenResponse, "accessToken" | "refreshToken"> {
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
  const apiBaseURL = ref(localStorage.getItem(API_URL_KEY) || import.meta.env.VITE_API_BASE_URL || "http://127.0.0.1:8080");
  const accessToken = ref(initial.accessToken);
  const refreshToken = ref(initial.refreshToken);
  const user = ref<User | null>(null);
  const workspaces = ref<Workspace[]>([]);
  const activeWorkspaceId = ref(localStorage.getItem(WORKSPACE_KEY) || "");
  const ready = ref(false);
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

  const refresh = async () => {
    if (!refreshToken.value) return null;
    try {
      const pair = await api.post<TokenResponse>("/api/v1/auth/refresh", { refreshToken: refreshToken.value });
      accessToken.value = pair.accessToken;
      refreshToken.value = pair.refreshToken;
      persistSession();
      return pair.accessToken;
    } catch {
      clear();
      return null;
    }
  };

  const configureClient = () => api.configure(apiBaseURL.value, accessToken.value, refresh);

  const loadIdentity = async () => {
    user.value = await api.get<User>("/api/v1/me");
    workspaces.value = await api.get<Workspace[]>("/api/v1/workspaces");
    if (!workspaces.value.some((item) => item.id === activeWorkspaceId.value)) {
      activeWorkspaceId.value = workspaces.value[0]?.id || "";
    }
    if (activeWorkspaceId.value) localStorage.setItem(WORKSPACE_KEY, activeWorkspaceId.value);
  };

  const bootstrap = async () => {
    configureClient();
    if (accessToken.value) {
      try {
        await loadIdentity();
      } catch {
        clear();
      }
    }
    ready.value = true;
  };

  const establish = async (pair: TokenResponse) => {
    accessToken.value = pair.accessToken;
    refreshToken.value = pair.refreshToken;
    user.value = pair.user ?? null;
    persistSession();
    configureClient();
    await loadIdentity();
  };

  const login = async (email: string, password: string, apiURL: string) => {
    apiBaseURL.value = apiURL.trim().replace(/\/+$/, "");
    configureClient();
    const pair = await api.post<TokenResponse>("/api/v1/auth/login", { email, password });
    await establish(pair);
  };

  const register = async (email: string, password: string, displayName: string, apiURL: string) => {
    apiBaseURL.value = apiURL.trim().replace(/\/+$/, "");
    configureClient();
    const pair = await api.post<TokenResponse>("/api/v1/auth/register", { email, password, displayName });
    await establish(pair);
  };

  const selectWorkspace = (id: string) => {
    if (!workspaces.value.some((item) => item.id === id)) return;
    activeWorkspaceId.value = id;
    localStorage.setItem(WORKSPACE_KEY, id);
  };

  const updateAPIBaseURL = (value: string) => {
    apiBaseURL.value = value.trim().replace(/\/+$/, "");
    persistSession();
    configureClient();
  };

  const clear = () => {
    accessToken.value = "";
    refreshToken.value = "";
    user.value = null;
    workspaces.value = [];
    activeWorkspaceId.value = "";
    sessionStorage.removeItem(SESSION_KEY);
    localStorage.removeItem(WORKSPACE_KEY);
    configureClient();
  };

  const logout = async () => {
    try {
      await api.post("/api/v1/auth/logout", refreshToken.value ? { refreshToken: refreshToken.value } : undefined);
    } finally {
      clear();
    }
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
    register,
    logout,
    selectWorkspace,
    updateAPIBaseURL,
    loadIdentity,
  };
});
