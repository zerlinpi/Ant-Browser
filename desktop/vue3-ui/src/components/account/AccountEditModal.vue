<template>
  <ModalDialog :open="open" title="编辑账号信息" :description="description" @close="close">
    <form :id="formId" class="form-grid" @submit.prevent="submit">
      <label class="field field-span"><span>资产名称</span><input ref="firstField" v-model="form.name" required maxlength="120" placeholder="例如：Amazon US 主店" /></label>
      <label class="field"><span>账号标识</span><input v-model="form.identifier" required maxlength="255" placeholder="店铺 URL、卖家 ID 或登录名" /></label>
      <label class="field"><span>用户名（可选）</span><input v-model="form.username" maxlength="160" autocomplete="off" /></label>
      <label class="field"><span>邮箱（可选）</span><input v-model="form.email" type="email" maxlength="254" autocomplete="off" /></label>
      <label class="field"><span>外部 ID（可选）</span><input v-model="form.externalId" maxlength="255" placeholder="平台侧账号或店铺 ID" /></label>
      <label class="field"><span>地区（可选）</span><input v-model="form.region" maxlength="64" placeholder="例如：US" /></label>
      <label class="field field-span"><span>备注（可选）</span><textarea v-model="form.notes" maxlength="2000" rows="3" /></label>
    </form>
    <div v-if="error" class="alert alert-danger modal-alert" role="alert">
      <Icon icon="lucide:circle-alert" /><span>{{ error }}</span>
      <button v-if="conflict" class="button button-secondary small" type="button" :disabled="reloading" @click="reloadLatest">
        <Icon v-if="reloading" icon="lucide:loader-circle" class="spin" />载入最新数据
      </button>
    </div>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="!canSubmit">
        <Icon v-if="saving" icon="lucide:loader-circle" class="spin" />{{ saving ? "保存中…" : "保存" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, useId, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api, ApiError } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { describeAccountError, isStaleAccountError, platformLabel } from "@/components/account/accountMeta";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset } from "@/types";

const props = defineProps<{ open: boolean; account: AccountAsset | null }>();
const emit = defineEmits<{ close: []; saved: [account: AccountAsset]; stale: [] }>();

type EditableFields = { name: string; identifier: string; username: string; email: string; externalId: string; region: string; notes: string };

const session = useSessionStore();
const formId = `account-edit-${useId()}`;
const firstField = ref<HTMLInputElement>();
const saving = ref(false);
const reloading = ref(false);
const error = ref("");
/** Set by a version conflict: saving stays blocked until the latest data is loaded. */
const conflict = ref(false);
// Snapshot taken when the dialog opens. Its version guards the update, so a
// concurrent change is reported instead of being silently overwritten.
const base = ref<AccountAsset | null>(null);
const form = reactive<EditableFields>({ name: "", identifier: "", username: "", email: "", externalId: "", region: "", notes: "" });

const editableFields = (account: AccountAsset | null): EditableFields => ({
  name: account?.name || "",
  identifier: account?.identifier || "",
  username: account?.username || "",
  email: account?.email || "",
  externalId: account?.externalId || "",
  region: account?.region || "",
  notes: account?.notes || "",
});

const trimmedForm = (): EditableFields => ({
  name: form.name.trim(),
  identifier: form.identifier.trim(),
  username: form.username.trim(),
  email: form.email.trim(),
  externalId: form.externalId.trim(),
  region: form.region.trim(),
  notes: form.notes.trim(),
});

const snapshot = (account: AccountAsset | null) => {
  base.value = account ? { ...account } : null;
  Object.assign(form, editableFields(base.value));
  conflict.value = false;
  error.value = "";
};

const description = computed(() => (base.value ? `平台：${platformLabel(base.value.platform)}（创建后不可修改）` : ""));
const dirty = computed(() => {
  const original = editableFields(base.value);
  const next = trimmedForm();
  return (Object.keys(next) as (keyof EditableFields)[]).some((key) => next[key] !== original[key]);
});
const canSubmit = computed(() =>
  Boolean(base.value) && !saving.value && !reloading.value && !conflict.value && dirty.value && Boolean(form.name.trim()) && Boolean(form.identifier.trim()));

watch(() => props.open, async (open) => {
  if (!open) return;
  snapshot(props.account);
  await nextTick();
  firstField.value?.focus();
}, { immediate: true });

const close = () => {
  if (!saving.value) emit("close");
};

/** Replaces the snapshot (and the typed edits) with the server's current data. */
const reloadLatest = async () => {
  const id = base.value?.id;
  if (!id || reloading.value) return;
  reloading.value = true;
  try {
    snapshot(await api.get<AccountAsset>(`${session.workspaceBase}/accounts/${encodeURIComponent(id)}`));
  } catch (err) {
    error.value = describeAccountError(err, "最新数据加载失败", { not_found: "账号不存在或已被删除" });
    if (isStaleAccountError(err)) emit("stale");
  } finally {
    reloading.value = false;
  }
};

const submit = async () => {
  const account = base.value;
  if (!account || !canSubmit.value) return;
  const values = trimmedForm();
  saving.value = true;
  error.value = "";
  try {
    // PATCH clears omitted optional fields, so every field is resent. Status,
    // risk level and metadata are not edited here and go back unchanged.
    const updated = await api.patch<AccountAsset>(`${session.workspaceBase}/accounts/${encodeURIComponent(account.id)}`, {
      ...values,
      status: account.status,
      riskLevel: account.riskLevel,
      metadata: account.metadata,
      expectedVersion: account.version,
    });
    emit("saved", updated);
  } catch (err) {
    conflict.value = err instanceof ApiError && err.code === "version_conflict";
    error.value = describeAccountError(err, "账号信息保存失败", {
      version_conflict: "账号已被其他成员更新。载入最新数据后请重新修改",
      not_found: "账号不存在或已被删除",
      account_identifier_conflict: "保存失败：同平台已有账号使用该标识（不区分大小写），请更换账号标识",
    });
    if (isStaleAccountError(err)) emit("stale");
  } finally {
    saving.value = false;
  }
};
</script>

<style scoped>
.modal-alert {
  margin-top: 16px;
}
</style>
