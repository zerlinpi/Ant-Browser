<template>
  <div class="page-stack">
    <PageHeader title="自动化中心" description="编排、发布并运行浏览器自动化工作流">
      <RouterLink v-if="canManage" class="button button-primary" :to="{ name: 'automation-new' }"><Icon icon="lucide:plus" />新建工作流</RouterLink>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>
    <div v-if="successMessage" class="alert alert-success" role="status"><Icon icon="lucide:circle-check" /><span>{{ successMessage }}</span></div>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索工作流…" aria-label="搜索工作流" /></label>
        <select v-model="status" aria-label="按状态筛选"><option value="">全部状态</option><option value="draft">草稿</option><option value="published">已发布</option><option value="archived">已归档</option></select>
        <span class="toolbar-spacer" />
        <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新</button>
      </div>
      <div v-if="filtered.length" class="workflow-grid">
        <article v-for="item in filtered" :key="item.id" class="workflow-card">
          <header><span class="workflow-icon"><Icon icon="lucide:workflow" /></span><StatusBadge :status="item.status === 'published' ? 'active' : item.status" :label="statusLabel(item.status)" /></header>
          <h2>{{ item.name }}</h2>
          <p v-if="item.status === 'archived'">最新版本 v{{ item.latestVersion }} · 归档于 {{ formatDateTime(item.archivedAt) }}</p>
          <p v-else>最新版本 v{{ item.latestVersion }} · {{ item.publishedVersionId ? "有发布版本" : "尚未发布" }}</p>
          <footer>
            <small>更新于 {{ formatDateTime(item.updatedAt) }}</small>
            <div>
              <RouterLink class="button button-quiet small" :to="{ name: 'automation-edit', params: { workflowId: item.id } }">
                <Icon :icon="editable(item) ? 'lucide:pencil' : 'lucide:eye'" />{{ editable(item) ? "编辑" : "查看" }}
              </RouterLink>
              <button v-if="canRun" class="button button-primary small" type="button" :disabled="!runnable(item)" :title="runnable(item) ? '运行已发布版本' : '仅已发布的工作流可运行'" @click="openRun(item)"><Icon icon="lucide:play" />运行</button>
              <button v-if="canManage && item.status !== 'archived'" class="icon-button" type="button" title="归档" :aria-label="`归档 ${item.name}`" @click="askArchive(item)"><Icon icon="lucide:archive" /></button>
            </div>
          </footer>
        </article>
      </div>
      <div v-else-if="loading" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载工作流…</div>
      <EmptyState v-else-if="loadFailed" icon="lucide:cloud-off" title="工作流加载失败" description="请检查网络或稍后重试。">
        <button class="button button-secondary" type="button" @click="load">重试</button>
      </EmptyState>
      <EmptyState v-else-if="workflows.length" icon="lucide:search-x" title="没有匹配的工作流" description="调整搜索词或状态筛选。" />
      <EmptyState v-else icon="lucide:workflow" title="还没有自动化工作流" description="从打开网页、点击、输入、等待、截图和数据采集等步骤开始编排。">
        <RouterLink v-if="canManage" class="button button-primary" :to="{ name: 'automation-new' }">新建工作流</RouterLink>
      </EmptyState>
    </article>

    <ModalDialog :open="runOpen" title="运行工作流" :description="runWorkflow ? `${runWorkflow.name} · 已发布版本` : ''" @close="closeRun">
      <div v-if="runError" class="alert alert-danger run-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ runError }}</span></div>
      <div v-if="runSubmitted" class="alert alert-success run-alert" role="status">
        <Icon icon="lucide:circle-check" />
        <span>{{ runSubmitted.taskId ? `任务 ${shortId(runSubmitted.taskId)} 已进入队列。` : "任务已进入队列。" }}<RouterLink class="text-link" :to="{ name: 'tasks' }">前往任务中心</RouterLink></span>
      </div>
      <template v-else>
        <div v-if="instancesError" class="alert alert-danger run-alert" role="alert">
          <Icon icon="lucide:circle-alert" /><span>实例加载失败：{{ instancesError }}</span>
          <button class="button button-quiet small" type="button" :disabled="instancesLoading" @click="loadInstances">重试</button>
        </div>
        <label class="field">
          <span>浏览器实例</span>
          <select v-model="runInstanceId" :disabled="instancesLoading || running || !runnableInstances.length">
            <option value="" disabled>{{ instancePlaceholder }}</option>
            <option v-for="instance in runnableInstances" :key="instance.id" :value="instance.id">{{ instance.name }} · {{ instanceStateLabel(instance.observedState) }}</option>
          </select>
        </label>
        <p class="form-note"><Icon icon="lucide:info" /><span>仅列出已分配到桌面设备的实例，任务由该设备执行。设备只领取 Playwright 与 CDP 引擎的工作流。</span></p>
      </template>
      <template #footer>
        <button v-if="runSubmitted" class="button button-primary" type="button" @click="closeRun">完成</button>
        <template v-else>
          <button class="button button-secondary" type="button" :disabled="running" @click="closeRun">取消</button>
          <button class="button button-primary" type="button" :disabled="running || instancesLoading || !runInstanceId" @click="runSelected">
            <Icon :icon="running ? 'lucide:loader-circle' : 'lucide:play'" :class="{ spin: running }" />{{ running ? "提交中…" : "提交运行" }}
          </button>
        </template>
      </template>
    </ModalDialog>

    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Icon } from "@iconify/vue";
