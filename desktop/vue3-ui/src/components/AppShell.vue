<template>
  <div class="app-shell" :class="{ 'sidebar-collapsed': collapsed }">
    <aside class="sidebar">
      <RouterLink class="brand" to="/" aria-label="Ant Browser 控制台">
        <img :src="logoURL" alt="" />
        <span v-if="!collapsed">Ant Browser</span>
      </RouterLink>

      <nav class="nav-scroll" aria-label="主导航">
        <section v-for="group in navigation" :key="group.label" class="nav-group">
          <p v-if="!collapsed" class="nav-label">{{ group.label }}</p>
          <RouterLink
            v-for="item in group.items"
            :key="item.to"
            :to="item.to"
            class="nav-item"
            :class="{ 'is-active': isNavActive(item.to) }"
            :aria-current="isNavActive(item.to) ? 'page' : undefined"
            :title="collapsed ? item.label : undefined"
          >
            <Icon :icon="item.icon" />
            <span v-if="!collapsed">{{ item.label }}</span>
          </RouterLink>
        </section>
      </nav>

      <button class="collapse-button" type="button" @click="collapsed = !collapsed">
        <Icon :icon="collapsed ? 'lucide:panel-left-open' : 'lucide:panel-left-close'" />
        <span v-if="!collapsed">收起侧边栏</span>
      </button>
    </aside>

    <div class="app-main">
      <header class="topbar">
        <form class="topbar-search" role="search" @submit.prevent="openSearch">
          <Icon icon="lucide:search" />
          <input v-model="globalSearch" type="search" placeholder="搜索浏览器实例名称或标签…" aria-label="搜索浏览器实例" />
        </form>
        <div class="topbar-actions">
          <label class="workspace-select">
            <Icon icon="lucide:building-2" />
            <select :value="session.activeWorkspaceId" aria-label="切换工作空间" @change="changeWorkspace">
              <option v-if="!session.workspaces.length" value="" disabled>暂无工作空间</option>
              <option v-for="workspace in session.workspaces" :key="workspace.id" :value="workspace.id">{{ workspace.name }}</option>
            </select>
          </label>
          <div ref="notificationRef" class="notification-wrap">
            <button class="icon-button" type="button" aria-label="通知" :aria-expanded="notificationsOpen" @click="notificationsOpen = !notificationsOpen">
              <Icon icon="lucide:bell" />
              <span v-if="unreadCount" class="notification-count">{{ unreadCount > 9 ? "9+" : unreadCount }}</span>
            </button>
            <div v-if="notificationsOpen" class="notification-panel">
              <div class="notification-header">
                <strong>通知</strong>
                <button v-if="unreadCount" type="button" @click="markAllRead">全部已读</button>
              </div>
              <div class="notification-list">
                <button v-for="item in notifications" :key="item.id" class="notification-item" :class="{ unread: !item.readAt }" type="button" @click="markRead(item)">
                  <span class="notification-icon"><Icon :icon="notificationIcon(item.eventType)" /></span>
                  <span><strong>{{ item.title }}</strong><small>{{ item.body }}</small></span>
                </button>
                <p v-if="!notifications.length" class="notification-empty">暂无通知</p>
              </div>
            </div>
          </div>
          <RouterLink class="icon-button" to="/settings" aria-label="设置"><Icon icon="lucide:settings" /></RouterLink>
          <div ref="userMenuRef" class="user-menu-wrap">
            <button class="profile-button" type="button" aria-haspopup="menu" :aria-expanded="userMenuOpen" @click="userMenuOpen = !userMenuOpen">
              <span class="profile-avatar"><Icon icon="lucide:user" /></span>
              <span>{{ displayName }}</span>
              <Icon icon="lucide:chevron-down" />
            </button>
            <div v-if="userMenuOpen" class="menu-panel user-menu" role="menu" aria-label="用户菜单">
              <div class="menu-header">
                <strong>{{ session.user?.displayName || "当前用户" }}</strong>
                <small>{{ session.user?.email || "—" }}</small>
              </div>
              <button class="menu-item danger" type="button" role="menuitem" :disabled="loggingOut" @click="logout">
                <Icon :icon="loggingOut ? 'lucide:loader-circle' : 'lucide:log-out'" :class="{ spin: loggingOut }" />
                <span>{{ loggingOut ? "正在退出…" : "退出登录" }}</span>
              </button>
            </div>
          </div>
        </div>
      </header>

      <main class="content">
        <div v-if="workspaceMissing" class="page-stack">
          <article class="panel">
            <EmptyState icon="lucide:building-2" title="请先选择或创建工作空间" description="实例、账号、代理与任务数据按工作空间隔离。可新建工作空间，或使用邀请码加入团队。">
              <RouterLink class="button button-primary" to="/workspace">前往团队空间</RouterLink>
            </EmptyState>
          </article>
        </div>
        <RouterView v-else :key="session.activeWorkspaceId" />
      </main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { RouterLink, RouterView, useRoute, useRouter } from "vue-router";
