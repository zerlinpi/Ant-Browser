<template>
  <div class="page-stack">
    <PageHeader title="控制台" :description="`${session.activeWorkspace?.name || '当前工作空间'}运营概览`">
      <button class="button button-secondary" type="button" @click="load"><Icon icon="lucide:refresh-cw" />刷新</button>
      <RouterLink class="button button-primary" to="/browsers"><Icon icon="lucide:plus" />新建浏览器</RouterLink>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger"><Icon icon="lucide:circle-alert" />{{ errorMessage }}</div>

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
          <div v-for="event in riskEvents" :key="String(event.id)" class="activity-row">
            <span class="activity-icon danger"><Icon icon="lucide:triangle-alert" /></span>
            <div><strong>{{ text(event, 'title', 'eventType', 'type') || '风险事件' }}</strong><p>{{ text(event, 'message', 'summary') || '请检查账号与代理状态' }}</p></div>
            <small>{{ time(text(event, 'createdAt', 'occurredAt')) }}</small>
          </div>
        </div>
        <EmptyState v-else icon="lucide:shield-check" title="当前没有高风险事件" description="系统会在账号、代理或任务出现异常时立即通知。" />
      </article>
    </section>

    <article class="panel">
      <header class="panel-header"><div><h2>最近任务</h2><p>跨设备执行队列与自动化结果</p></div><RouterLink to="/tasks">进入任务中心</RouterLink></header>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>任务</th><th>类型</th><th>状态</th><th>执行设备</th><th>更新时间</th></tr></thead>
          <tbody>
            <tr v-for="task in recentTasks" :key="String(task.id)">
              <td><strong>{{ text(task, 'name', 'id') }}</strong></td>
              <td>{{ text(task, 'kind', 'type') || 'automation' }}</td>
              <td><StatusBadge :status="text(task, 'status')" /></td>
              <td>{{ text(task, 'deviceName', 'deviceId') || '自动分配' }}</td>
              <td>{{ time(text(task, 'updatedAt', 'createdAt')) }}</td>
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
import { api } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useSessionStore } from "@/stores/session";

type AnyRecord = Record<string, unknown>;
const session = useSessionStore();
const dashboard = ref<AnyRecord>({});
const recentTasks = ref<AnyRecord[]>([]);
const riskEvents = ref<AnyRecord[]>([]);
const loading = ref(false);
const errorMessage = ref("");
const number = (...keys: string[]) => {
  for (const key of keys) if (typeof dashboard.value[key] === "number") return dashboard.value[key] as number;
  return 0;
};
const text = (item: AnyRecord, ...keys: string[]) => keys.map((key) => item[key]).find((value) => typeof value === "string") as string | undefined;
const time = (value?: string) => value ? new Intl.DateTimeFormat("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value)) : "—";

const stats = computed(() => [
  { label: "浏览器实例", value: number("instanceCount", "instancesTotal"), detail: `${number("runningInstanceCount", "instancesRunning")} 个运行中`, icon: "lucide:monitor", tone: "blue" },
  { label: "账号资产", value: number("accountCount", "accountsTotal"), detail: `${number("riskAccountCount", "accountsAtRisk")} 个需关注`, icon: "lucide:badge-user", tone: "violet" },
  { label: "健康代理", value: number("healthyProxyCount", "proxiesHealthy"), detail: `${number("proxyCount", "proxiesTotal")} 个节点`, icon: "lucide:globe-2", tone: "green" },
  { label: "自动化成功率", value: `${Math.round(number("taskSuccessRate", "automationSuccessRate") * (number("taskSuccessRate", "automationSuccessRate") <= 1 ? 100 : 1))}%`, detail: "近 24 小时", icon: "lucide:activity", tone: "orange" },
]);
const healthRows = computed(() => [
  { label: "实例在线率", value: `${number("runningInstanceCount", "instancesRunning")}/${number("instanceCount", "instancesTotal")}`, percent: number("instanceCount", "instancesTotal") ? number("runningInstanceCount", "instancesRunning") / number("instanceCount", "instancesTotal") * 100 : 0, tone: "green", icon: "lucide:monitor-check" },
  { label: "代理可用率", value: `${number("healthyProxyCount", "proxiesHealthy")}/${number("proxyCount", "proxiesTotal")}`, percent: number("proxyCount", "proxiesTotal") ? number("healthyProxyCount", "proxiesHealthy") / number("proxyCount", "proxiesTotal") * 100 : 0, tone: "blue", icon: "lucide:globe-2" },
  { label: "任务成功率", value: `${Math.round(number("taskSuccessRate", "automationSuccessRate") * (number("taskSuccessRate", "automationSuccessRate") <= 1 ? 100 : 1))}%`, percent: number("taskSuccessRate", "automationSuccessRate") * (number("taskSuccessRate", "automationSuccessRate") <= 1 ? 100 : 1), tone: "violet", icon: "lucide:workflow" },
]);

const load = async () => {
  if (!session.workspaceBase) return;
  loading.value = true;
  errorMessage.value = "";
  try {
    const [summary, tasks, risks] = await Promise.all([
      api.get<AnyRecord>(`${session.workspaceBase}/analytics/dashboard`),
      api.get<AnyRecord[]>(`${session.workspaceBase}/tasks?limit=6`),
      api.get<AnyRecord[]>(`${session.workspaceBase}/analytics/risk-events?limit=4`),
    ]);
    dashboard.value = summary;
    recentTasks.value = tasks.slice(0, 6);
    riskEvents.value = risks.slice(0, 4);
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : "无法加载运营数据";
  } finally { loading.value = false; }
};
onMounted(load);
</script>
