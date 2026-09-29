<template>
  <div class="page-stack">
    <PageHeader title="控制台" :description="`${session.activeWorkspace?.name || '当前工作空间'}运营概览`">
      <button class="button button-secondary" type="button" :disabled="loading" @click="load">
        <Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />{{ loading ? "刷新中…" : "刷新" }}
      </button>
      <RouterLink class="button button-primary" to="/browsers"><Icon icon="lucide:plus" />新建浏览器</RouterLink>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>
    <div v-if="!canReadAnalytics" class="stack-notice" role="status">
      <Icon icon="lucide:lock" /><div><strong>当前角色无权查看运营分析</strong><p>运营指标与风险提醒已隐藏，仅显示最近任务。</p></div>
    </div>

    <template v-if="canReadAnalytics">
      <section class="stat-grid" aria-label="关键指标">
        <article v-for="stat in stats" :key="stat.label" class="stat-card">
          <div><p>{{ stat.label }}</p><strong>{{ stat.value }}</strong><small>{{ stat.detail }}</small></div>
          <span class="stat-icon" :class="stat.tone"><Icon :icon="stat.icon" /></span>
        </article>
      </section>

      <section class="dashboard-grid">
        <article class="panel">
          <header class="panel-header"><div><h2>运营健康度</h2><p>实例、代理与任务的实时质量信号</p></div></header>
          <div class="health-list">
            <div v-for="health in healthRows" :key="health.label" class="health-row">
              <span><Icon :icon="health.icon" />{{ health.label }}</span>
              <div class="health-bar"><i :style="{ width: `${health.percent}%` }" :class="health.tone" /></div>
              <strong>{{ health.value }}</strong>
            </div>
          </div>
        </article>

        <article class="panel">
          <header class="panel-header"><div><h2>风险提醒</h2><p>需要团队尽快处理的异常</p></div><RouterLink to="/accounts">查看账号</RouterLink></header>
          <div v-if="riskEvents.length" class="activity-list">
            <div v-for="event in riskEvents" :key="event.id" class="activity-row">
              <span class="activity-icon danger"><Icon icon="lucide:triangle-alert" /></span>
              <div><strong>{{ riskTitle(event) }}</strong><p>{{ riskDescription(event) }}</p></div>
              <small>{{ time(event.createdAt) }}</small>
            </div>
          </div>
          <EmptyState v-else icon="lucide:shield-check" title="当前没有高风险事件" description="系统会在账号、代理或任务出现异常时立即通知。" />
        </article>
      </section>
    </template>

    <article class="panel">
      <header class="panel-header"><div><h2>最近任务</h2><p>跨设备执行队列与自动化结果</p></div><RouterLink to="/tasks">进入任务中心</RouterLink></header>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>任务</th><th>类型</th><th>状态</th><th>执行设备</th><th>更新时间</th></tr></thead>
          <tbody>
            <tr v-for="task in recentTasks" :key="task.id">
              <td><strong>{{ taskLabel(task) }}</strong></td>
              <td>{{ task.taskType }}</td>
              <td><StatusBadge :status="task.status" /></td>
              <td>{{ taskDevice(task) }}</td>
              <td>{{ time(task.updatedAt || task.createdAt) }}</td>
            </tr>
          </tbody>
        </table>
        <EmptyState v-if="!recentTasks.length && !loading" icon="lucide:list-checks" title="还没有任务记录" description="启动实例或运行自动化流程后，执行状态会出现在这里。" />
      </div>
    </article>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { RouterLink } from "vue-router";
import { Icon } from "@iconify/vue";
import { api, describeError } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useSessionStore } from "@/stores/session";
import type { AnalyticsDashboard, AnalyticsRiskEvent, AnalyticsRiskPage, TaskRun } from "@/types";

const HOUR_MS = 3_600_000;

const emptyDashboard = (): AnalyticsDashboard => ({
  window: { from: "", to: "" },
  accounts: { total: 0, active: 0, atRisk: 0, byStatus: {} },
  instances: { total: 0, online: 0, offline: 0, starting: 0, failed: 0 },
  proxies: { total: 0, healthy: 0, unhealthy: 0, samples: 0, successRate: 0, averageLatencyMs: 0 },
  tasks: { total: 0, completed: 0, succeeded: 0, failed: 0, successRate: 0 },
  risks: { open: 0, bySeverity: {} },
  team: { activeMembers: 0, activeActors: 0, eventsByActor: {} },
});

const session = useSessionStore();
const dashboard = ref<AnalyticsDashboard>(emptyDashboard());
const recentTasks = ref<TaskRun[]>([]);
const riskEvents = ref<AnalyticsRiskEvent[]>([]);
const loading = ref(false);
const errorMessage = ref("");
/** Client time when the current dashboard arrived; used to recognise a rolling window. */
const dashboardLoadedAt = ref(0);
// Analytics endpoints require analytics.read (operators lack it); tasks only need workflow.read.
const canReadAnalytics = computed(() => session.can("analytics.read"));

