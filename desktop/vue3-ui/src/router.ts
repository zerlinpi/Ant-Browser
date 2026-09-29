import { createRouter, createWebHashHistory, type RouteRecordRaw } from "vue-router";
import { useSessionStore } from "@/stores/session";

declare module "vue-router" {
  interface RouteMeta {
    public?: boolean;
    /** The view calls workspace-scoped endpoints; AppShell prompts for a workspace when none is active. */
    requiresWorkspace?: boolean;
  }
}

const routes: RouteRecordRaw[] = [
  { path: "/login", name: "login", component: () => import("@/views/LoginView.vue"), meta: { public: true } },
  {
    path: "/",
    component: () => import("@/components/AppShell.vue"),
    children: [
      { path: "", name: "dashboard", component: () => import("@/views/DashboardView.vue"), meta: { requiresWorkspace: true } },
      { path: "workspace", name: "workspace", component: () => import("@/views/WorkspaceView.vue") },
      { path: "browsers", name: "browsers", component: () => import("@/views/BrowserManagerView.vue"), meta: { requiresWorkspace: true } },
      { path: "profiles", name: "profiles", component: () => import("@/views/ProfilesView.vue"), meta: { requiresWorkspace: true } },
      { path: "profiles/:profileId", name: "profile-detail", component: () => import("@/views/ProfileDetailView.vue"), meta: { requiresWorkspace: true } },
      { path: "accounts", name: "accounts", component: () => import("@/views/AccountCenterView.vue"), meta: { requiresWorkspace: true } },
      { path: "accounts/:accountId", name: "account-detail", component: () => import("@/views/AccountDetailView.vue"), meta: { requiresWorkspace: true } },
      { path: "proxies", name: "proxies", component: () => import("@/views/ProxyCenterView.vue"), meta: { requiresWorkspace: true } },
      { path: "fingerprints", name: "fingerprints", component: () => import("@/views/FingerprintCenterView.vue"), meta: { requiresWorkspace: true } },
      { path: "fingerprints/new", name: "fingerprint-new", component: () => import("@/views/FingerprintTemplateEditorView.vue"), meta: { requiresWorkspace: true } },
      { path: "fingerprints/:templateId", name: "fingerprint-edit", component: () => import("@/views/FingerprintTemplateEditorView.vue"), meta: { requiresWorkspace: true } },
      { path: "automation", name: "automation", component: () => import("@/views/AutomationCenterView.vue"), meta: { requiresWorkspace: true } },
      { path: "automation/new", name: "automation-new", component: () => import("@/views/WorkflowBuilderView.vue"), meta: { requiresWorkspace: true } },
      { path: "automation/:workflowId", name: "automation-edit", component: () => import("@/views/WorkflowBuilderView.vue"), meta: { requiresWorkspace: true } },
      { path: "schedules", name: "schedules", component: () => import("@/views/SchedulerView.vue"), meta: { requiresWorkspace: true } },
      { path: "tasks", name: "tasks", component: () => import("@/views/TaskCenterView.vue"), meta: { requiresWorkspace: true } },
      { path: "devices", name: "devices", component: () => import("@/views/DevicesView.vue") },
      { path: "settings", name: "settings", component: () => import("@/views/SettingsView.vue") },
    ],
  },
  { path: "/:pathMatch(.*)*", redirect: "/" },
];

export const router = createRouter({ history: createWebHashHistory(), routes });

let sessionExpiryWired = false;

router.beforeEach(async (to) => {
  const session = useSessionStore();
  if (!sessionExpiryWired) {
    // Registered lazily because Pinia is installed after this module loads.
    sessionExpiryWired = true;
    session.onSessionExpired(() => {
      // During bootstrap the guard below performs the redirect itself.
      if (!session.ready) return;
      const current = router.currentRoute.value;
      if (current.meta.public) return;
      void router.replace({ name: "login", query: { next: current.fullPath } });
    });
  }
  if (!session.ready) await session.bootstrap();
  if (!to.meta.public && !session.authenticated) return { name: "login", query: { next: to.fullPath } };
  if (to.name === "login" && session.authenticated) return { name: "dashboard" };
  return true;
});
