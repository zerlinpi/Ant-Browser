import { createRouter, createWebHashHistory, type RouteRecordRaw } from "vue-router";
import { useSessionStore } from "@/stores/session";

const routes: RouteRecordRaw[] = [
  { path: "/login", name: "login", component: () => import("@/views/LoginView.vue"), meta: { public: true } },
  {
    path: "/",
    component: () => import("@/components/AppShell.vue"),
    children: [
      { path: "", name: "dashboard", component: () => import("@/views/DashboardView.vue") },
      { path: "workspace", name: "workspace", component: () => import("@/views/WorkspaceView.vue") },
      { path: "browsers", name: "browsers", component: () => import("@/views/BrowserManagerView.vue") },
      { path: "accounts", name: "accounts", component: () => import("@/views/AccountCenterView.vue") },
      { path: "proxies", name: "proxies", component: () => import("@/views/ProxyCenterView.vue") },
      { path: "automation", name: "automation", component: () => import("@/views/AutomationCenterView.vue") },
      { path: "tasks", name: "tasks", component: () => import("@/views/TaskCenterView.vue") },
      { path: "settings", name: "settings", component: () => import("@/views/SettingsView.vue") },
    ],
  },
  { path: "/:pathMatch(.*)*", redirect: "/" },
];

export const router = createRouter({ history: createWebHashHistory(), routes });

router.beforeEach(async (to) => {
  const session = useSessionStore();
  if (!session.ready) await session.bootstrap();
  if (!to.meta.public && !session.authenticated) return { name: "login", query: { next: to.fullPath } };
  if (to.name === "login" && session.authenticated) return { name: "dashboard" };
  return true;
});
