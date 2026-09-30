<template>
  <ModalDialog :open="open" title="任务详情" :description="task ? `${taskTypeLabel(task.taskType)} · ${task.id}` : ''" wide @close="$emit('close')">
    <div v-if="error" class="alert alert-danger task-detail-alert" role="alert">
      <Icon icon="lucide:circle-alert" /><span>{{ error }}</span>
      <button class="button button-quiet small" type="button" :disabled="loading" @click="load">重试</button>
    </div>
    <div v-if="loading && !task" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载任务…</div>
    <template v-if="task">
      <div v-if="task.errorCode || task.errorMessage" class="alert alert-danger task-detail-alert">
        <Icon icon="lucide:circle-x" /><span><strong>{{ task.errorCode || "执行失败" }}</strong><template v-if="task.errorMessage">：{{ task.errorMessage }}</template></span>
      </div>
      <dl class="detail-list task-detail-list">
        <div><dt>状态</dt><dd><StatusBadge :status="task.status" :label="taskStatusLabel(task.status)" /></dd></div>
        <div><dt>类型</dt><dd>{{ taskTypeLabel(task.taskType) }}<small class="cell-subtitle mono">{{ task.taskType }}</small></dd></div>
        <div><dt>优先级 / 重试上限</dt><dd>{{ task.priority }} / {{ task.retryLimit }} 次</dd></div>
        <div><dt>发起人</dt><dd class="mono">{{ task.requestedBy || "系统" }}</dd></div>
        <div v-if="task.workflowId"><dt>工作流</dt><dd class="mono">{{ task.workflowId }}</dd></div>
        <div v-if="task.workflowVersionId"><dt>工作流版本</dt><dd class="mono">{{ task.workflowVersionId }}</dd></div>
        <div v-if="task.leaseOwner"><dt>租约持有者</dt><dd class="mono">{{ task.leaseOwner }}</dd></div>
        <div v-if="task.leaseExpiresAt"><dt>租约到期</dt><dd>{{ formatFullDateTime(task.leaseExpiresAt) }}</dd></div>
        <div><dt>可执行时间</dt><dd>{{ formatFullDateTime(task.availableAt) }}</dd></div>
        <div><dt>创建时间</dt><dd>{{ formatFullDateTime(task.createdAt) }}</dd></div>
        <div><dt>更新时间</dt><dd>{{ formatFullDateTime(task.updatedAt) }}</dd></div>
        <div><dt>完成时间</dt><dd>{{ formatFullDateTime(task.completedAt) }}</dd></div>
        <div class="task-detail-wide"><dt>幂等键</dt><dd class="mono">{{ task.idempotencyKey || "—" }}</dd></div>
      </dl>
      <section class="task-payload" aria-label="任务载荷">
        <header>
          <strong>载荷</strong>
          <button class="button button-quiet small" type="button" @click="copyPayload"><Icon :icon="copyState === 'copied' ? 'lucide:check' : 'lucide:copy'" />{{ copyLabels[copyState] }}</button>
        </header>
        <pre class="code-block">{{ payloadText }}</pre>
      </section>
    </template>
    <template #footer>
      <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新</button>
      <button class="button button-secondary" type="button" @click="$emit('close')">关闭</button>
      <button v-if="task && canCancel && isTaskCancellable(task.status)" class="button button-danger" type="button" @click="$emit('cancel', task)"><Icon icon="lucide:ban" />终止任务</button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api, describeError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useSessionStore } from "@/stores/session";
import type { TaskRun } from "@/types";
import { copyToClipboard, formatFullDateTime } from "@/utils/format";
import { isTaskCancellable, taskStatusLabel, taskTypeLabel } from "./taskLabels";

const props = defineProps<{ open: boolean; taskId: string; initial?: TaskRun | null; canCancel: boolean }>();
const emit = defineEmits<{ close: []; cancel: [task: TaskRun]; loaded: [task: TaskRun] }>();

const session = useSessionStore();
const task = ref<TaskRun | null>(null);
const loading = ref(false);
const error = ref("");
const copyState = ref<"idle" | "copied" | "failed">("idle");
const copyLabels = { idle: "复制", copied: "已复制", failed: "复制失败" } as const;
let loadToken = 0;
let copyTimer: number | undefined;

const payloadText = computed(() => JSON.stringify(task.value?.payload ?? {}, null, 2));

const load = async () => {
  const base = session.workspaceBase;
  if (!base || !props.taskId) {
    error.value = "请先选择工作空间";
    return;
  }
  const token = ++loadToken;
  loading.value = true;
  error.value = "";
  try {
    const value = await api.get<TaskRun>(`${base}/tasks/${encodeURIComponent(props.taskId)}`);
    if (token !== loadToken) return;
    task.value = value;
    emit("loaded", value);
  } catch (err) {
    if (token === loadToken) error.value = `任务详情加载失败：${describeError(err, "请稍后重试")}`;
  } finally {
    if (token === loadToken) loading.value = false;
  }
};

watch(() => [props.open, props.taskId] as const, ([open]) => {
  if (!open) return;
  // Show the list row immediately; the fresh copy replaces it once loaded.
  task.value = props.initial?.id === props.taskId ? props.initial : null;
  copyState.value = "idle";
  void load();
}, { immediate: true });

const copyPayload = async () => {
  copyState.value = (await copyToClipboard(payloadText.value)) ? "copied" : "failed";
  window.clearTimeout(copyTimer);
  copyTimer = window.setTimeout(() => { copyState.value = "idle"; }, 1500);
};

onBeforeUnmount(() => window.clearTimeout(copyTimer));
</script>

<style scoped>
.task-detail-alert{margin-bottom:14px}
.task-detail-alert .button{margin-left:auto}
.task-detail-list{padding:0 0 18px}
.task-detail-wide{grid-column:1/-1}
.task-payload header{display:flex;align-items:center;justify-content:space-between;margin-bottom:8px;font-size:13px}
</style>
