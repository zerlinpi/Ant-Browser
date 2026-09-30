<template>
  <div class="page-stack settings-page">
    <PageHeader title="系统设置" description="管理 Cloud 连接、账号安全、通知偏好与商业授权" />

    <nav class="settings-tabs" aria-label="设置分类">
      <button v-for="item in tabs" :key="item.id" :class="{ active: tab === item.id }" type="button" :aria-pressed="tab === item.id" @click="tab = item.id">
        <Icon :icon="item.icon" />{{ item.label }}
      </button>
    </nav>

    <div v-if="feedback[tab].error" class="alert alert-danger" role="alert">
      <Icon icon="lucide:circle-alert" /><span>{{ feedback[tab].error }}</span>
    </div>
    <div v-if="feedback[tab].success" class="alert alert-success" role="status">
      <Icon icon="lucide:circle-check" /><span>{{ feedback[tab].success }}</span>
    </div>

    <section v-if="tab === 'connection'" class="panel settings-panel">
      <header class="panel-header">
        <div><h2>Cloud 连接</h2><p>桌面端通过安全 API 与 WebSocket 连接控制面。</p></div>
      </header>
      <form class="settings-form" @submit.prevent="saveConnection">
        <label class="field">
          <span>Cloud API 地址</span>
          <input v-model.trim="apiURL" type="url" inputmode="url" spellcheck="false" placeholder="https://control.example.com" required />
          <small>修改地址会退出当前登录。生产环境应使用受信任的 HTTPS 域名。</small>
        </label>
        <div class="settings-row">
          <div>
            <strong>连接状态</strong>
            <p>{{ session.apiBaseURL }}<template v-if="probe.checkedAt"> · 检测于 {{ formatFullDateTime(probe.checkedAt) }}</template></p>
            <p v-if="probe.detail">{{ probe.detail }}</p>
          </div>
          <div class="probe-actions">
            <span role="status" aria-live="polite"><StatusBadge :status="probeBadge.status" :label="probeBadge.label" /></span>
            <button class="button button-secondary small" type="button" :disabled="probing" @click="checkConnection">
              <Icon icon="lucide:refresh-cw" :class="{ spin: probing }" />{{ probing ? "检测中…" : "重新检测" }}
            </button>
          </div>
        </div>
        <div class="settings-row">
          <div><strong>设备管理</strong><p>查看已注册的桌面设备，吊销丢失或停用设备的凭据。</p></div>
          <RouterLink class="button button-secondary" :to="{ name: 'devices' }"><Icon icon="lucide:hard-drive" />设备管理</RouterLink>
        </div>
        <div class="settings-actions"><button class="button button-primary" type="submit">保存连接</button></div>
      </form>
    </section>

    <div v-else-if="tab === 'security'" class="security-stack">
      <TwoFactorPanel />
      <AccountSessionsPanel />
    </div>

    <section v-else-if="tab === 'notifications'" class="panel settings-panel">
      <header class="panel-header">
        <div><h2>通知偏好</h2><p>每类事件可以覆盖默认的实时与邮件投递渠道。</p></div>
      </header>
      <EmptyState v-if="!session.workspaceBase" icon="lucide:building-2" title="请先选择工作空间" description="通知偏好按工作空间分别保存。" />
      <template v-else>
        <div class="preference-list">
          <div class="preference-heading" aria-hidden="true"><span></span><span>事件类型</span><span>实时</span><span>邮件</span></div>
          <div v-for="item in notificationEvents" :key="item.eventType" class="preference-row preference-grid">
            <span class="preference-icon"><Icon :icon="item.icon" /></span>
            <span><strong>{{ item.label }}</strong><small>{{ item.description }}</small></span>
            <input v-model="preferences[item.eventType].websocket" type="checkbox" :disabled="!preferencesReady || savingPreferences" :aria-label="`${item.label}实时通知`" />
            <input v-model="preferences[item.eventType].email" type="checkbox" :disabled="!preferencesReady || savingPreferences" :aria-label="`${item.label}邮件通知`" />
          </div>
        </div>
        <div class="settings-actions">
          <button v-if="!preferencesReady" class="button button-secondary" type="button" :disabled="loadingPreferences" @click="loadPreferences">
            {{ loadingPreferences ? "加载中…" : "重新加载" }}
          </button>
          <button class="button button-primary" type="button" :disabled="!preferencesReady || savingPreferences" @click="savePreferences">
            {{ savingPreferences ? "保存中…" : "保存偏好" }}
          </button>
        </div>
      </template>
    </section>

    <section v-else class="settings-billing-grid">
      <article v-if="!orgId" class="panel billing-wide">
        <EmptyState icon="lucide:building-2" title="请先选择工作空间" description="订阅与配额按工作空间所属组织显示。" />
      </article>
      <article v-else-if="!session.can('billing.read')" class="panel billing-wide">
        <EmptyState icon="lucide:lock" title="无权查看订阅与配额" description="需要管理员或所有者角色。" />
      </article>
      <template v-else>
        <article class="panel plan-card">
          <p class="page-eyebrow">CURRENT PLAN</p>
          <h2>{{ planName }}</h2>
          <p>组织内共享实例、席位、自动化、存储与 API 配额。</p>
          <StatusBadge :status="subscriptionKnown ? subscription?.status || 'inactive' : 'unknown'" :label="subscriptionLabel" />
        </article>

        <article class="panel entitlement-panel">
          <header class="panel-header"><div><h2>组织配额</h2><p>授权服务不可用时，受限资源默认拒绝。</p></div></header>
          <div class="entitlement-list">
            <div v-for="item in entitlements" :key="item.code" class="entitlement-row">
              <div><strong>{{ labelOf(item.code) }}</strong><small>{{ usageText(item) }}</small></div>
              <div class="quota-bar"><i :style="{ width: `${percentOf(item)}%` }" /></div>
            </div>
          </div>
          <EmptyState v-if="!entitlements.length && !loadingBilling" title="暂无生效配额" description="当前组织没有生效中的授权配额。" />
        </article>

        <article class="panel license-panel">
          <header class="panel-header"><div><h2>激活商业授权</h2><p>许可证令牌会在服务端校验并仅以摘要形式存储。</p></div></header>
          <template v-if="session.can('billing.manage')">
            <label class="field">
              <span>许可证令牌</span>
              <input v-model.trim="licenseToken" type="password" autocomplete="off" placeholder="粘贴许可证令牌" @keydown.enter.prevent="activateLicense" />
            </label>
            <button class="button button-primary" type="button" :disabled="activating || !licenseToken" @click="activateLicense">
              {{ activating ? "激活中…" : "激活" }}
            </button>
          </template>
          <p v-else class="license-note muted-text">仅所有者可以激活商业授权。</p>
        </article>
      </template>
    </section>

    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from "vue";