import { api } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import { NotificationChannel } from "@/services/notifications";
import { useSessionStore } from "@/stores/session";
import type { NotificationItem, NotificationPage } from "@/types";
import logoURL from "../../../../frontend/src/resources/images/logo.png";

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const collapsed = ref(false);
const globalSearch = ref("");
const notificationsOpen = ref(false);
const notifications = ref<NotificationItem[]>([]);
const unreadCount = ref(0);
const userMenuOpen = ref(false);
const loggingOut = ref(false);
const userMenuRef = ref<HTMLElement>();
const notificationRef = ref<HTMLElement>();
let channel: NotificationChannel | undefined;

const navigation = [
  { label: "工作台", items: [
    { to: "/", label: "控制台", icon: "lucide:layout-dashboard" },
    { to: "/workspace", label: "团队空间", icon: "lucide:users" },
  ] },
  { label: "跨境运营", items: [
    { to: "/browsers", label: "浏览器实例", icon: "lucide:monitor" },
    { to: "/profiles", label: "云端档案", icon: "lucide:cloud" },
    { to: "/accounts", label: "账号中心", icon: "lucide:contact-round" },
    { to: "/proxies", label: "代理中心", icon: "lucide:globe-2" },
    { to: "/fingerprints", label: "指纹中心", icon: "lucide:fingerprint" },
    { to: "/automation", label: "自动化", icon: "lucide:workflow" },
    { to: "/schedules", label: "任务调度", icon: "lucide:calendar-clock" },
    { to: "/tasks", label: "任务中心", icon: "lucide:list-checks" },
  ] },
  { label: "系统", items: [
    { to: "/devices", label: "设备管理", icon: "lucide:hard-drive" },
    { to: "/settings", label: "系统设置", icon: "lucide:settings" },
  ] },
];

const displayName = computed(() => session.user?.displayName || session.user?.email || "用户");
const workspaceMissing = computed(() => Boolean(route.meta.requiresWorkspace) && !session.activeWorkspaceId);
// Detail and editor routes (for example /accounts/:id) keep their section highlighted.
const isNavActive = (to: string) => to === "/" ? route.path === "/" : route.path === to || route.path.startsWith(`${to}/`);

watch(() => [route.name, route.query.q] as const, ([name, q]) => {
  if (name === "browsers") globalSearch.value = typeof q === "string" ? q : "";
}, { immediate: true });

const openSearch = () => {
  const q = globalSearch.value.trim();
  if (!q && route.name !== "browsers") return;
  void router.push({ name: "browsers", query: q ? { q } : {} });
};

const loadNotifications = async () => {
  if (!session.workspaceBase) return;
  const page = await api.get<NotificationPage>(`${session.workspaceBase}/notifications?limit=20`).catch(() => undefined);
  notifications.value = page?.items ?? [];
  unreadCount.value = page?.unreadCount ?? 0;
};

const connectNotifications = async () => {
  channel?.close();
  if (!session.activeWorkspaceId) {
    notifications.value = [];
    unreadCount.value = 0;
    return;
  }
  await loadNotifications();
  channel = new NotificationChannel(session.apiBaseURL, session.activeWorkspaceId, (item) => {
    const existing = notifications.value.find((current) => current.id === item.id);
    if (!item.readAt && (!existing || existing.readAt)) unreadCount.value += 1;
    notifications.value = [item, ...notifications.value.filter((current) => current.id !== item.id)].slice(0, 50);
  });
  void channel.connect();
};

watch(() => session.activeWorkspaceId, () => void connectNotifications(), { immediate: true });

const onDocumentPointer = (event: MouseEvent) => {
  const target = event.target as Node;
  if (userMenuOpen.value && !userMenuRef.value?.contains(target)) userMenuOpen.value = false;
  if (notificationsOpen.value && !notificationRef.value?.contains(target)) notificationsOpen.value = false;
};
const onDocumentKeydown = (event: KeyboardEvent) => {
  if (event.key !== "Escape") return;
  userMenuOpen.value = false;
  notificationsOpen.value = false;
};
onMounted(() => {
  document.addEventListener("mousedown", onDocumentPointer);
  document.addEventListener("keydown", onDocumentKeydown);
});
onBeforeUnmount(() => {
  document.removeEventListener("mousedown", onDocumentPointer);
  document.removeEventListener("keydown", onDocumentKeydown);
  channel?.close();
});

const changeWorkspace = (event: Event) => session.selectWorkspace((event.target as HTMLSelectElement).value);

const logout = async () => {
  loggingOut.value = true;
  try {
    await session.logout();
    userMenuOpen.value = false;
    await router.replace({ name: "login" });
  } finally {
    loggingOut.value = false;
  }
};

const markRead = async (item: NotificationItem) => {
  if (!item.readAt) {
    try {
      await api.post(`${session.workspaceBase}/notifications/${item.id}/read`);
      item.readAt = new Date().toISOString();
      unreadCount.value = Math.max(0, unreadCount.value - 1);
    } catch {
      // Keep the unread state so a later retry remains possible.
    }
  }
};
const markAllRead = async () => {
  try {
    await api.post(`${session.workspaceBase}/notifications/read-all`);
    const now = new Date().toISOString();
    notifications.value.forEach((item) => { item.readAt ||= now; });
    unreadCount.value = 0;
  } catch {
    // Preserve server state on failure.
  }
};
const notificationIcon = (type: string) => type.includes("risk") || type.includes("failed")
  ? "lucide:triangle-alert"
  : type.includes("proxy") ? "lucide:globe-2" : "lucide:info";
</script>
