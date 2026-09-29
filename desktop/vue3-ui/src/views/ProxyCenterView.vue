<template>
  <div class="page-stack">
    <PageHeader title="代理中心" description="按节点所属连接栈管理代理、健康检查与实例分配">
      <template v-if="canManage">
        <button class="button button-secondary" type="button" :disabled="!proxies.length || checkingAll" @click="checkAll">
          <Icon :icon="checkingAll ? 'lucide:loader-circle' : 'lucide:gauge'" :class="{ spin: checkingAll }" />{{ checkingAll ? "排队中…" : "测试全部" }}
        </button>
        <button class="button button-primary" type="button" @click="openCreate"><Icon icon="lucide:plus" />添加代理</button>
      </template>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>
    <div v-if="notice" class="alert" :class="notice.tone === 'success' ? 'alert-success' : 'alert-warning'" role="status">
      <Icon :icon="notice.tone === 'success' ? 'lucide:circle-check' : 'lucide:triangle-alert'" /><span>{{ notice.text }}</span>
    </div>

    <div class="stack-notice">
      <Icon icon="lucide:split" />
      <div>
        <strong>连接栈严格隔离</strong>
        <p><b>xray</b> 为 Xray + sing-box 组合栈：Xray 执行 VMess、VLESS、Trojan、Shadowsocks 与链式代理，sing-box 执行 Hysteria、Hysteria2、TUIC、AnyTLS。<b>mihomo</b> 为独立栈。启动、测速、预热与下载均按节点所属栈执行，不会跨栈回退。</p>
      </div>
    </div>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索名称、服务器或出口 IP…" aria-label="搜索代理" /></label>
        <select v-model="stackFilter" aria-label="按连接栈筛选"><option value="">全部连接栈</option><option value="xray">xray 组合栈</option><option value="mihomo">mihomo 独立栈</option></select>
        <select v-model="protocolFilter" aria-label="按协议筛选"><option value="">全部协议</option><option v-for="item in protocolOptions" :key="item" :value="item">{{ protocolLabel(item) }}</option></select>
        <span class="toolbar-spacer" />
        <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新</button>
      </div>
      <div class="table-wrap">
        <table v-if="rows.length" class="data-table">
          <thead>
            <tr><th>代理名称</th><th>连接栈</th><th>协议 / 执行器</th><th>服务器</th><th>出口 IP</th><th>延迟</th><th>健康检查</th><th v-if="canManage" class="actions-cell">操作</th></tr>
          </thead>
          <tbody>
            <tr v-for="row in rows" :key="row.proxy.id">
              <td>
                <div class="primary-cell">
                  <span class="row-icon"><Icon icon="lucide:globe-2" /></span>
                  <div><strong>{{ row.proxy.name }}</strong><small>{{ credentialLabel(row.proxy) }}</small></div>
                  <span v-if="row.proxy.status === 'disabled'" class="tag">已停用</span>
                </div>
              </td>
              <td><span class="stack-badge" :class="normalizeStack(row.proxy.connectorType)">{{ normalizeStack(row.proxy.connectorType) }}</span></td>
              <td><strong>{{ protocolLabel(row.proxy.protocol) }}</strong><small class="cell-subtitle">{{ executorLabel(row.proxy.connectorType, row.proxy.kernel) }}</small></td>
              <td><span v-if="row.proxy.protocol === 'direct'" class="muted-text">—</span><span v-else class="mono">{{ row.proxy.host }}:{{ row.proxy.port }}</span></td>
              <td><span v-if="row.check?.ip" class="mono">{{ row.check.ip }}</span><span v-else class="muted-text">—</span></td>
              <td><span :class="latencyTone(row.check)">{{ latencyText(row.check) }}</span></td>
              <td>
                <StatusBadge :status="row.badge.status" :label="row.badge.label" />
                <small v-if="row.reason" :class="row.check?.errorCode === 'proxy_unreachable' ? 'row-error' : 'cell-subtitle'">{{ row.reason }}</small>
                <small v-else-if="row.check?.status === 'queued' && !row.polling" class="cell-subtitle">尚未完成，稍后刷新查看</small>
                <small v-else-if="row.check?.completedAt" class="cell-subtitle">{{ formatDateTime(row.check.completedAt) }}</small>
              </td>
              <td v-if="canManage" class="actions-cell">
                <button class="button button-quiet small" type="button" :disabled="row.checking || !row.checkable" :title="row.checkable ? undefined : '代理已停用，无法检测'" @click="healthCheck(row.proxy)">
                  <Icon v-if="row.checking" icon="lucide:loader-circle" class="spin" />{{ row.checking ? "检测中…" : "健康检查" }}
                </button>
                <button class="icon-button" type="button" title="编辑" :aria-label="`编辑 ${row.proxy.name}`" @click="openEdit(row.proxy)"><Icon icon="lucide:pencil" /></button>
                <ActionMenu :items="rowMenu" :label="`${row.proxy.name} 更多操作`" @select="(key) => onMenu(row.proxy, key)" />
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else-if="loading" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载代理…</div>
        <EmptyState v-else-if="loadFailed" icon="lucide:cloud-off" title="代理列表加载失败" description="请检查网络或稍后重试。">
          <button class="button button-secondary" type="button" @click="load">重试</button>
        </EmptyState>
        <EmptyState v-else-if="proxies.length" icon="lucide:search-x" title="没有匹配的代理" description="调整搜索词或筛选条件。" />
        <EmptyState v-else icon="lucide:globe-2" title="还没有代理节点" description="添加代理后，系统按其连接栈完成测速、出口检查和实例分配。">
          <button v-if="canManage" class="button button-primary" type="button" @click="openCreate">添加代理</button>
        </EmptyState>
      </div>
    </article>

    <ProxyFormDialog :open="formOpen" :proxy="editingProxy" @close="formOpen = false" @saved="onSaved" />
    <ProxyAssignmentDialog :open="assignOpen" :proxy="assignTarget" :mode="assignMode" :known="knownAssignments" @close="assignOpen = false" @done="onAssignmentDone" />
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ActionMenu, { type ActionMenuItem } from "@/components/ActionMenu.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import ProxyAssignmentDialog from "@/components/proxy/ProxyAssignmentDialog.vue";
import ProxyFormDialog from "@/components/proxy/ProxyFormDialog.vue";
import { assignmentKey, type AssignmentMode, type AssignmentResult } from "@/components/proxy/assignment";
import { allProtocols, executorLabel, healthBadge, healthReason, isHealthCheckable, normalizeStack, protocolLabel, stackProtocols } from "@/components/proxy/protocols";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { ProxyAssignment, ProxyConnectorType, ProxyHealthCheck, ProxyNode } from "@/types";
import { formatDateTime } from "@/utils/format";