import { RouterLink, useRouter } from "vue-router";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import AccountSessionsPanel from "@/components/settings/AccountSessionsPanel.vue";
import TwoFactorPanel from "@/components/settings/TwoFactorPanel.vue";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { BillingPlan, BillingSubscription, Entitlement, NotificationPreference } from "@/types";
import { formatFullDateTime } from "@/utils/format";

type TabId = "connection" | "security" | "notifications" | "billing";
type DeliveryChannel = "websocket" | "email";
type PreferenceState = Record<string, Record<DeliveryChannel, boolean>>;
type ProbeState = "checking" | "ready" | "not_ready" | "unreachable";

const PROBE_TIMEOUT_MS = 5000;

const notificationEvents = [
  { eventType: "*", label: "默认通知", description: "没有单独配置的其他事件", icon: "lucide:bell" },
  { eventType: "account.risk_event", label: "账号风险", description: "异常、冻结与验证事件", icon: "lucide:badge-alert" },
  { eventType: "proxy.health_failed", label: "代理失效", description: "真实出口与 IP 健康异常", icon: "lucide:globe-lock" },
  { eventType: "task.failed", label: "任务失败", description: "重试耗尽与死信事件", icon: "lucide:list-x" },
  { eventType: "security.event", label: "安全事件", description: "登录、权限与敏感操作", icon: "lucide:shield-alert" },
] as const;
const channels: readonly DeliveryChannel[] = ["websocket", "email"];

