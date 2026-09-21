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
          <RouterLink v-for="item in group.items" :key="item.to" :to="item.to" class="nav-item" :title="collapsed ? item.label : undefined">
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
        <div class="topbar-search">
          <Icon icon="lucide:search" />
          <input v-model="globalSearch" type="search" placeholder="搜索实例、账号或任务…" aria-label="全局搜索" @keydown.enter="openSearch" />
        </div>
        <div class="topbar-actions">
          <label class="workspace-select">
            <Icon icon="lucide:building-2" />
            <select :value="session.activeWorkspaceId" aria-label="切换工作空间" @change="changeWorkspace">
              <option v-for="workspace in session.workspaces" :key="workspace.id" :value="workspace.id">{{ workspace.name }}</option>
            </select>
          </label>
          <div class="notification-wrap">
            <button class="icon-button" type="button" aria-label="通知" @click="notificationsOpen = !notificationsOpen">
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
                  <span class="notification-icon"><Icon :icon="notificationIcon(item.type)" /></span>
                  <span><strong>{{ item.title }}</strong><small>{{ item.message }}</small></span>
                </button>
                <p v-if="!notifications.length" class="notification-empty">暂无通知</p>
              </div>
            </div>
          </div>
          <RouterLink class="icon-button" to="/settings" aria-label="设置"><Icon icon="lucide:settings" /></RouterLink>
          <button class="profile-button" type="button" @click="session.logout().then(() => router.push('/login'))">
            <span class="profile-avatar"><Icon icon="lucide:user" /></span>
            <span v-if="!compactHeader">{{ session.user?.displayName || session.user?.email || "用户" }}</span>
          </button>
        </div>
      </header>

      <main class="content"><RouterView :key="session.activeWorkspaceId" /></main>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { RouterLink, RouterView, useRouter } from "vue-router";
import { api } from "@/api/client";
import { NotificationChannel } from "@/services/notifications";
import { useSessionStore } from "@/stores/session";
import type { NotificationItem } from "@/types";
import logoURL from "../../../../frontend/src/resources/images/logo.png";

const router = useRouter();
const session = useSessionStore();
const collapsed = ref(false);
const compactHeader = ref(false);
const globalSearch = ref("");
const notificationsOpen = ref(false);
const notifications = ref<NotificationItem[]>([]);
let channel: NotificationChannel | undefined;

const navigation = [
  { label: "工作台", items: [
    { to: "/", label: "控制台", icon: "lucide:layout-dashboard" },
    { to: "/workspace", label: "团队空间", icon: "lucide:users" },
  ] },
  { label: "跨境运营", items: [
    { to: "/browsers", label: "浏览器实例", icon: "lucide:monitor" },
    { to: "/accounts", label: "账号中心", icon: "lucide:badge-user" },
    { to: "/proxies", label: "代理中心", icon: "lucide:globe-2" },
    { to: "/automation", label: "自动化", icon: "lucide:workflow" },
    { to: "/tasks", label: "任务中心", icon: "lucide:list-checks" },
  ] },
  { label: "系统", items: [
    { to: "/settings", label: "系统设置", icon: "lucide:settings" },
  ] },
];

const unreadCount = computed(() => notifications.value.filter((item) => !item.readAt).length);
const workspaceBase = computed(() => session.workspaceBase);

const loadNotifications = async () => {
  if (!workspaceBase.value) return;
  notifications.value = await api.get<NotificationItem[]>(`${workspaceBase.value}/notifications?limit=20`).catch(() => []);
};

const connectNotifications = async () => {
  channel?.close();
  if (!session.activeWorkspaceId) return;
  await loadNotifications();
  channel = new NotificationChannel(session.apiBaseURL, session.activeWorkspaceId, (item) => {
    notifications.value = [item, ...notifications.value.filter((current) => current.id !== item.id)].slice(0, 50);
  });
  void channel.connect();
};

watch(() => session.activeWorkspaceId, () => void connectNotifications(), { immediate: true });
onBeforeUnmount(() => channel?.close());

const changeWorkspace = (event: Event) => session.selectWorkspace((event.target as HTMLSelectElement).value);
const openSearch = () => {
  if (!globalSearch.value.trim()) return;
  void router.push({ name: "browsers", query: { q: globalSearch.value.trim() } });
};
const markRead = async (item: NotificationItem) => {
  if (!item.readAt) {
    await api.post(`${workspaceBase.value}/notifications/${item.id}/read`).catch(() => undefined);
    item.readAt = new Date().toISOString();
  }
};
const markAllRead = async () => {
  await api.post(`${workspaceBase.value}/notifications/read-all`).catch(() => undefined);
  const now = new Date().toISOString();
  notifications.value.forEach((item) => { item.readAt ||= now; });
};
const notificationIcon = (type: string) => type.includes("risk") || type.includes("failed")
  ? "lucide:triangle-alert"
  : type.includes("proxy") ? "lucide:globe-2" : "lucide:info";
</script>