const time = (value?: string) => {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(date);
};
const pad = (value: number) => String(value).padStart(2, "0");
const rangePoint = (date: Date) => `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;

/** Label for the server-normalised statistics window (the server default is the previous 24 hours). */
const windowLabel = computed(() => {
  const from = new Date(dashboard.value.window.from);
  const to = new Date(dashboard.value.window.to);
  const span = to.getTime() - from.getTime();
  if (!Number.isFinite(span) || span <= 0) return "—";
  const hours = Math.round(span / HOUR_MS);
  // A whole-hour window ending when the data was loaded reads as a rolling range.
  const rolling = hours >= 1 && Math.abs(span - hours * HOUR_MS) < 60_000 && Math.abs(dashboardLoadedAt.value - to.getTime()) < 5 * 60_000;
  if (!rolling) return `${rangePoint(from)} – ${rangePoint(to)}`;
  return hours >= 48 && hours % 24 === 0 ? `近 ${hours / 24} 天` : `近 ${hours} 小时`;
});

const detailText = (event: AnalyticsRiskEvent, ...keys: string[]) => {
  for (const key of keys) {
    const value = event.details?.[key];
    if (typeof value === "string" && value.trim()) return value;
  }
  return "";
};
const riskTitle = (event: AnalyticsRiskEvent) => ({
  "account.risk_event": "账号风险事件",
  "proxy.health_failed": "代理健康异常",
  "task.failed": "任务执行失败",
}[event.eventType] || event.eventType || "风险事件");
const riskDescription = (event: AnalyticsRiskEvent) => detailText(event, "description", "message", "code") || "请检查账号、代理或任务状态";
const payloadText = (task: TaskRun, key: string) => typeof task.payload?.[key] === "string" ? String(task.payload[key]) : "";
const taskLabel = (task: TaskRun) => payloadText(task, "name") || task.workflowId || task.id;
const taskDevice = (task: TaskRun) => payloadText(task, "deviceName") || payloadText(task, "deviceId") || "自动分配";

const stats = computed(() => [
  { label: "浏览器实例", value: dashboard.value.instances.total, detail: `${dashboard.value.instances.online} 个在线`, icon: "lucide:monitor", tone: "blue" },
  { label: "账号资产", value: dashboard.value.accounts.total, detail: `${dashboard.value.accounts.atRisk} 个需关注`, icon: "lucide:contact-round", tone: "violet" },
  { label: "健康代理", value: dashboard.value.proxies.healthy, detail: `${dashboard.value.proxies.total} 个节点`, icon: "lucide:globe-2", tone: "green" },
  { label: "自动化成功率", value: `${Math.round(dashboard.value.tasks.successRate)}%`, detail: windowLabel.value, icon: "lucide:activity", tone: "orange" },
]);
const healthRows = computed(() => [
  { label: "实例在线率", value: `${dashboard.value.instances.online}/${dashboard.value.instances.total}`, percent: dashboard.value.instances.total ? dashboard.value.instances.online / dashboard.value.instances.total * 100 : 0, tone: "green", icon: "lucide:monitor-check" },
  { label: "代理可用率", value: `${dashboard.value.proxies.healthy}/${dashboard.value.proxies.total}`, percent: dashboard.value.proxies.total ? dashboard.value.proxies.healthy / dashboard.value.proxies.total * 100 : 0, tone: "blue", icon: "lucide:globe-2" },
  { label: "任务成功率", value: `${Math.round(dashboard.value.tasks.successRate)}%`, percent: dashboard.value.tasks.successRate, tone: "violet", icon: "lucide:workflow" },
]);

const load = async () => {
  const base = session.workspaceBase;
  if (!base || loading.value) return;
  loading.value = true;
  const analytics = canReadAnalytics.value;
  // Sections load independently so one failure (for example a 403) keeps the rest visible.
  const [summary, tasks, risks] = await Promise.allSettled([
    analytics ? api.get<AnalyticsDashboard>(`${base}/analytics/dashboard`) : Promise.resolve(null),
    api.list<TaskRun>(`${base}/tasks?limit=6`),
    analytics ? api.get<AnalyticsRiskPage | null>(`${base}/analytics/risk-events?limit=4&openOnly=true`) : Promise.resolve(null),
  ]);
  // Sections that fail with the same reason are reported together.
  const failures = new Map<string, string[]>();
  const fail = (section: string, reason: unknown) => {
    const message = describeError(reason, "请稍后重试");
    failures.set(message, [...(failures.get(message) ?? []), section]);
  };
  if (summary.status === "fulfilled") {
    if (summary.value) {
      dashboard.value = summary.value;
      dashboardLoadedAt.value = Date.now();
    }
  } else fail("运营指标", summary.reason);
  if (tasks.status === "fulfilled") recentTasks.value = tasks.value.slice(0, 6);
  else fail("最近任务", tasks.reason);
  if (risks.status === "fulfilled") riskEvents.value = (risks.value?.items ?? []).slice(0, 4);
  else fail("风险提醒", risks.reason);
  errorMessage.value = [...failures].map(([message, sections]) => `${sections.join("、")}加载失败：${message}`).join("；");
  loading.value = false;
};
onMounted(load);
</script>
