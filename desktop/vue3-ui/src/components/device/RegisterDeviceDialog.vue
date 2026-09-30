<template>
  <ModalDialog :open="open" title="注册设备" description="为桌面端代理创建设备身份，凭据仅在注册成功后显示一次。" @close="close">
    <div v-if="error" class="alert alert-danger dialog-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ error }}</span></div>
    <form :id="formId" class="form-grid" novalidate @submit.prevent="submit">
      <label class="field field-span">
        <span>工作空间</span>
        <select v-model="form.workspaceId" :disabled="saving" required>
          <option v-for="workspace in workspaces" :key="workspace.id" :value="workspace.id">{{ workspace.name }}</option>
        </select>
        <small>设备只能执行该工作空间内的实例与同步任务。</small>
      </label>
      <label class="field field-span">
        <span>设备名称</span>
        <input v-model="form.name" maxlength="120" required autocomplete="off" placeholder="例如：运营部 Windows 01" :disabled="saving" />
      </label>
      <label class="field">
        <span>平台</span>
        <select v-model="form.platform" :disabled="saving">
          <option v-for="option in devicePlatformOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
        </select>
      </label>
      <label class="field">
        <span>代理版本（可选）</span>
        <input v-model="form.agentVersion" maxlength="64" autocomplete="off" placeholder="例如：2.0.0" :disabled="saving" />
      </label>
    </form>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="saving || !form.workspaceId || !form.name.trim()">
        <Icon :class="{ spin: saving }" :icon="saving ? 'lucide:loader-circle' : 'lucide:key-round'" />{{ saving ? "注册中…" : "注册并生成凭据" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { devicePlatformOptions } from "@/components/device/deviceLabels";
import type { DevicePlatform, DeviceRegistration, Workspace } from "@/types";

/**
 * deviceservice.RegisterInput. `capabilities` is also declared there but is
 * reported by the agent itself, so the console never sends it.
 */
interface RegisterDeviceInput {
  workspaceId: string;
  name: string;
  platform: DevicePlatform;
  agentVersion?: string;
}

const props = defineProps<{ open: boolean; workspaces: Workspace[]; defaultWorkspaceId?: string }>();
const emit = defineEmits<{ close: []; registered: [registration: DeviceRegistration] }>();

const formId = "register-device-form";
const form = reactive({ workspaceId: "", name: "", platform: "windows" as DevicePlatform, agentVersion: "" });
const saving = ref(false);
const error = ref("");

const pickWorkspace = () => {
  const preferred = props.workspaces.find((item) => item.id === props.defaultWorkspaceId);
  return preferred?.id ?? props.workspaces[0]?.id ?? "";
};

watch(() => props.open, (open) => {
  if (!open) return;
  Object.assign(form, { workspaceId: pickWorkspace(), name: "", platform: "windows", agentVersion: "" });
  error.value = "";
}, { immediate: true });

const close = () => {
  if (!saving.value) emit("close");
};

const submit = async () => {
  if (saving.value) return;
  const name = form.name.trim();
  const agentVersion = form.agentVersion.trim();
  if (!props.workspaces.some((item) => item.id === form.workspaceId)) {
    error.value = "请选择工作空间";
    return;
  }
  if (!name || [...name].length > 120) {
    error.value = "设备名称需为 1–120 个字符";
    return;
  }
  saving.value = true;
  error.value = "";
  try {
    const input: RegisterDeviceInput = { workspaceId: form.workspaceId, name, platform: form.platform };
    if (agentVersion) input.agentVersion = agentVersion;
    const registration = await api.post<DeviceRegistration>("/api/v1/devices", input);
    emit("registered", registration);
  } catch (err) {
    error.value = err instanceof ApiError && err.status === 403
      ? "当前角色无权注册设备（需要运营及以上角色）"
      : describeError(err, "设备注册失败，请稍后重试");
  } finally {
    saving.value = false;
  }
};
</script>

<style scoped>
.dialog-alert{margin-bottom:15px}
</style>
