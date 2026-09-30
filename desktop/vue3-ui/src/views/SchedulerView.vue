<template>
  <div class="page-stack">
    <PageHeader title="任务调度" description="按 IANA 时区定时运行已发布工作流">
      <button v-if="canManage" class="button button-primary" type="button" @click="openCreate"><Icon icon="lucide:calendar-plus" />新建计划</button>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>
    <div v-if="successMessage" class="alert alert-success" role="status"><Icon icon="lucide:circle-check" /><span>{{ successMessage }}</span></div>

    <section class="stat-grid compact">
      <article class="stat-card"><div><p>计划总数</p><strong>{{ schedules.length }}</strong></div><span class="stat-icon blue"><Icon icon="lucide:calendar-clock" /></span></article>
      <article class="stat-card"><div><p>已启用</p><strong>{{ enabledCount }}</strong></div><span class="stat-icon green"><Icon icon="lucide:circle-play" /></span></article>
      <article class="stat-card"><div><p>异常</p><strong>{{ errorCount }}</strong></div><span class="stat-icon orange"><Icon icon="lucide:triangle-alert" /></span></article>
    </section>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索工作流、实例或 Cron…" aria-label="搜索调度计划" /></label>
        <select v-model="statusFilter" aria-label="按状态筛选"><option value="">全部状态</option><option value="enabled">已启用</option><option value="disabled">已停用</option><option value="error">异常</option></select>
        <span class="toolbar-spacer" />
        <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新</button>
      </div>
      <div class="table-wrap">
        <table v-if="rows.length" class="data-table">
          <thead><tr><th>工作流</th><th>浏览器实例</th><th>Cron / 时区</th><th>状态</th><th>下次运行</th><th>上次运行</th><th v-if="canManage" class="actions-cell">操作</th></tr></thead>
          <tbody>
            <tr v-for="row in rows" :key="row.item.id">
              <td>
                <strong>{{ workflowName(row.item.workflowId) }}</strong>
                <small v-if="row.stale" class="row-error">工作流已重新发布或归档，请重新创建计划</small>
                <small v-else class="cell-subtitle mono">版本 {{ shortId(row.item.workflowVersionId) }}</small>
              </td>
              <td>{{ instanceName(row.item.instanceId) }}</td>
              <td><span class="mono">{{ row.item.cronExpression }}</span><small class="cell-subtitle">{{ row.item.timezone }}</small></td>
              <td><StatusBadge :status="stateBadge[row.state]" :label="stateLabels[row.state]" /></td>
              <td>{{ row.state === "enabled" ? formatDateTime(row.item.nextRunAt) : "—" }}</td>
              <td>{{ formatDateTime(row.item.lastRunAt) }}<small v-if="row.item.lastError" class="row-error">{{ translateScheduleMessage(row.item.lastError) }}</small></td>
              <td v-if="canManage" class="actions-cell">
                <button
                  class="button button-quiet small"
                  type="button"
                  :disabled="togglingId === row.item.id || (row.stale && row.state !== 'enabled')"
                  :title="row.stale && row.state !== 'enabled' ? '工作流版本已失效，无法启用' : undefined"
                  @click="toggle(row.item)"
                >
                  <Icon v-if="togglingId === row.item.id" icon="lucide:loader-circle" class="spin" />{{ toggleLabels[row.state] }}
                </button>
                <button class="icon-button" type="button" :disabled="row.stale" :title="row.stale ? '工作流版本已失效，请重新创建计划' : '编辑'" :aria-label="`编辑 ${workflowName(row.item.workflowId)} 的计划`" @click="openEdit(row.item)"><Icon icon="lucide:pencil" /></button>
                <button class="icon-button schedule-delete" type="button" title="删除" :aria-label="`删除 ${workflowName(row.item.workflowId)} 的计划`" @click="askDelete(row.item)"><Icon icon="lucide:trash-2" /></button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else-if="loading" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载调度计划…</div>
        <EmptyState v-else-if="loadFailed" icon="lucide:cloud-off" title="调度计划加载失败" description="请检查网络或稍后重试。">
          <button class="button button-secondary" type="button" @click="load">重试</button>
        </EmptyState>
        <EmptyState v-else-if="schedules.length" icon="lucide:search-x" title="没有匹配的调度计划" description="调整搜索词或状态筛选。" />
        <EmptyState v-else icon="lucide:calendar-clock" title="还没有调度计划" description="选择已发布工作流和运行实例，创建定时运营任务。">
          <button v-if="canManage" class="button button-primary" type="button" @click="openCreate">新建计划</button>
        </EmptyState>
      </div>
    </article>

    <ModalDialog :open="createOpen" title="新建调度计划" description="五段 Cron：分 时 日 月 周（周日为 0）。" @close="closeCreate">
      <div v-if="createError" class="alert alert-danger schedule-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ createError }}</span></div>
      <div class="form-grid">
        <label class="field field-span">
          <span>已发布工作流</span>
          <select v-model="createForm.workflowId" :disabled="saving">
            <option value="" disabled>{{ publishedWorkflows.length ? "请选择工作流" : "没有已发布的工作流" }}</option>
            <option v-for="item in publishedWorkflows" :key="item.id" :value="item.id">{{ item.name }} · v{{ item.latestVersion }}</option>
          </select>
        </label>
        <label class="field field-span">
          <span>运行实例</span>
          <select v-model="createForm.instanceId" :disabled="saving">
            <option value="" disabled>{{ runnableInstances.length ? "请选择已分配设备的实例" : "没有已分配到设备的实例" }}</option>
            <option v-for="item in runnableInstances" :key="item.id" :value="item.id">{{ item.name }} · {{ instanceStateLabel(item.observedState) }}</option>
          </select>
        </label>
        <label class="field"><span>Cron 表达式</span><input v-model.trim="createForm.cronExpression" class="mono" placeholder="0 9 * * *" :disabled="saving" /></label>
        <label class="field"><span>IANA 时区</span><input v-model.trim="createForm.timezone" list="schedule-timezones" placeholder="Asia/Shanghai" :disabled="saving" /></label>
      </div>
      <div class="form-note">
        <Icon icon="lucide:info" />
        <span>示例：<button v-for="preset in cronPresets" :key="preset.value" class="text-link cron-preset" type="button" :disabled="saving" @click="createForm.cronExpression = preset.value">{{ preset.label }}</button><br />仅 Playwright 与 CDP 引擎的工作流可定时运行。</span>
      </div>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="saving" @click="closeCreate">取消</button>
        <button class="button button-primary" type="button" :disabled="saving || !canCreate" @click="createSchedule"><Icon v-if="saving" icon="lucide:loader-circle" class="spin" />{{ saving ? "创建中…" : "创建计划" }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="editOpen" title="编辑调度计划" :description="editing ? `${workflowName(editing.workflowId)} · ${instanceName(editing.instanceId)}` : ''" @close="closeEdit">
      <div v-if="editError" class="alert alert-danger schedule-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ editError }}</span></div>
      <div class="form-grid">
        <label class="field"><span>Cron 表达式</span><input v-model.trim="editForm.cronExpression" class="mono" placeholder="0 9 * * *" :disabled="editBusy" /></label>
        <label class="field"><span>IANA 时区</span><input v-model.trim="editForm.timezone" list="schedule-timezones" placeholder="Asia/Shanghai" :disabled="editBusy" /></label>
      </div>
      <div class="form-note">
        <Icon icon="lucide:info" />
        <span>示例：<button v-for="preset in cronPresets" :key="preset.value" class="text-link cron-preset" type="button" :disabled="editBusy" @click="editForm.cronExpression = preset.value">{{ preset.label }}</button><br />保存后按新规则重新计算下次运行时间。</span>
      </div>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="saving" @click="closeEdit">取消</button>
        <button class="button button-primary" type="button" :disabled="editBusy || !canSaveEdit" @click="saveEdit">
          <Icon v-if="editBusy" icon="lucide:loader-circle" class="spin" />{{ editRefreshing ? "加载中…" : saving ? "保存中…" : "保存" }}
        </button>
      </template>
    </ModalDialog>

    <datalist id="schedule-timezones"><option v-for="zone in timezones" :key="zone" :value="zone" /></datalist>
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { BrowserInstance, Schedule, Workflow } from "@/types";
import { formatDateTime, shortId } from "@/utils/format";

type ScheduleState = "enabled" | "disabled" | "error";

interface ScheduleRow {
  item: Schedule;
  state: ScheduleState;
  /** The pinned workflow version is no longer the published one: update and dispatch are rejected. */
  stale: boolean;
}

const session = useSessionStore();
const confirm = useConfirm();
// Create, update, delete, enable and disable all require workflow.manage.
const canManage = computed(() => session.can("workflow.manage"));

const schedules = ref<Schedule[]>([]);
const workflows = ref<Workflow[]>([]);
const instances = ref<BrowserInstance[]>([]);
const query = ref("");
const statusFilter = ref("");
const loading = ref(false);
const loadFailed = ref(false);
const saving = ref(false);
const togglingId = ref("");
const errorMessage = ref("");
const successMessage = ref("");

const defaultTimezone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
const timezones = (() => {
  try {
    return typeof Intl.supportedValuesOf === "function" ? Intl.supportedValuesOf("timeZone") : [];
  } catch {
    return [];
  }
})();
const cronPresets = [
  { label: "每天 09:00", value: "0 9 * * *" },
  { label: "工作日 09:00", value: "0 9 * * 1-5" },
  { label: "每小时", value: "0 * * * *" },
  { label: "每 30 分钟", value: "*/30 * * * *" },
];

const createOpen = ref(false);
const createError = ref("");
const createForm = reactive({ workflowId: "", instanceId: "", cronExpression: "0 9 * * *", timezone: defaultTimezone });
const editOpen = ref(false);
const editError = ref("");
const editRefreshing = ref(false);
const editing = ref<Schedule | null>(null);
const editForm = reactive({ cronExpression: "", timezone: "" });
let editToken = 0;

const stateLabels: Record<ScheduleState, string> = { enabled: "已启用", disabled: "已停用", error: "异常" };
const stateBadge: Record<ScheduleState, string> = { enabled: "active", disabled: "disabled", error: "error" };
// Enabling also resets an error status to active, so it doubles as "recover".
const toggleLabels: Record<ScheduleState, string> = { enabled: "停用", disabled: "启用", error: "恢复" };
const stateNames: Record<string, string> = { offline: "离线", starting: "启动中", running: "运行中", stopping: "停止中", migrating: "迁移中", failed: "异常" };
const unschedulableText = "工作流已重新发布、归档，或其引擎不支持定时运行；请删除后重新创建计划";

// Server statuses: active (enabled), paused (disabled), error (dispatch failed).
const scheduleState = (item: Schedule): ScheduleState => (item.status === "error" ? "error" : item.enabled ? "enabled" : "disabled");
const workflowById = computed(() => new Map(workflows.value.map((item) => [item.id, item])));
const isStale = (item: Schedule) => {
  const workflow = workflowById.value.get(item.workflowId);
  // Unknown when workflows failed to load.
  return Boolean(workflow && (workflow.status !== "published" || workflow.publishedVersionId !== item.workflowVersionId));
};
const workflowName = (id: string) => workflowById.value.get(id)?.name || shortId(id);
const instanceName = (id: string) => instances.value.find((item) => item.id === id)?.name || shortId(id);
const instanceStateLabel = (value: string) => stateNames[value] ?? value;
const schedulePath = (id: string) => `${session.workspaceBase}/schedules/${encodeURIComponent(id)}`;

const cronRanges: Array<[number, number]> = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 6]];
const cronFieldNames = ["分钟", "小时", "日", "月", "星期"];
// Go's strings.Fields splits on Unicode White_Space; JS \s also matches U+FEFF
// and misses U+0085, so the separator set is spelled out.
const cronSeparators = /[\t\n\v\f\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000]+/;
/**
 * Mirrors ParseCron in server/services/schedule-service/cron.go. Items are `*`, `N` or `N-M` with an
 * optional step `/S` (S ≥ 1); numbers are unsigned digits. A step counts from the item's start, so `N/S`
 * means `N-max/S`, and a step larger than the span yields only the start.
 */
