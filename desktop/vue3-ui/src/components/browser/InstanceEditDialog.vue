<template>
  <ModalDialog :open="open" title="编辑实例" :description="baseline?.name" @close="close">
    <form :id="formId" class="dialog-form" novalidate @submit.prevent="save">
      <div class="form-grid">
        <label class="field field-span">
          <span>实例名称</span>
          <input ref="nameInput" v-model="name" :maxlength="MAX_NAME_LENGTH" autocomplete="off" :aria-invalid="!!nameProblem" />
          <small v-if="nameProblem" class="text-danger">{{ nameProblem }}</small>
        </label>
        <label class="field field-span">
          <span>标签</span>
          <input v-model="tagsText" placeholder="多个标签用逗号分隔" autocomplete="off" :aria-invalid="!!tagsProblem" />
          <small :class="{ 'text-danger': tagsProblem }">{{ tagsProblem || `最多 ${MAX_TAGS} 个，每个不超过 ${MAX_TAG_LENGTH} 个字符` }}</small>
        </label>
        <label class="field field-span">
          <span>指纹模板</span>
          <select v-model="fingerprintTemplateId" :disabled="locked || templatesLoading" :aria-busy="templatesLoading">
            <option value="">不绑定指纹模板</option>
            <option v-if="orphanTemplateId" :value="orphanTemplateId">当前模板（{{ shortId(orphanTemplateId) }}）</option>
            <option v-for="template in templates" :key="template.id" :value="template.id">
              {{ template.name }} · Chromium {{ template.browserMajor }} · {{ platformLabel(template.platform) }}
            </option>
          </select>
          <small v-if="templatesLoading">正在加载指纹模板…</small>
        </label>
        <label class="field field-span">
          <span>运行设备</span>
          <select v-model="assignedDeviceId" :disabled="locked">
            <option value="">暂不分配</option>
            <option v-if="orphanDeviceId" :value="orphanDeviceId">{{ deviceName(orphanDeviceId) }}（当前）</option>
            <option v-for="device in devices" :key="device.id" :value="device.id">{{ deviceOptionLabel(device) }}</option>
          </select>
        </label>
      </div>
      <div v-if="locked" class="alert alert-warning" role="status">
        <Icon icon="lucide:lock" /><span>实例运行中或迁移中，指纹模板和运行设备暂不可修改，请先停止实例。</span>
      </div>
      <div v-if="templatesError" class="alert alert-danger" role="alert">
        <Icon icon="lucide:triangle-alert" /><span>{{ templatesError }}</span>
        <button class="button button-secondary small" type="button" :disabled="templatesLoading" @click="emit('retry-templates')">重试</button>
      </div>
      <div v-if="missing" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>实例已不存在，可能已被删除。</span></div>
      <div v-if="error" class="alert alert-danger" role="alert"><Icon icon="lucide:triangle-alert" /><span>{{ error }}</span></div>
    </form>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="!canSave">{{ saving ? "正在保存…" : "保存" }}</button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { ApiError, api, describeError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import {
  MAX_NAME_LENGTH,
  MAX_TAG_LENGTH,
  MAX_TAGS,
  deviceOptionLabel,
  isRuntimeActive,
  nameError,
  parseTags,
  platformLabel,
  sameTags,
  tagsError,
} from "@/components/browser/instanceState";
import type { AgentDevice, BrowserInstance, FingerprintTemplate } from "@/types";
import { shortId } from "@/utils/format";

/** Fields of browserinstanceservice.UpdateInput edited here; the PATCH handler adds expectedVersion. */
interface InstanceUpdateFields {
  name?: string;
  tags?: string[];
  fingerprintTemplateId?: string;
  assignedDeviceId?: string;
}

const props = defineProps<{
  open: boolean;
  /** Live row from the parent list, so a save uses the latest known version. */
  instance: BrowserInstance | null;
  /** Assignable devices of the active workspace. */
  devices: AgentDevice[];
  deviceName: (id: string) => string;
  templates: FingerprintTemplate[];
  templatesLoading: boolean;
  templatesError: string;
  takenNames: string[];
  workspaceBase: string;
}>();
const emit = defineEmits<{ close: []; saved: [instance: BrowserInstance]; refresh: []; "retry-templates": [] }>();

const formId = "instance-edit-form";
const nameInput = ref<HTMLInputElement>();
/** Values when the dialog opened; only fields changed from these are sent. */
const baseline = ref<BrowserInstance | null>(null);
const name = ref("");
const tagsText = ref("");
const initialTagsText = ref("");
const fingerprintTemplateId = ref("");
const assignedDeviceId = ref("");
const saving = ref(false);
const error = ref("");

watch(() => props.open, async (open) => {
  if (!open) return;
  const item = props.instance;
  baseline.value = item;
  name.value = item?.name ?? "";
  initialTagsText.value = (item?.tags ?? []).join(", ");
  tagsText.value = initialTagsText.value;
  fingerprintTemplateId.value = item?.fingerprintTemplateId ?? "";
  assignedDeviceId.value = item?.assignedDeviceId ?? "";
  error.value = "";
  await nextTick();
  nameInput.value?.focus();
}, { immediate: true });

const missing = computed(() => props.open && !props.instance);
const locked = computed(() => Boolean(baseline.value && isRuntimeActive(baseline.value)));
const trimmedName = computed(() => name.value.trim());
const nameProblem = computed(() => {
  if (!baseline.value || trimmedName.value === baseline.value.name) return "";
  return nameError(trimmedName.value, props.takenNames);
});
const parsedTags = computed(() => parseTags(tagsText.value));
const tagsChanged = computed(() => tagsText.value !== initialTagsText.value && !sameTags(parsedTags.value, baseline.value?.tags ?? []));
const tagsProblem = computed(() => (tagsChanged.value ? tagsError(parsedTags.value) : ""));
const orphanTemplateId = computed(() => {
  const id = baseline.value?.fingerprintTemplateId;
  return id && !props.templates.some((template) => template.id === id) ? id : "";
});
const orphanDeviceId = computed(() => {
  const id = baseline.value?.assignedDeviceId;
  return id && !props.devices.some((device) => device.id === id) ? id : "";
});

const changes = computed<InstanceUpdateFields>(() => {
  const item = baseline.value;
  const fields: InstanceUpdateFields = {};
  if (!item) return fields;
  if (trimmedName.value !== item.name) fields.name = trimmedName.value;
  if (tagsChanged.value) fields.tags = parsedTags.value;
  if (fingerprintTemplateId.value !== (item.fingerprintTemplateId ?? "")) fields.fingerprintTemplateId = fingerprintTemplateId.value;
  if (assignedDeviceId.value !== (item.assignedDeviceId ?? "")) fields.assignedDeviceId = assignedDeviceId.value;
  return fields;
});
const canSave = computed(() =>
  !saving.value && Boolean(props.instance) && Object.keys(changes.value).length > 0 && !nameProblem.value && !tagsProblem.value,
);

const save = async () => {
  const item = props.instance;
  if (!item || !canSave.value) return;
  const fields = changes.value;
  saving.value = true;
  error.value = "";
  try {
    const updated = await api.patch<BrowserInstance>(`${props.workspaceBase}/browser-instances/${item.id}`, {
      ...fields,
      expectedVersion: item.version,
    });
    emit("saved", updated);
  } catch (err) {
    // Agent state reports also bump the version; the parent reloads so a retry uses the new one.
    if (err instanceof ApiError && err.code === "version_conflict") emit("refresh");
    error.value = describeError(err, "保存失败，请稍后重试", {
      instance_state_conflict: "实例运行中或迁移中，服务端拒绝修改指纹模板或运行设备，请先停止实例。",
      instance_validation_failed: "名称需为 1–120 个字符，所选模板和设备须属于当前工作空间。",
      version_conflict: "实例刚刚发生变化，已刷新最新版本，请再次保存。",
      instance_name_conflict: "保存失败：已有同名实例（不区分大小写），请更换名称。",
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
</style>
