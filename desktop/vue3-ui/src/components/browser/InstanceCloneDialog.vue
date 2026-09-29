<template>
  <ModalDialog :open="open" title="克隆实例" :description="baseline ? `基于「${baseline.name}」创建新实例` : undefined" @close="close">
    <form :id="formId" class="dialog-form" novalidate @submit.prevent="submit">
      <div class="form-grid">
        <label class="field field-span">
          <span>新实例名称</span>
          <input ref="nameInput" v-model="name" :maxlength="MAX_NAME_LENGTH" autocomplete="off" :aria-invalid="!!nameProblem" />
          <small v-if="nameProblem" class="text-danger">{{ nameProblem }}</small>
        </label>
        <label class="field field-span">
          <span>运行设备</span>
          <select v-model="assignedDeviceId">
            <option value="">暂不分配</option>
            <option v-if="orphanDeviceId" :value="orphanDeviceId">{{ deviceName(orphanDeviceId) }}（源实例设备）</option>
            <option v-for="device in devices" :key="device.id" :value="device.id">{{ deviceOptionLabel(device) }}</option>
          </select>
        </label>
      </div>
      <p class="form-note"><Icon icon="lucide:info" /><span>{{ copyNote }}</span></p>
      <div v-if="missing" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>源实例已不存在，可能已被删除。</span></div>
      <div v-if="error" class="alert alert-danger" role="alert"><Icon icon="lucide:triangle-alert" /><span>{{ error }}</span></div>
    </form>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="!canSubmit">{{ saving ? "正在克隆…" : "克隆" }}</button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api, describeError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { MAX_NAME_LENGTH, deviceOptionLabel, nameError, suggestCopyName } from "@/components/browser/instanceState";
import type { AgentDevice, BrowserInstance } from "@/types";

/**
 * browserinstanceservice.CloneInput. Omitting assignedDeviceId keeps the source
 * device and "" leaves the clone unassigned. Tags and the fingerprint template
 * are always copied; profile and proxy assignment never are.
 */
interface CloneBody {
  name: string;
  assignedDeviceId?: string;
}

const props = defineProps<{
  open: boolean;
  instance: BrowserInstance | null;
  /** Assignable devices of the active workspace. */
  devices: AgentDevice[];
  deviceName: (id: string) => string;
  takenNames: string[];
  workspaceBase: string;
}>();
const emit = defineEmits<{ close: []; cloned: [instance: BrowserInstance] }>();

const formId = "instance-clone-form";
const nameInput = ref<HTMLInputElement>();
const baseline = ref<BrowserInstance | null>(null);
const name = ref("");
const assignedDeviceId = ref("");
const saving = ref(false);
const error = ref("");

watch(() => props.open, async (open) => {
  if (!open) return;
  const item = props.instance;
  baseline.value = item;
  name.value = item ? suggestCopyName(item.name, props.takenNames) : "";
  assignedDeviceId.value = item?.assignedDeviceId ?? "";
  error.value = "";
  await nextTick();
  nameInput.value?.select();
}, { immediate: true });

const missing = computed(() => props.open && !props.instance);
const trimmedName = computed(() => name.value.trim());
const nameProblem = computed(() => nameError(trimmedName.value, props.takenNames));
const orphanDeviceId = computed(() => {
  const id = baseline.value?.assignedDeviceId;
  return id && !props.devices.some((device) => device.id === id) ? id : "";
});
const copyNote = computed(() => {
  const copied: string[] = [];
  if (baseline.value?.fingerprintTemplateId) copied.push("指纹模板");
  const tags = baseline.value?.tags ?? [];
  if (tags.length) copied.push(`标签（${tags.join("、")}）`);
  return `${copied.length ? `将复制${copied.join("和")}；` : ""}云端档案和代理不会复制。`;
});
const canSubmit = computed(() => !saving.value && Boolean(props.instance) && !nameProblem.value);

const submit = async () => {
  const source = props.instance;
  if (!source || !canSubmit.value) return;
  const body: CloneBody = { name: trimmedName.value };
  if (assignedDeviceId.value !== (source.assignedDeviceId ?? "")) body.assignedDeviceId = assignedDeviceId.value;
  saving.value = true;
  error.value = "";
  try {
    const created = await api.post<BrowserInstance>(`${props.workspaceBase}/browser-instances/${source.id}/clone`, body);
    emit("cloned", created);
  } catch (err) {
    error.value = describeError(err, "克隆失败，请稍后重试", {
      validation_failed: "名称需为 1–120 个字符。",
      not_found: "源实例或所选设备不存在，请刷新后重试。",
    });
  } finally {
    saving.value = false;
  }
};

const close = () => {
  if (!saving.value) emit("close");
};
</script>

<style scoped>
.dialog-form {
  display: grid;
  gap: 16px;
}

.dialog-form .form-note {
  margin: 0;
}
</style>