import { RouterLink } from "vue-router";
import { api, describeError, formatBatchOutcome, idempotencyKey, summarizeBatch } from "@/api/client";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { BatchResult, BrowserInstance, TaskRun, Workflow } from "@/types";
import { formatDateTime, shortId } from "@/utils/format";

const session = useSessionStore();
const confirm = useConfirm();
const canManage = computed(() => session.can("workflow.manage"));
// Batch workflow execution requires task.operate and instance.operate (plus workflow.read).
const canRun = computed(() => session.can("task.operate") && session.can("instance.operate"));

const workflows = ref<Workflow[]>([]);
const query = ref("");
const status = ref("");
const loading = ref(false);
const loadFailed = ref(false);
const errorMessage = ref("");
const successMessage = ref("");

const runOpen = ref(false);
const runWorkflow = ref<Workflow | null>(null);
const runInstanceId = ref("");
const running = ref(false);
const runError = ref("");
const runSubmitted = ref<{ taskId?: string } | null>(null);
const instances = ref<BrowserInstance[]>([]);
const instancesLoading = ref(false);
const instancesError = ref("");
/** One key per run dialog, so retrying after a network error replays instead of duplicating. */
let runKey = "";

const statusNames: Record<string, string> = { draft: "草稿", published: "已发布", archived: "已归档" };
const stateNames: Record<string, string> = { offline: "离线", starting: "启动中", running: "运行中", stopping: "停止中", migrating: "迁移中", failed: "异常" };

const filtered = computed(() => {
  const needle = query.value.toLowerCase();
  return workflows.value.filter((item) => (!needle || item.name.toLowerCase().includes(needle)) && (!status.value || item.status === status.value));
});
const runnableInstances = computed(() => instances.value.filter((item) => item.assignedDeviceId && !item.deletedAt));
const instancePlaceholder = computed(() => {
  if (instancesLoading.value) return "正在加载实例…";
  return runnableInstances.value.length ? "请选择实例" : "没有已分配到设备的实例";
});

const statusLabel = (value: string) => statusNames[value] ?? value;
const instanceStateLabel = (value: string) => stateNames[value] ?? value;
const editable = (item: Workflow) => canManage.value && item.status !== "archived";
// Archiving keeps publishedVersionId, so the status decides whether a run is allowed.
const runnable = (item: Workflow) => item.status === "published" && Boolean(item.publishedVersionId);
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
    workflows.value = await api.list<Workflow>(`${base}/workflows`);
    loadFailed.value = false;
  } catch (error) {
    loadFailed.value = !workflows.value.length;
    errorMessage.value = `工作流加载失败：${describeError(error, "请稍后重试")}`;
  } finally {
    loading.value = false;
  }
};

const loadInstances = async () => {
  const base = session.workspaceBase;
  if (!base) {
    instancesError.value = "请先选择工作空间";
    return;
  }
  instancesLoading.value = true;
  instancesError.value = "";
  try {
    instances.value = await api.list<BrowserInstance>(`${base}/browser-instances`);
    if (!runnableInstances.value.some((item) => item.id === runInstanceId.value)) runInstanceId.value = "";
  } catch (error) {
    instancesError.value = describeError(error, "请稍后重试");
  } finally {
    instancesLoading.value = false;
  }
};

const openRun = (item: Workflow) => {
  clearMessages();
  runWorkflow.value = item;
  runInstanceId.value = "";
  runError.value = "";
  runSubmitted.value = null;
  runKey = idempotencyKey(`workflow-run-${item.id}`);
  runOpen.value = true;
  void loadInstances();
};

const closeRun = () => {
  if (!running.value) runOpen.value = false;
};

const runSelected = async () => {
  const workflow = runWorkflow.value;
  const base = session.workspaceBase;
  if (!workflow?.publishedVersionId || !runInstanceId.value || !base || running.value) return;
  running.value = true;
  runError.value = "";
  try {
    const result = await api.post<BatchResult<TaskRun>>(
      `${base}/batch/workflow-executions`,
      { items: [{ workflowId: workflow.id, workflowVersionId: workflow.publishedVersionId, instanceId: runInstanceId.value }] },
      runKey,
    );
    // Item failures answer 207 with the same payload; the items decide the outcome.
    const outcome = summarizeBatch(result);
    if (outcome.failed || !outcome.succeeded) {
      runError.value = `运行未提交（${formatBatchOutcome(outcome)}）`;
      return;
    }
    runSubmitted.value = { taskId: result?.items?.find((item) => item.status === "succeeded")?.value?.id };
  } catch (error) {
    runError.value = `运行提交失败：${describeError(error, "请稍后重试")}`;
  } finally {
    running.value = false;
  }
};

const askArchive = (item: Workflow) => {
  clearMessages();
  confirm.ask({
    title: "归档工作流",
    message: `确定归档「${item.name}」吗？\n归档后不能再发布或运行，引用它的调度计划到期时会转为异常。此操作不可撤销。`,
    confirmText: "归档",
    danger: true,
  }, async () => {
    try {
      await api.post<Workflow>(`${session.workspaceBase}/workflows/${encodeURIComponent(item.id)}/archive`, { expectedVersion: item.version });
    } catch (error) {
      throw new Error(describeError(error, "归档失败，请稍后重试", {
        workflow_state_conflict: "工作流已归档",
        version_conflict: "工作流已被更新，请刷新后重试",
      }));
    }
    successMessage.value = `已归档「${item.name}」`;
    await load();
  });
};

onMounted(() => void load());
</script>

<style scoped>
.run-alert{margin-bottom:15px}
.run-alert .button{margin-left:auto}
.run-alert .text-link{margin-left:6px}
.workflow-card footer>div{align-items:center}
</style>