interface Notice {
  tone: "success" | "warning";
  text: string;
}

interface ProxyRow {
  proxy: ProxyNode;
  check?: ProxyHealthCheck;
  badge: { status: string; label: string };
  reason: string;
  /** Disabled proxies are rejected by the health worker (proxy_disabled). */
  checkable: boolean;
  /** A check is being requested or its queued result is still being polled. */
  checking: boolean;
  polling: boolean;
}

const POLL_INTERVAL_MS = 2000;
const POLL_WINDOW_MS = 60_000;
/** Every workspace request is metered (api_calls), so one tick polls at most this many checks. */
const POLL_BATCH = 8;

const session = useSessionStore();
const confirm = useConfirm();
const canManage = computed(() => session.can("proxy.manage"));

const proxies = ref<ProxyNode[]>([]);
const healthByProxy = ref<Record<string, ProxyHealthCheck | undefined>>({});
const loading = ref(false);
const loadFailed = ref(false);
const errorMessage = ref("");
const notice = ref<Notice | null>(null);
const query = ref("");
const stackFilter = ref<"" | ProxyConnectorType>("");
const protocolFilter = ref("");
const checkingAll = ref(false);
/** Proxy IDs with a health-check request in flight. */
const requesting = reactive(new Set<string>());
/** Queued check ID → time polling started. */
const polling = reactive(new Map<string, number>());
/** Queued check ID → last poll time; decides the rotation when more than POLL_BATCH are queued. */
const lastPolled = new Map<string, number>();

const formOpen = ref(false);
const editingProxy = ref<ProxyNode | null>(null);
const assignOpen = ref(false);
const assignMode = ref<AssignmentMode>("assign");
const assignTarget = ref<ProxyNode | null>(null);
const knownAssignments = ref<Record<string, ProxyAssignment>>({});