const cronProblem = (expression: string) => {
  const parts = expression.split(cronSeparators).filter(Boolean);
  if (parts.length !== 5) return "Cron 表达式需为五段：分 时 日 月 周";
  for (const [index, part] of parts.entries()) {
    const [min, max] = cronRanges[index];
    const valid = part.split(",").every((item) => {
      const match = /^(\*|(\d+)(?:-(\d+))?)(?:\/(\d+))?$/.exec(item);
      if (!match || (match[4] !== undefined && Number(match[4]) < 1)) return false;
      if (match[1] === "*") return true;
      const start = Number(match[2]);
      const end = match[3] !== undefined ? Number(match[3]) : match[4] !== undefined ? max : start;
      return start >= min && end <= max && start <= end;
    });
    if (!valid) return `Cron 第 ${index + 1} 段（${cronFieldNames[index]}）无效，取值 ${min}–${max}`;
  }
  return "";
};
const timezoneProblem = (zone: string) => {
  try {
    new Intl.DateTimeFormat("en-US", { timeZone: zone });
    return "";
  } catch {
    return "时区无效，请填写 IANA 时区，例如 Asia/Shanghai";
  }
};
// Validation text from schedule-service and the dispatcher is English.
const translateScheduleMessage = (message: string) => {
  if (message.includes("cron expression must contain five fields")) return "Cron 表达式需为五段：分 时 日 月 周";
  const field = /cron field (\d)/.exec(message);
  if (field) return `Cron 第 ${field[1]} 段（${cronFieldNames[Number(field[1]) - 1] ?? "字段"}）无效`;
  if (message.includes("no occurrence in search window")) return "Cron 表达式在未来五年内不会触发（例如 2 月 30 日），请检查日期与月份";
  if (message.includes("timezone must be a valid IANA timezone")) return "时区无效，请填写 IANA 时区，例如 Asia/Shanghai";
  if (message.includes("workflow target is no longer executable")) return "工作流版本已不可执行（已重新发布或归档）";
  if (message.includes("schedule task idempotency conflict")) return "定时任务幂等冲突";
  if (message.includes("must be a UUID")) return "工作流或实例标识无效，请刷新后重试";
  return message;
};
const translatedCodes = new Set(["validation_failed", "invalid_cron_expression", "invalid_timezone"]);
const scheduleError = (error: unknown, fallback: string, overrides?: Record<string, string>) =>
  error instanceof ApiError && translatedCodes.has(error.code) ? translateScheduleMessage(error.message) : describeError(error, fallback, overrides);

