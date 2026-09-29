<template>
  <ModalDialog :open="open" title="迁移到其他设备" :description="baseline?.name" @close="close">
    <form :id="formId" class="dialog-form" novalidate @submit.prevent="submit">
      <div class="form-grid">
        <div class="field field-span">
          <span>当前设备</span>
          <p class="static-value">{{ currentDevice }}</p>
        </div>
        <label class="field field-span">
          <span>目标设备</span>
          <select ref="targetSelect" v-model="targetDeviceId" :disabled="!targets.length">
            <option value="" disabled>{{ targets.length ? "选择目标设备" : "没有其他可用设备" }}</option>
            <option v-for="device in targets" :key="device.id" :value="device.id">{{ deviceOptionLabel(device) }}</option>
          </select>
          <small>仅列出当前工作空间中由你注册且未撤销的设备。</small>
        </label>
      </div>
      <div v-if="targetOffline" class="alert alert-warning" role="status">
        <Icon icon="lucide:wifi-off" /><span>目标设备当前离线，迁移完成后可能无法自动启动。</span>
      </div>
      <p class="form-note"><Icon icon="lucide:info" /><span>源设备会停止实例并上传云端档案，完成后在目标设备自动启动。迁移期间不能删除实例，也不能修改设备或指纹模板。</span></p>
      <div v-if="missing" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>实例已不存在，可能已被删除。</span></div>
      <div v-if="error" class="alert alert-danger" role="alert"><Icon icon="lucide:triangle-alert" /><span>{{ error }}</span></div>
    </form>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="!canSubmit">{{ saving ? "正在提交…" : "开始迁移" }}</button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { ApiError, api, describeError, idempotencyKey } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { deviceOptionLabel, isMigrating, migrationTargets } from "@/components/browser/instanceState";
import type { AgentDevice, BrowserInstance, InstanceCommandResult } from "@/types";

const props = defineProps<{
  open: boolean;
  /** Live row from the parent list, so the command carries the latest version. */
  instance: BrowserInstance | null;
  /** Assignable devices of the active workspace; the current device is excluded here. */
  devices: AgentDevice[];
  deviceName: (id: string) => string;
  workspaceBase: string;
}>();
const emit = defineEmits<{ close: []; migrated: [result: InstanceCommandResult, targetDeviceId: string]; refresh: [] }>();

const formId = "instance-migrate-form";
const targetSelect = ref<HTMLSelectElement>();
const baseline = ref<BrowserInstance | null>(null);
const targetDeviceId = ref("");
const saving = ref(false);
const error = ref("");

const targets = computed(() => migrationTargets(props.devices, props.instance ?? baseline.value));

watch(() => props.open, async (open) => {
  if (!open) return;
  baseline.value = props.instance;
  targetDeviceId.value = targets.value.length === 1 ? targets.value[0].id : "";
  error.value = "";
  await nextTick();
  targetSelect.value?.focus();
}, { immediate: true });

const missing = computed(() => props.open && !props.instance);
const currentDevice = computed(() => {
  const id = (props.instance ?? baseline.value)?.assignedDeviceId;
  return id ? props.deviceName(id) : "未分配";
});
const selectedTarget = computed(() => targets.value.find((device) => device.id === targetDeviceId.value));
const targetOffline = computed(() => selectedTarget.value?.status.toLowerCase() === "offline");
// Mirrors RequestCommand validation: a source device and a cloud profile are required.
const canSubmit = computed(() => {
  const item = props.instance;
  if (saving.value || !item?.assignedDeviceId || !item.profileId || isMigrating(item)) return false;
  return Boolean(selectedTarget.value);
});

const submit = async () => {
  const item = props.instance;
  const target = selectedTarget.value;
  if (!item || !target || !canSubmit.value) return;
  saving.value = true;
  error.value = "";
  try {
    // CommandInput: instance.migrate requires exactly { targetDeviceId } as payload.
    const result = await api.post<InstanceCommandResult>(
      `${props.workspaceBase}/browser-instances/${item.id}/commands`,
      { action: "instance.migrate", expectedVersion: item.version, payload: { targetDeviceId: target.id } },
      idempotencyKey("instance-migrate"),
    );
    emit("migrated", result, target.id);
  } catch (err) {
    if (err instanceof ApiError && err.code === "version_conflict") emit("refresh");
    error.value = describeError(err, "迁移请求失败，请稍后重试", {
      instance_validation_failed: "无法迁移：实例需绑定云端档案，源设备和目标设备须属于当前工作空间且未撤销。",
      version_conflict: "实例刚刚发生变化，已刷新最新版本，请再次提交。",
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

.static-value {
  min-height: 39px;
  display: flex;
  align-items: center;
  margin: 0;
  padding: 0 11px;
  background: var(--surface-2);
  border: 1px solid var(--line);
  border-radius: 8px;
  font-weight: 600;
}
</style>
