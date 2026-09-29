<template>
  <ModalDialog :open="open" :title="isAssign ? '分配到实例' : '解除分配'" :description="description" @close="close">
    <div v-if="error" class="alert alert-danger assignment-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ error }}</span></div>
    <div v-if="loadError" class="alert alert-danger assignment-alert" role="alert">
      <Icon icon="lucide:circle-alert" /><span>实例加载失败：{{ loadError }}</span>
      <button class="button button-quiet small" type="button" :disabled="loading" @click="loadInstances">重试</button>
    </div>
    <label class="field">
      <span>浏览器实例</span>
      <select v-model="targetId" :disabled="loading || saving || !instances.length">
        <option value="" disabled>{{ placeholder }}</option>
        <option v-for="item in instances" :key="item.id" :value="item.id">{{ optionLabel(item) }}</option>
      </select>
    </label>
    <p class="form-note"><Icon icon="lucide:info" /><span>{{ note }}</span></p>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button" :class="isAssign ? 'button-primary' : 'button-danger'" type="button" :disabled="saving || loading || !targetId" @click="submit">
        <Icon v-if="saving" icon="lucide:loader-circle" class="spin" />{{ saving ? "处理中…" : isAssign ? "分配" : "解除分配" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api, describeError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { useSessionStore } from "@/stores/session";
import type { BrowserInstance, ProxyAssignment, ProxyAssignmentTargetType, ProxyNode } from "@/types";
import { assignmentKey, type AssignmentMode, type AssignmentResult } from "./assignment";
import { protocolLabel, stackLabel } from "./protocols";

type Phase = "assign" | "unassign";

const TARGET_TYPE: ProxyAssignmentTargetType = "browser_instance";

const props = defineProps<{
  open: boolean;
  proxy: ProxyNode | null;
  mode: AssignmentMode;
  /** Instance assignments loaded with the page; they only mark options, and unassigning re-reads the target. */
  known: Record<string, ProxyAssignment>;
}>();
const emit = defineEmits<{ close: []; done: [result: AssignmentResult] }>();

const session = useSessionStore();
const instances = ref<BrowserInstance[]>([]);
const targetId = ref("");
const loading = ref(false);
const loadError = ref("");
const saving = ref(false);
const error = ref("");
let loadToken = 0;

const stateNames: Record<string, string> = {
  offline: "离线", starting: "启动中", running: "运行中", stopping: "停止中", migrating: "迁移中", failed: "异常",
};
const fallbacks: Record<Phase, string> = { assign: "分配失败", unassign: "解除分配失败" };
const overrides: Record<Phase, Record<string, string>> = {
  assign: { proxy_assignment_conflict: "该实例已分配其他代理，每个实例只能分配一个代理", not_found: "代理或实例不存在，请刷新后重试" },
  unassign: { version_conflict: "分配刚刚发生变化，请重试", not_found: "该实例当前没有代理分配" },
};

const isAssign = computed(() => props.mode === "assign");
const description = computed(() => props.proxy ? `${props.proxy.name} · ${stackLabel(props.proxy.connectorType)} · ${protocolLabel(props.proxy.protocol)}` : "");
const placeholder = computed(() => loading.value ? "正在加载实例…" : instances.value.length ? "请选择实例" : "暂无浏览器实例");
const note = computed(() => isAssign.value
  ? `每个实例同一时间只分配一个代理；实例按该代理所属连接栈（${stackLabel(props.proxy?.connectorType)}）执行，不会跨栈回退。`
  : "解除后该实例不再使用此代理；若实例分配的是其他代理，不会做任何更改。");

const assignedHere = (instanceId: string) => Boolean(props.proxy) && props.known[assignmentKey(TARGET_TYPE, instanceId)]?.proxyId === props.proxy?.id;
const optionLabel = (item: BrowserInstance) =>
  `${item.name} · ${stateNames[item.observedState] ?? item.observedState}${assignedHere(item.id) ? " · 已分配此代理" : ""}`;

const loadInstances = async () => {
  const base = session.workspaceBase;
  if (!base) {
    loadError.value = "请先选择工作空间";
    return;
  }
  const token = ++loadToken;
  loading.value = true;
  loadError.value = "";
  try {
    const items = await api.list<BrowserInstance>(`${base}/browser-instances`);
    if (token !== loadToken) return;
    instances.value = items.filter((item) => !item.deletedAt);
    if (!isAssign.value && !targetId.value) targetId.value = instances.value.find((item) => assignedHere(item.id))?.id ?? "";
  } catch (err) {
    if (token === loadToken) loadError.value = describeError(err, "请稍后重试");
  } finally {
    if (token === loadToken) loading.value = false;
  }
};

watch(() => props.open, (open) => {
  if (!open) return;
  targetId.value = "";
  error.value = "";
  instances.value = [];
  void loadInstances();
}, { immediate: true });

const close = () => {
  if (!saving.value) emit("close");
};

const submit = async () => {
  const proxy = props.proxy;
  const base = session.workspaceBase;
  const target = instances.value.find((item) => item.id === targetId.value);
  if (!proxy || !base || !target || saving.value) return;
  saving.value = true;
  error.value = "";
  const phase: Phase = isAssign.value ? "assign" : "unassign";
  const targetPath = `${base}/proxy-assignments/${TARGET_TYPE}/${encodeURIComponent(target.id)}`;
  try {
    if (isAssign.value) {
      // Idempotent for the same proxy and target; another proxy is rejected.
      const assignment = await api.post<ProxyAssignment>(`${base}/proxies/${encodeURIComponent(proxy.id)}/assignments`, {
        targetId: target.id,
        targetType: TARGET_TYPE,
      });
      emit("done", { mode: props.mode, assignment, targetName: target.name });
      return;
    }
    // DELETE is scoped to the target, so read it first: the version fences a
    // concurrent change and another proxy's assignment is left untouched.
    const current = await api.get<ProxyAssignment>(targetPath);
    if (current.proxyId !== proxy.id) {
      error.value = `该实例分配的是「${current.proxyName || "其他代理"}」，未做更改`;
      return;
    }
    await api.delete(`${targetPath}?version=${current.version}`);
    emit("done", { mode: props.mode, assignment: current, targetName: target.name });
  } catch (err) {
    error.value = `${fallbacks[phase]}：${describeError(err, "请稍后重试", overrides[phase])}`;
  } finally {
    saving.value = false;
  }
};
</script>

<style scoped>
.assignment-alert{margin-bottom:15px}
.assignment-alert .button{margin-left:auto}
</style>