const publishedWorkflows = computed(() => workflows.value.filter((item) => item.status === "published" && item.publishedVersionId));
const runnableInstances = computed(() => instances.value.filter((item) => item.assignedDeviceId && !item.deletedAt));
const enabledCount = computed(() => schedules.value.filter((item) => scheduleState(item) === "enabled").length);
const errorCount = computed(() => schedules.value.filter((item) => scheduleState(item) === "error").length);
const canCreate = computed(() => Boolean(createForm.workflowId && createForm.instanceId && createForm.cronExpression && createForm.timezone));
const editBusy = computed(() => saving.value || editRefreshing.value);
const canSaveEdit = computed(() => Boolean(editing.value && editForm.cronExpression && editForm.timezone));
const rows = computed<ScheduleRow[]>(() => {
  const needle = query.value.toLowerCase();
  return schedules.value
    .filter((item) => {
      const text = `${workflowName(item.workflowId)} ${instanceName(item.instanceId)} ${item.cronExpression} ${item.timezone}`.toLowerCase();
      return (!needle || text.includes(needle)) && (!statusFilter.value || scheduleState(item) === statusFilter.value);
    })
    .map((item) => ({ item, state: scheduleState(item), stale: isStale(item) }));
});

const clearMessages = () => {
  errorMessage.value = "";
  successMessage.value = "";
};