const tabs: { id: TabId; label: string; icon: string }[] = [
  { id: "connection", label: "Cloud 连接", icon: "lucide:cloud" },
  { id: "security", label: "账号安全", icon: "lucide:shield-check" },
  { id: "notifications", label: "通知", icon: "lucide:bell" },
  { id: "billing", label: "订阅与授权", icon: "lucide:badge-check" },
];

const probeBadges: Record<ProbeState, { status: string; label: string }> = {
  checking: { status: "pending", label: "检测中…" },
  ready: { status: "ready", label: "就绪" },
  not_ready: { status: "warning", label: "未就绪" },
  unreachable: { status: "unreachable", label: "无法连接" },
};

// billingservice license errors are not localized by the shared client.
const licenseErrors: Record<string, string> = {
  license_invalid: "许可证令牌无效",
  license_expired: "许可证已过期",
  license_revoked: "许可证已被吊销",
  license_scope_mismatch: "许可证与当前组织不匹配",
  billing_validation_failed: "许可证令牌格式无效",
  billing_state_conflict: "许可证状态冲突，可能已被激活",
};

const session = useSessionStore();
const router = useRouter();
const confirm = useConfirm();
const tab = ref<TabId>("connection");
const feedback = reactive<Record<TabId, { error: string; success: string }>>({
  connection: { error: "", success: "" },
  security: { error: "", success: "" },
  notifications: { error: "", success: "" },
  billing: { error: "", success: "" },
});
const clearFeedback = (id: TabId) => {
  feedback[id].error = "";
  feedback[id].success = "";
};

// ---- Cloud connection ----

const apiURL = ref(session.apiBaseURL);
const probing = ref(false);
const probe = reactive<{ state: ProbeState; detail: string; checkedAt: string }>({ state: "checking", detail: "", checkedAt: "" });
const probeBadge = computed(() => probeBadges[probe.state]);
let probeRequest: AbortController | undefined;
let disposed = false;

/** Canonical control-plane base URL (origin + path, no trailing slash) or a validation message. */
const canonicalAPIURL = (value: string): { url?: string; error?: string } => {
  let url: URL;
  try {
    url = new URL(value.trim());
  } catch {
    return { error: "请输入完整地址，例如 https://control.example.com" };
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return { error: "Cloud API 地址必须使用 HTTP 或 HTTPS" };
  if (url.username || url.password || url.search || url.hash) return { error: "地址不能包含账号信息、查询参数或锚点" };
  return { url: `${url.origin}${url.pathname}`.replace(/\/+$/, "") };
};

/** Unauthenticated GET; resolves to the HTTP status, or undefined when no response arrived (network, CORS or timeout). */
const fetchStatus = async (base: string, path: string): Promise<number | undefined> => {
  const controller = new AbortController();
  probeRequest = controller;
  const timer = window.setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);
  try {
    // No custom headers: a simple request needs no CORS preflight. The gateway sends Cache-Control: no-store.
    const response = await fetch(`${base}${path}`, { credentials: "omit", signal: controller.signal });
    return response.status;
  } catch {
    return undefined;
  } finally {
    window.clearTimeout(timer);
  }
};

const isSuccess = (status: number) => status >= 200 && status < 300;

const checkConnection = async () => {
  if (probing.value) return;
  probing.value = true;
  const base = session.apiBaseURL;
  let result: { state: ProbeState; detail: string };
  const ready = await fetchStatus(base, "/readyz");
  if (ready !== undefined) {
    result = isSuccess(ready)
      ? { state: "ready", detail: "控制面与依赖服务可用" }
      : { state: "not_ready", detail: ready === 503 ? "控制面依赖服务暂不可用（HTTP 503）" : `就绪检查返回 HTTP ${ready}` };
  } else {
    // /healthz only proves the process answers; it is consulted when /readyz got no response.
    const alive = await fetchStatus(base, "/healthz");
    if (alive === undefined) result = { state: "unreachable", detail: "请检查地址与网络，或确认服务端允许当前客户端来源" };
    else if (isSuccess(alive)) result = { state: "ready", detail: "服务在线（就绪检查无响应，已改用存活检查）" };
    else result = { state: "not_ready", detail: `存活检查返回 HTTP ${alive}` };
  }
  if (disposed) return;
  Object.assign(probe, result, { checkedAt: new Date().toISOString() });
  probing.value = false;
};