const rowMenu: ActionMenuItem[] = [
  { key: "assign", label: "分配到实例", icon: "lucide:link" },
  { key: "unassign", label: "解除分配", icon: "lucide:unlink" },
  { key: "delete", label: "删除代理", icon: "lucide:trash-2", danger: true },
];

const protocolOptions = computed(() => (stackFilter.value ? stackProtocols(stackFilter.value) : allProtocols));
watch(stackFilter, () => {
  if (protocolFilter.value && !protocolOptions.value.includes(protocolFilter.value)) protocolFilter.value = "";
});

const isPolling = (check?: ProxyHealthCheck) => Boolean(check && polling.has(check.id));
const isChecking = (proxy: ProxyNode) => requesting.has(proxy.id) || isPolling(healthByProxy.value[proxy.id]);

const rows = computed<ProxyRow[]>(() => {
  const needle = query.value.toLowerCase();
  return proxies.value
    .filter((proxy) => {
      const text = `${proxy.name} ${proxy.host} ${healthByProxy.value[proxy.id]?.ip ?? ""}`.toLowerCase();
      return (!needle || text.includes(needle))
        && (!stackFilter.value || normalizeStack(proxy.connectorType) === stackFilter.value)
        && (!protocolFilter.value || proxy.protocol === protocolFilter.value);
    })
    .map((proxy) => {
      const check = healthByProxy.value[proxy.id];
      return {
        proxy,
        check,
        badge: healthBadge(check),
        reason: healthReason(check),
        checkable: isHealthCheckable(proxy.status),
        checking: isChecking(proxy),
        polling: isPolling(check),
      };
    });
});

const credentialLabel = (proxy: ProxyNode) =>
  [proxy.username ? `用户 ${proxy.username}` : "", proxy.hasCredentials ? "已保存凭据" : ""].filter(Boolean).join(" · ") || "无认证";
const latencyText = (check?: ProxyHealthCheck) => {
  if (!check) return "未测速";
  if (check.status === "queued") return "检测中";
  return check.status === "succeeded" && check.latencyMs ? `${check.latencyMs} ms` : "—";
};
const latencyTone = (check?: ProxyHealthCheck) => {
  const value = check?.status === "succeeded" ? check.latencyMs : undefined;
  return !value ? "muted-text" : value < 250 ? "text-success" : value < 600 ? "text-warning" : "text-danger";
};
const clearMessages = () => {
  errorMessage.value = "";
  notice.value = null;
};
const proxyPath = (id: string) => `${session.workspaceBase}/proxies/${encodeURIComponent(id)}`;
const isVersionConflict = (error: unknown) => error instanceof ApiError && error.code === "version_conflict";

let disposed = false;

/**
 * Background refresh of one row. Completing a check rewrites the proxy's status
 * and bumps its version on the server; a failure here is tolerated because edit
 * and delete re-read the version on conflict.
 */
const refreshProxy = async (proxyId: string) => {
  if (!session.workspaceBase) return;
  try {
    const fresh = await api.get<ProxyNode>(proxyPath(proxyId));
    if (!disposed) proxies.value = proxies.value.map((item) => (item.id === fresh.id ? fresh : item));
  } catch (error) {
    if (!disposed && error instanceof ApiError && error.status === 404) proxies.value = proxies.value.filter((item) => item.id !== proxyId);
  }
};

// Health-check polling: GET /proxy-health-checks/{id} every 2 s while queued,
// for at most 60 s per check; stopped when the view unmounts.
let pollTimer: number | undefined;

const setCheck = (check: ProxyHealthCheck) => {
  const current = healthByProxy.value[check.proxyId];
  // An older check never replaces a newer one for the same proxy.
  if (current && current.id !== check.id && Date.parse(current.createdAt) > Date.parse(check.createdAt)) return;
  healthByProxy.value = { ...healthByProxy.value, [check.proxyId]: check };
};

const stopPolling = (checkId: string) => {
  polling.delete(checkId);
  lastPolled.delete(checkId);
};

const schedulePoll = () => {
  if (disposed || pollTimer !== undefined || !polling.size) return;
  pollTimer = window.setTimeout(() => {
    pollTimer = undefined;
    void pollOnce();
  }, POLL_INTERVAL_MS);
};

const track = (check: ProxyHealthCheck) => {
  setCheck(check);
  if (check.status !== "queued" || polling.has(check.id)) return;
  polling.set(check.id, Date.now());
  schedulePoll();
};

