<template>
  <div class="page-stack">
    <PageHeader title="任务中心" description="跟踪批量操作、自动化运行与代理检查的统一任务队列" />

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>
    <div v-if="successMessage" class="alert alert-success" role="status"><Icon icon="lucide:circle-check" /><span>{{ successMessage }}</span></div>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索任务 ID、类型或工作流…" aria-label="搜索任务" /></label>
        <select v-model="statusFilter" aria-label="按状态筛选">
          <option value="">全部状态</option>
          <option v-for="item in taskStatuses" :key="item" :value="item">{{ taskStatusLabel(item) }}</option>
        </select>
        <select v-model="typeFilter" aria-label="按类型筛选">
          <option value="">全部类型</option>
          <option v-for="item in typeOptions" :key="item" :value="item">{{ taskTypeLabel(item) }}</option>
        </select>
        <span class="toolbar-spacer" />
        <span v-if="tasks.length" class="muted-text task-count">最近 {{ tasks.length }} 条</span>
        <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新</button>
      </div>
      <div class="table-wrap">
        <table v-if="filtered.length" class="data-table">
          <thead><tr><th>任务</th><th>状态</th><th>重试上限</th><th>创建时间</th><th>更新时间</th><th class="actions-cell">操作</th></tr></thead>
          <tbody>
            <tr v-for="item in filtered" :key="item.id">
              <td>
                <div class="primary-cell">
                  <span class="row-icon"><Icon :icon="taskTypeIcon(item.taskType)" /></span>
                  <div><strong>{{ taskTypeLabel(item.taskType) }}</strong><small class="mono">{{ shortId(item.id) }}</small></div>
                </div>
              </td>
              <td>
                <StatusBadge :status="item.status" :label="taskStatusLabel(item.status)" />
                <small v-if="item.errorMessage || item.errorCode" class="row-error">{{ item.errorMessage || item.errorCode }}</small>
              </td>
              <td>{{ item.retryLimit }} 次</td>
              <td>{{ formatDateTime(item.createdAt) }}</td>
              <td>{{ formatDateTime(item.updatedAt) }}</td>
              <td class="actions-cell">
                <button class="button button-quiet small" type="button" @click="openDetail(item)"><Icon icon="lucide:file-text" />详情</button>
                <button v-if="canOperate && isTaskCancellable(item.status)" class="button button-quiet small task-cancel" type="button" @click="askCancel(item)"><Icon icon="lucide:ban" />终止</button>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-else-if="loading" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载任务…</div>
        <EmptyState v-else-if="loadFailed" icon="lucide:cloud-off" title="任务列表加载失败" description="请检查网络或稍后重试。">
          <button class="button button-secondary" type="button" @click="load">重试</button>
        </EmptyState>
        <EmptyState v-else-if="tasks.length" icon="lucide:search-x" title="没有匹配的任务" description="调整搜索词或筛选条件。" />
        <EmptyState v-else icon="lucide:list-checks" title="还没有任务" description="批量操作、自动化运行与代理健康检查都会进入统一任务队列。" />
      </div>
    </article>

    <TaskDetailDialog :open="detailOpen" :task-id="detailId" :initial="detailInitial" :can-cancel="canOperate" @close="detailOpen = false" @cancel="cancelFromDetail" @loaded="mergeTask" />
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Icon } from "@iconify/vue";
import { api, describeError } from "@/api/client";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import TaskDetailDialog from "@/components/task/TaskDetailDialog.vue";
import { isTaskCancellable, taskStatuses, taskStatusLabel, taskTypeIcon, taskTypeLabel } from "@/components/task/taskLabels";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { TaskRun } from "@/types";
import { formatDateTime, shortId } from "@/utils/format";

/** The server caps list pages at 200 (larger values fall back to 100). */
const LIST_LIMIT = 200;

const session = useSessionStore();
const confirm = useConfirm();
const canOperate = computed(() => session.can("task.operate"));

const tasks = ref<TaskRun[]>([]);
const loading = ref(false);
const loadFailed = ref(false);
const errorMessage = ref("");
const successMessage = ref("");
const query = ref("");
const statusFilter = ref("");
const typeFilter = ref("");
const detailOpen = ref(false);
const detailId = ref("");
const detailInitial = ref<TaskRun | null>(null);

const typeOptions = computed(() => [...new Set(tasks.value.map((item) => item.taskType))].sort());
const filtered = computed(() => {
  const needle = query.value.toLowerCase();
  return tasks.value.filter((item) => {
    const text = `${item.id} ${item.taskType} ${taskTypeLabel(item.taskType)} ${item.workflowId ?? ""} ${item.errorCode ?? ""}`.toLowerCase();
    return (!needle || text.includes(needle)) && (!statusFilter.value || item.status === statusFilter.value) && (!typeFilter.value || item.taskType === typeFilter.value);
  });
});

const clearMessages = () => {
  errorMessage.value = "";
  successMessage.value = "";
};

const load = async () => {
  const base = session.workspaceBase;
  if (!base) {
    tasks.value = [];
    errorMessage.value = "请先选择工作空间";
    return;
  }
  loading.value = true;
  errorMessage.value = "";
  try {
    tasks.value = await api.list<TaskRun>(`${base}/tasks?limit=${LIST_LIMIT}`);
    loadFailed.value = false;
  } catch (error) {
    loadFailed.value = !tasks.value.length;
    errorMessage.value = `任务列表加载失败：${describeError(error, "请稍后重试")}`;
  } finally {
    loading.value = false;
  }
};

const mergeTask = (task: TaskRun) => {
  tasks.value = tasks.value.map((item) => (item.id === task.id ? task : item));
};

const openDetail = (task: TaskRun) => {
  detailInitial.value = task;
  detailId.value = task.id;
  detailOpen.value = true;
};

const askCancel = (task: TaskRun) => {
  clearMessages();
  confirm.ask({
    title: "终止任务",
    message: `确定终止「${taskTypeLabel(task.taskType)} ${shortId(task.id)}」吗？\n终止后不会再被领取或重试，已执行的步骤不会回滚。`,
    confirmText: "终止任务",
    danger: true,
  }, async () => {
    try {
      const updated = await api.post<TaskRun>(`${session.workspaceBase}/tasks/${encodeURIComponent(task.id)}/cancel`);
      mergeTask(updated);
    } catch (error) {
      throw new Error(describeError(error, "终止失败，请稍后重试", { task_state_conflict: "任务已结束，无法终止" }));
    }
    successMessage.value = `任务 ${shortId(task.id)} 已终止`;
    await load();
  });
};

const cancelFromDetail = (task: TaskRun) => {
  detailOpen.value = false;
  askCancel(task);
};

onMounted(() => void load());
</script>

<style scoped>
.task-count{font-size:12px}
.task-cancel:hover{background:#fff0f0;color:var(--red)}
</style>