const saveConnection = () => {
  clearFeedback("connection");
  const { url, error } = canonicalAPIURL(apiURL.value);
  if (!url) {
    feedback.connection.error = error ?? "Cloud API 地址无效";
    return;
  }
  apiURL.value = url;
  if (url === session.apiBaseURL) {
    feedback.connection.success = "Cloud API 地址未变化";
    return;
  }
  confirm.ask({
    title: "切换 Cloud API 地址",
    message: `将切换到 ${url}。\n当前登录会退出，需要在新地址重新登录。`,
    confirmText: "切换并重新登录",
  }, async () => {
    // The session is cleared when the address changes; tokens belong to the previous server.
    if (session.updateAPIBaseURL(url)) await router.push({ name: "login" });
  });
};

// ---- Notification preferences ----

const loadingPreferences = ref(false);
const savingPreferences = ref(false);
/** Saving is blocked until the stored preferences were read, so defaults never overwrite them. */
const preferencesReady = ref(false);
const preferences = reactive<PreferenceState>(Object.fromEntries(
  notificationEvents.map((item) => [item.eventType, { websocket: true, email: false }]),
));

const applyPreferences = (items: NotificationPreference[]) => {
  const wildcard = { websocket: true, email: false };
  for (const channel of channels) {
    const saved = items.find((item) => item.channel === channel && item.eventType === "*");
    wildcard[channel] = saved?.enabled ?? wildcard[channel];
  }
  for (const event of notificationEvents) {
    for (const channel of channels) {
      const saved = items.find((item) => item.channel === channel && item.eventType === event.eventType);
      preferences[event.eventType][channel] = saved?.enabled ?? wildcard[channel];
    }
  }
};

const loadPreferences = async () => {
  const base = session.workspaceBase;
  if (!base || loadingPreferences.value) return;
  loadingPreferences.value = true;
  feedback.notifications.error = "";
  try {
    applyPreferences(await api.list<NotificationPreference>(`${base}/notification-preferences`));
    preferencesReady.value = true;
  } catch (error) {
    feedback.notifications.error = `通知偏好加载失败：${describeError(error, "请稍后重试")}`;
  } finally {
    loadingPreferences.value = false;
  }
};

const savePreferences = async () => {
  const base = session.workspaceBase;
  if (!base || !preferencesReady.value || savingPreferences.value) return;
  savingPreferences.value = true;
  clearFeedback("notifications");
  const payload = notificationEvents.flatMap((event) => channels.map((channel) => ({
    channel,
    eventType: event.eventType,
    enabled: preferences[event.eventType][channel],
  })));
  try {
    const saved = await api.put<NotificationPreference[] | null>(`${base}/notification-preferences`, { preferences: payload });
    if (Array.isArray(saved)) applyPreferences(saved);
    feedback.notifications.success = "通知偏好已保存";
  } catch (error) {
    feedback.notifications.error = describeError(error, "通知偏好保存失败，请稍后重试");
  } finally {
    savingPreferences.value = false;
  }
};

// ---- Subscription and licensing ----

const loadingBilling = ref(false);
const activating = ref(false);
const licenseToken = ref("");
const subscription = ref<BillingSubscription | null>(null);
/** False until the subscription (or its absence) is confirmed, so a failed load never reads as Free. */
const subscriptionKnown = ref(false);
const plans = ref<BillingPlan[]>([]);
const entitlements = ref<Entitlement[]>([]);

const orgId = computed(() => session.activeWorkspace?.organizationId || "");
const planName = computed(() => {
  if (!subscriptionKnown.value) return "—";
  if (!subscription.value) return "Free";
  const plan = plans.value.find((item) => item.id === subscription.value?.planId);
  return plan?.name || plan?.code || "未知套餐";
});
const subscriptionLabel = computed(() => {
  if (!subscriptionKnown.value) return loadingBilling.value ? "加载中…" : "未知";
  if (!subscription.value) return "免费版";
  const labels: Record<string, string> = { trialing: "试用中", active: "生效中", past_due: "待续费" };
  return labels[subscription.value.status] || subscription.value.status;
});