const pollOnce = async () => {
  const base = session.workspaceBase;
  if (!base) {
    polling.clear();
    lastPolled.clear();
    return;
  }
  const now = Date.now();
  for (const [checkId, startedAt] of [...polling.entries()]) {
    if (now - startedAt >= POLL_WINDOW_MS) stopPolling(checkId);
  }
  // Least recently polled first, so a large batch rotates instead of starving.
  const batch = [...polling.keys()].sort((a, b) => (lastPolled.get(a) ?? 0) - (lastPolled.get(b) ?? 0)).slice(0, POLL_BATCH);
  await Promise.all(batch.map(async (checkId) => {
    lastPolled.set(checkId, Date.now());
    try {
      const check = await api.get<ProxyHealthCheck>(`${base}/proxy-health-checks/${encodeURIComponent(checkId)}`);
      if (disposed) return;
      setCheck(check);
      if (check.status !== "queued") {
        stopPolling(checkId);
        void refreshProxy(check.proxyId);
      }
    } catch (error) {
      // A missing check stops polling; other failures retry until the window closes.
      if (error instanceof ApiError && error.status === 404) stopPolling(checkId);
    }
  }));
  schedulePoll();
};

onBeforeUnmount(() => {
  disposed = true;
  if (pollTimer !== undefined) window.clearTimeout(pollTimer);
  polling.clear();
  lastPolled.clear();
});

let loadToken = 0;

const load = async () => {
  const base = session.workspaceBase;
  if (!base) {
    errorMessage.value = "请先选择工作空间";
    return;
  }
  const token = ++loadToken;
  loading.value = true;
  errorMessage.value = "";
  try {
    const [items, assignments] = await Promise.all([
      api.list<ProxyNode>(`${base}/proxies`),
      // Only marks dialog options, so a failure leaves the previous marks.
      api.list<ProxyAssignment>(`${base}/proxy-assignments?targetType=browser_instance`).catch(() => null),
    ]);
    const pages = await Promise.allSettled(items.map((item) => api.list<ProxyHealthCheck>(`${base}/proxies/${encodeURIComponent(item.id)}/health-checks?limit=1`)));
    // A newer load (or unmount) supersedes this one.
    if (disposed || token !== loadToken) return;
    if (assignments) {
      knownAssignments.value = Object.fromEntries(assignments.map((item) => [assignmentKey(item.targetType, item.targetId), item]));
    }
    const next: Record<string, ProxyHealthCheck | undefined> = {};
    let historyFailures = 0;
    items.forEach((item, index) => {
      const page = pages[index];
      if (page.status === "fulfilled") next[item.id] = page.value[0];
      else {
        historyFailures += 1;
        next[item.id] = healthByProxy.value[item.id];
      }
    });
    proxies.value = items;
    healthByProxy.value = next;
    loadFailed.value = false;
    // Resume polling checks queued recently (for example right before a refresh).
    for (const check of Object.values(next)) {
      if (check?.status === "queued" && Date.now() - Date.parse(check.createdAt) < POLL_WINDOW_MS) track(check);
    }
    if (historyFailures) errorMessage.value = `${historyFailures} 个代理的检测记录加载失败，可稍后刷新`;
  } catch (error) {
    if (token !== loadToken) return;
    loadFailed.value = !proxies.value.length;
    errorMessage.value = `代理列表加载失败：${describeError(error, "请稍后重试")}`;
  } finally {
    if (token === loadToken) loading.value = false;
  }
};

const requestCheck = async (proxy: ProxyNode) => {
  requesting.add(proxy.id);
  try {
    // 202 { data: check, taskId, dispatchStatus }; the check stays queued until the worker reports.
    const check = await api.post<ProxyHealthCheck>(`${proxyPath(proxy.id)}/health-checks`);
    track(check);
    return check;
  } finally {
    requesting.delete(proxy.id);
  }
};

const healthCheck = async (proxy: ProxyNode) => {
  if (isChecking(proxy) || !isHealthCheckable(proxy.status)) return;
  clearMessages();
  try {
    await requestCheck(proxy);
  } catch (error) {
    errorMessage.value = `「${proxy.name}」健康检查排队失败：${describeError(error, "请稍后重试")}`;
  }
};