const load = async () => {
  const base = session.workspaceBase;
  if (!base) {
    errorMessage.value = "请先选择工作空间";
    return;
  }
  loading.value = true;
  errorMessage.value = "";
  try {
    const [scheduleResult, workflowResult, instanceResult] = await Promise.allSettled([
      api.list<Schedule>(`${base}/schedules`),
      api.list<Workflow>(`${base}/workflows`),
      api.list<BrowserInstance>(`${base}/browser-instances`),
    ]);
    if (scheduleResult.status === "rejected") throw scheduleResult.reason;
    schedules.value = scheduleResult.value;
    loadFailed.value = false;
    if (workflowResult.status === "fulfilled") workflows.value = workflowResult.value;
    if (instanceResult.status === "fulfilled") instances.value = instanceResult.value;
    // Names and pickers degrade to IDs; the reason stays visible.
    const lookupFailure = [workflowResult, instanceResult].find((result): result is PromiseRejectedResult => result.status === "rejected");
    if (lookupFailure) errorMessage.value = `工作流或实例信息加载失败：${describeError(lookupFailure.reason, "请稍后重试")}`;
  } catch (error) {
    loadFailed.value = !schedules.value.length;
    errorMessage.value = `调度计划加载失败：${describeError(error, "请稍后重试")}`;
  } finally {
    loading.value = false;
  }
};

const openCreate = () => {
  clearMessages();
  Object.assign(createForm, { workflowId: "", instanceId: "", cronExpression: "0 9 * * *", timezone: defaultTimezone });
  createError.value = "";
  createOpen.value = true;
};
const closeCreate = () => {
  if (!saving.value) createOpen.value = false;
};

