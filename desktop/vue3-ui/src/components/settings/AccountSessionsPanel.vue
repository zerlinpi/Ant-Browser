<template>
  <section class="panel settings-panel">
    <header class="panel-header">
      <div><h2>登录会话</h2><p>登录过当前账号的设备与浏览器。发现不认识的会话时，立即让它退出。</p></div>
      <div class="session-header-actions">
        <button class="button button-quiet small" type="button" :disabled="loading" @click="load">
          <Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新
        </button>
        <button class="button button-danger small" type="button" :disabled="loading || !otherCount" @click="askRevokeOthers">
          <Icon icon="lucide:log-out" />退出其他会话
        </button>
      </div>
    </header>

    <div v-if="error" class="alert alert-danger session-alert" role="alert">
      <Icon icon="lucide:circle-alert" /><span>{{ error }}</span>
      <button class="button button-secondary small" type="button" :disabled="loading" @click="load">重试</button>
    </div>
    <div v-if="success" class="alert alert-success session-alert" role="status"><Icon icon="lucide:circle-check" /><span>{{ success }}</span></div>

    <div v-if="sessions.length" class="table-wrap">
      <table class="data-table">
        <thead><tr><th>设备</th><th>IP 地址</th><th>登录时间</th><th>最近活动</th><th class="actions-cell">操作</th></tr></thead>
        <tbody>
          <tr v-for="item in sessions" :key="item.id">
            <td>
              <div class="session-device">
                <Icon :icon="deviceIcon(item.userAgent)" />
                <span>
                  <strong>{{ describeUserAgent(item.userAgent) }}</strong>
                  <small class="cell-subtitle" :title="item.userAgent || undefined">{{ item.current ? "当前会话" : `会话 ${shortId(item.id)}` }}</small>
                </span>
              </div>
            </td>
            <td class="mono">{{ item.ipAddress || "—" }}</td>
            <td>{{ formatDateTime(item.createdAt) }}</td>
            <td>{{ formatDateTime(item.lastSeenAt) }}</td>
            <td class="actions-cell">
              <button class="button button-quiet small" type="button" :aria-label="item.current ? '退出当前会话' : `让会话 ${shortId(item.id)} 退出`" @click="askRevoke(item)">
                <Icon icon="lucide:log-out" />{{ item.current ? "退出登录" : "退出" }}
              </button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else-if="loading" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载登录会话…</p>
    <p v-else-if="!error" class="panel-empty">暂无登录会话</p>
    <p class="form-note session-note">
      <Icon icon="lucide:info" />
      <span>会话在最后一次续期后 {{ ttlHint }} 自动失效。让会话退出后，它的访问令牌与刷新令牌立即作废，实时通知连接会在一分钟内断开。</span>
    </p>

    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { AccountSession } from "@/types";
import { formatDateTime, shortId } from "@/utils/format";

const session = useSessionStore();
const router = useRouter();
const confirm = useConfirm();
const sessions = ref<AccountSession[]>([]);
const loading = ref(false);
const error = ref("");
const success = ref("");
let loadToken = 0;

const otherCount = computed(() => sessions.value.filter((item) => !item.current).length);
const ttlHint = computed(() => {
  const current = sessions.value.find((item) => item.current);
  if (!current) return "一段时间";
  const days = Math.round((Date.parse(current.expiresAt) - Date.parse(current.lastSeenAt)) / 86_400_000);
  return days >= 1 ? `${days} 天` : "一段时间";
});

// Best-effort labels: user agents are client supplied and only ever displayed.
const operatingSystems: [RegExp, string][] = [
  [/Windows/i, "Windows"], [/iPhone|iPad|iOS/i, "iOS"], [/Android/i, "Android"],
  [/Mac OS X|Macintosh/i, "macOS"], [/CrOS/i, "ChromeOS"], [/Linux/i, "Linux"],
];
const browsers: [RegExp, string][] = [
  [/Edg\//, "Edge"], [/OPR\//, "Opera"], [/Firefox\//, "Firefox"], [/Chrome\//, "Chrome"], [/Safari\//, "Safari"],
];
const describeUserAgent = (userAgent?: string) => {
  const value = userAgent?.trim() ?? "";
  if (!value) return "未知设备";
  const os = operatingSystems.find(([pattern]) => pattern.test(value))?.[1];
  const browser = browsers.find(([pattern]) => pattern.test(value))?.[1];
  if (os && browser) return `${browser} · ${os}`;
  return os || browser || (value.length > 40 ? `${value.slice(0, 40)}…` : value);
};
const deviceIcon = (userAgent?: string) =>
  /iPhone|iPad|Android|Mobile/i.test(userAgent ?? "") ? "lucide:smartphone" : "lucide:monitor";

const load = async () => {
  const token = ++loadToken;
  loading.value = true;
  error.value = "";
  try {
    const items = await api.list<AccountSession>("/api/v1/me/sessions");
    if (token === loadToken) sessions.value = items;
  } catch (err) {
    if (token === loadToken) error.value = `登录会话加载失败：${describeError(err, "请稍后重试")}`;
  } finally {
    if (token === loadToken) loading.value = false;
  }
};

const signOutHere = async () => {
  // The server already ended this session; clear local state and leave.
  await session.logout();
  await router.replace({ name: "login" });
};

const askRevoke = (item: AccountSession) => {
  success.value = "";
  if (item.current) {
    confirm.ask({ title: "退出当前会话", message: "将退出本设备上的登录，需要重新登录后才能继续使用。", confirmText: "退出登录", danger: true }, signOutHere);
    return;
  }
  confirm.ask({
    title: "让会话退出",
    message: `「${describeUserAgent(item.userAgent)}」（${item.ipAddress || "未知 IP"}）将立即退出登录，其刷新令牌同时作废。`,
    confirmText: "退出该会话",
    danger: true,
  }, async () => {
    try {
      await api.delete(`/api/v1/me/sessions/${encodeURIComponent(item.id)}`);
    } catch (err) {
      // Already ended elsewhere: drop it from the list instead of failing.
      if (!(err instanceof ApiError && err.status === 404)) throw err;
    }
    sessions.value = sessions.value.filter((entry) => entry.id !== item.id);
    success.value = "该会话已退出登录";
  });
};

const askRevokeOthers = () => {
  success.value = "";
  confirm.ask({
    title: "退出其他会话",
    message: `除本设备外的 ${otherCount.value} 个会话将立即退出登录。`,
    confirmText: "全部退出",
    danger: true,
  }, async () => {
    const result = await api.post<{ revoked: number }>("/api/v1/me/sessions/revoke-others");
    success.value = result.revoked ? `已让 ${result.revoked} 个会话退出登录` : "没有其他需要退出的会话";
    await load();
  });
};

onMounted(() => void load());
</script>

<style scoped>
.session-header-actions {
  display: flex;
  gap: 8px;
}

.session-alert {
  margin: 0 20px 14px;
}

.session-alert .button {
  margin-left: auto;
}

.session-device {
  display: flex;
  align-items: center;
  gap: 10px;
}

.session-device > svg {
  flex: 0 0 auto;
  color: var(--muted);
}

.session-note {
  margin: 14px 20px 18px;
}
</style>