const checkAll = async () => {
  if (checkingAll.value || !proxies.value.length) return;
  clearMessages();
  const eligible = proxies.value.filter((proxy) => isHealthCheckable(proxy.status));
  const skipped = proxies.value.length - eligible.length;
  // Proxies whose check is still being requested or polled are not queued twice.
  const targets = eligible.filter((proxy) => !isChecking(proxy));
  if (!targets.length) {
    notice.value = { tone: "success", text: eligible.length ? "所有代理的健康检查均在进行中" : "没有可检测的代理，已停用的代理不会检测" };
    return;
  }
  checkingAll.value = true;
  const skippedText = skipped ? `，已跳过 ${skipped} 个停用的代理` : "";
  try {
    const results = await Promise.allSettled(targets.map(requestCheck));
    const failed = results.filter((result): result is PromiseRejectedResult => result.status === "rejected");
    if (failed.length) errorMessage.value = `${failed.length} / ${targets.length} 个代理健康检查排队失败：${describeError(failed[0].reason, "请稍后重试")}${skippedText}`;
    else notice.value = { tone: "success", text: `已为 ${targets.length} 个代理排队健康检查${skippedText}` };
  } finally {
    checkingAll.value = false;
  }
};

const openCreate = () => {
  editingProxy.value = null;
  formOpen.value = true;
};
const openEdit = (proxy: ProxyNode) => {
  editingProxy.value = proxy;
  formOpen.value = true;
};

const onSaved = async (proxy: ProxyNode) => {
  const updated = Boolean(editingProxy.value);
  formOpen.value = false;
  editingProxy.value = null;
  clearMessages();
  await load();
  if (updated) {
    notice.value = { tone: "success", text: `已更新代理「${proxy.name}」` };
    return;
  }
  try {
    await requestCheck(proxy);
    notice.value = { tone: "success", text: `已创建代理「${proxy.name}」，健康检查已排队` };
  } catch (error) {
    notice.value = { tone: "warning", text: `代理「${proxy.name}」已创建，但健康检查排队失败：${describeError(error, "请稍后重试")}。可在列表中重新检查。` };
  }
};

const openAssignment = (proxy: ProxyNode, mode: AssignmentMode) => {
  clearMessages();
  assignTarget.value = proxy;
  assignMode.value = mode;
  assignOpen.value = true;
};

const onAssignmentDone = async ({ mode, assignment, targetName }: AssignmentResult) => {
  const proxy = assignTarget.value;
  assignOpen.value = false;
  const next = { ...knownAssignments.value };
  const key = assignmentKey(assignment.targetType, assignment.targetId);
  if (mode === "assign") next[key] = assignment;
  else delete next[key];
  knownAssignments.value = next;
  notice.value = {
    tone: "success",
    text: mode === "assign" ? `已将「${proxy?.name ?? "代理"}」分配到「${targetName}」` : `「${targetName}」已不再分配「${proxy?.name ?? "代理"}」`,
  };
  // Assignments do not change list columns; refreshing the one row avoids a metered full reload.
  if (proxy) await refreshProxy(proxy.id);
};

const deleteProxy = async (proxy: ProxyNode) => {
  // If-Match is not CORS-allowed; the version travels as a query parameter.
  const remove = (version: number) => api.delete(`${proxyPath(proxy.id)}?version=${version}`);
  try {
    await remove(proxy.version);
  } catch (error) {
    // Completed health checks bump the version in the background. The user
    // confirmed deleting this proxy, so retry once with the current version.
    if (!isVersionConflict(error)) throw error;
    const fresh = await api.get<ProxyNode>(proxyPath(proxy.id));
    await remove(fresh.version);
  }
};

const askDelete = (proxy: ProxyNode) => {
  clearMessages();
  confirm.ask({
    title: "删除代理",
    message: `确定删除「${proxy.name}」吗？删除后不可恢复。\n仍分配给实例、账号或档案的代理需先解除分配。`,
    confirmText: "删除",
    danger: true,
  }, async () => {
    await deleteProxy(proxy);
    await load();
    notice.value = { tone: "success", text: `已删除代理「${proxy.name}」` };
  });
};

const onMenu = (proxy: ProxyNode, key: string) => {
  if (key === "assign" || key === "unassign") openAssignment(proxy, key);
  else if (key === "delete") askDelete(proxy);
};

onMounted(() => void load());
</script>