const labelOf = (code: string) => ({
  instances: "浏览器实例",
  team_members: "团队席位",
  automation_runs: "自动化次数",
  storage_bytes: "云端存储",
  api_calls: "API 调用",
  commercial_license: "商业授权",
}[code] || code);

const usageText = (item: Entitlement) => item.limit === undefined
  ? (item.featureEnabled ? "已启用 · 不限量" : "未启用")
  : `${item.consumed} / ${item.limit}`;

const percentOf = (item: Entitlement) => {
  if (!item.featureEnabled) return 0;
  if (item.limit === undefined) return 100;
  if (item.limit <= 0) return item.consumed > 0 ? 100 : 0;
  return Math.min(100, (item.consumed / item.limit) * 100);
};

const loadBilling = async () => {
  const organization = orgId.value;
  if (!organization || !session.can("billing.read") || loadingBilling.value) return;
  loadingBilling.value = true;
  feedback.billing.error = "";
  const base = `/api/v1/organizations/${organization}/billing`;
  const [planResult, subscriptionResult, entitlementResult] = await Promise.allSettled([
    api.list<BillingPlan>("/api/v1/billing/plans"),
    api.get<BillingSubscription>(`${base}/subscription`),
    api.list<Entitlement>(`${base}/entitlements`),
  ]);
  const failures: string[] = [];
  if (planResult.status === "fulfilled") plans.value = planResult.value;
  else failures.push(`套餐目录：${describeError(planResult.reason, "加载失败")}`);
  if (subscriptionResult.status === "fulfilled") {
    subscription.value = subscriptionResult.value;
    subscriptionKnown.value = true;
  } else if (subscriptionResult.reason instanceof ApiError && subscriptionResult.reason.code === "billing_not_found") {
    // The server answers 404 billing_not_found when the organization has no active subscription (Free).
    subscription.value = null;
    subscriptionKnown.value = true;
  } else {
    failures.push(`订阅：${describeError(subscriptionResult.reason, "加载失败")}`);
  }
  if (entitlementResult.status === "fulfilled") entitlements.value = entitlementResult.value;
  else failures.push(`配额：${describeError(entitlementResult.reason, "加载失败")}`);
  if (failures.length) feedback.billing.error = `部分授权信息未能加载。${failures.join("；")}`;
  loadingBilling.value = false;
};

const activateLicense = async () => {
  const organization = orgId.value;
  const token = licenseToken.value.trim();
  if (!organization || !token || activating.value) return;
  activating.value = true;
  clearFeedback("billing");
  try {
    await api.post(`/api/v1/organizations/${organization}/billing/licenses/activate`, { token });
    licenseToken.value = "";
    await loadBilling();
    feedback.billing.success = "商业授权已激活";
  } catch (error) {
    feedback.billing.error = describeError(error, "商业授权激活失败，请稍后重试", licenseErrors);
  } finally {
    activating.value = false;
  }
};

onMounted(() => {
  void checkConnection();
  void loadPreferences();
  void loadBilling();
});

onBeforeUnmount(() => {
  disposed = true;
  probeRequest?.abort();
});
</script>

<style scoped>
.preference-heading,
.preference-grid {
  display: grid;
  grid-template-columns: 36px minmax(0, 1fr) 52px 52px;
  align-items: center;
  gap: 12px;
}

.preference-heading {
  padding: 10px 0 6px;
  color: var(--muted);
  font-size: 11px;
  font-weight: 700;
  text-align: center;
}

.preference-heading span:nth-child(2) {
  text-align: left;
}

.preference-grid input {
  justify-self: center;
}

.probe-actions {
  display: flex;
  flex: 0 0 auto;
  align-items: center;
  gap: 12px;
}

.settings-row > div:first-child {
  min-width: 0;
  overflow-wrap: anywhere;
}

.settings-row {
  gap: 16px;
}

.settings-actions {
  gap: 8px;
}

.billing-wide {
  grid-column: 1 / -1;
}

.security-stack {
  display: grid;
  gap: 16px;
}

.license-note {
  margin: 16px 20px 0;
  font-size: 13px;
}
</style>