const createSchedule = async () => {
  const workflow = publishedWorkflows.value.find((item) => item.id === createForm.workflowId);
  if (!workflow?.publishedVersionId || !canCreate.value || saving.value) return;
  const problem = cronProblem(createForm.cronExpression) || timezoneProblem(createForm.timezone);
  if (problem) {
    createError.value = problem;
    return;
  }
  saving.value = true;
  createError.value = "";
  try {
    await api.post<Schedule>(`${session.workspaceBase}/schedules`, {
      workflowId: workflow.id,
      workflowVersionId: workflow.publishedVersionId,
      instanceId: createForm.instanceId,
      cronExpression: createForm.cronExpression,
      timezone: createForm.timezone,
    });
    createOpen.value = false;
    successMessage.value = "调度计划已创建";
    await load();
  } catch (error) {
    createError.value = `创建失败：${scheduleError(error, "请稍后重试", {
      schedule_state_conflict: "该工作流的发布版本不可定时运行（需已发布，且引擎为 Playwright 或 CDP）",
    })}`;
  } finally {
    saving.value = false;
  }
};

const openEdit = async (item: Schedule) => {
  clearMessages();
  const token = ++editToken;
  editing.value = item;
  Object.assign(editForm, { cronExpression: item.cronExpression, timezone: item.timezone });
  editError.value = "";
  editOpen.value = true;
  // Every dispatch bumps the version, so edit the current server copy.
  editRefreshing.value = true;
  try {
    const fresh = await api.get<Schedule>(schedulePath(item.id));
    if (token !== editToken) return;
    editing.value = fresh;
    Object.assign(editForm, { cronExpression: fresh.cronExpression, timezone: fresh.timezone });
  } catch (error) {
    if (token === editToken) editError.value = `未能获取最新计划，当前显示列表数据：${describeError(error, "请稍后重试")}`;
  } finally {
    if (token === editToken) editRefreshing.value = false;
  }
};
const closeEdit = () => {
  if (!saving.value) editOpen.value = false;
};

const saveEdit = async () => {
  const item = editing.value;
  if (!item || !canSaveEdit.value || editBusy.value) return;
  const problem = cronProblem(editForm.cronExpression) || timezoneProblem(editForm.timezone);
  if (problem) {
    editError.value = problem;
    return;
  }
  saving.value = true;
  editError.value = "";
  const patch = (version: number) => api.patch<Schedule>(schedulePath(item.id), {
    cronExpression: editForm.cronExpression,
    timezone: editForm.timezone,
    expectedVersion: version,
  });
  try {
    try {
      await patch(item.version);
    } catch (error) {
      // A stale version and an unschedulable workflow share schedule_state_conflict;
      // an unchanged version means the workflow side rejected the update.
      if (!(error instanceof ApiError && error.code === "schedule_state_conflict")) throw error;
      const fresh = await api.get<Schedule>(schedulePath(item.id));
      if (fresh.version === item.version) throw error;
      if (fresh.cronExpression !== item.cronExpression || fresh.timezone !== item.timezone) {
        editing.value = fresh;
        editError.value = "计划已被他人修改；再次保存将以当前表单覆盖。";
        return;
      }
      // Only the dispatcher touched the schedule (runs bump the version): save on top of it.
      await patch(fresh.version);
    }
    editOpen.value = false;
    successMessage.value = "调度计划已更新";
    await load();
  } catch (error) {
    editError.value = `保存失败：${scheduleError(error, "请稍后重试", { schedule_state_conflict: unschedulableText })}`;
  } finally {
    saving.value = false;
  }
};

const toggle = async (item: Schedule) => {
  if (togglingId.value) return;
  const state = scheduleState(item);
  const action = toggleLabels[state];
  clearMessages();
  togglingId.value = item.id;
  try {
    await api.post<Schedule>(`${schedulePath(item.id)}/${state === "enabled" ? "disable" : "enable"}`);
    successMessage.value = `调度计划已${action}`;
    await load();
  } catch (error) {
    // Resuming recomputes the next run from the saved rule, which can fail.
    errorMessage.value = `${action}失败：${scheduleError(error, "请稍后重试")}`;
  } finally {
    togglingId.value = "";
  }
};

const askDelete = (item: Schedule) => {
  clearMessages();
  confirm.ask({
    title: "删除调度计划",
    message: `确定删除「${workflowName(item.workflowId)}」在「${instanceName(item.instanceId)}」上的计划吗？\n删除后不可恢复，已排队的任务不受影响。`,
    confirmText: "删除",
    danger: true,
  }, async () => {
    await api.delete(schedulePath(item.id));
    successMessage.value = "调度计划已删除";
    await load();
  });
};

onMounted(() => void load());
</script>

<style scoped>
.schedule-alert{margin-bottom:15px}
.cron-preset{margin-right:10px}
.cron-preset:disabled{opacity:.5;cursor:not-allowed}
.schedule-delete:hover{background:#fff0f0;color:var(--red)}
</style>
