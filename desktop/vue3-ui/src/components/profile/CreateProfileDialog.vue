<template>
  <ModalDialog :open="open" title="新建档案" description="档案保存浏览器数据的云端版本，由桌面端代理加密上传与恢复。" @close="close">
    <div v-if="error" class="alert alert-danger dialog-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ error }}</span></div>
    <form :id="formId" class="form-grid" novalidate @submit.prevent="submit">
      <label class="field field-span">
        <span>档案名称</span>
        <input v-model="form.name" maxlength="120" required autocomplete="off" placeholder="例如：Amazon US 店铺 01" :disabled="saving" />
      </label>
      <label class="field field-span">
        <span>指纹模板（可选）</span>
        <select v-model="form.fingerprintTemplateId" :disabled="saving || templatesLoading || !templates.length" :aria-busy="templatesLoading">
          <option value="">不绑定指纹模板</option>
          <option v-for="template in templates" :key="template.id" :value="template.id">{{ template.name }}</option>
        </select>
        <small v-if="templatesLoading">正在加载指纹模板…</small>
        <small v-else-if="templatesError">{{ templatesError }}，可不绑定直接创建。</small>
        <small v-else-if="!templates.length">当前工作空间暂无指纹模板。</small>
      </label>
    </form>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="saving || !form.name.trim()">
        <Icon :class="{ spin: saving }" :icon="saving ? 'lucide:loader-circle' : 'lucide:plus'" />{{ saving ? "创建中…" : "创建档案" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api, describeError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { useSessionStore } from "@/stores/session";
import type { CloudProfile, FingerprintTemplate } from "@/types";

/** profilesyncservice.CreateProfileInput */
interface CreateProfileInput {
  name: string;
  fingerprintTemplateId?: string;
}

const props = defineProps<{ open: boolean; templates: FingerprintTemplate[]; templatesLoading?: boolean; templatesError?: string }>();
const emit = defineEmits<{ close: []; created: [profile: CloudProfile] }>();

const session = useSessionStore();
const formId = "create-profile-form";
const form = reactive({ name: "", fingerprintTemplateId: "" });
const saving = ref(false);
const error = ref("");

watch(() => props.open, (open) => {
  if (!open) return;
  Object.assign(form, { name: "", fingerprintTemplateId: "" });
  error.value = "";
}, { immediate: true });

// Drop a selection that no longer exists after the template list reloads.
watch(() => props.templates, (templates) => {
  if (form.fingerprintTemplateId && !templates.some((item) => item.id === form.fingerprintTemplateId)) form.fingerprintTemplateId = "";
});

const close = () => {
  if (!saving.value) emit("close");
};

const submit = async () => {
  if (saving.value) return;
  const name = form.name.trim();
  if (!name || [...name].length > 120) {
    error.value = "档案名称需为 1–120 个字符";
    return;
  }
  if (!session.workspaceBase) {
    error.value = "请先选择工作空间";
    return;
  }
  saving.value = true;
  error.value = "";
  try {
    const input: CreateProfileInput = { name };
    if (form.fingerprintTemplateId) input.fingerprintTemplateId = form.fingerprintTemplateId;
    const profile = await api.post<CloudProfile>(`${session.workspaceBase}/profiles`, input);
    emit("created", profile);
  } catch (err) {
    error.value = describeError(err, "档案创建失败，请稍后重试");
  } finally {
    saving.value = false;
  }
};
</script>

<style scoped>
.dialog-alert{margin-bottom:15px}
</style>
