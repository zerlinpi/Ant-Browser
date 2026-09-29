<template>
  <ModalDialog :open="open" :title="isStatus ? '更改账号状态' : '调整风险等级'" :description="description" @close="close">
    <form :id="formId" @submit.prevent="submit">
      <fieldset ref="choices" class="choice-grid" :disabled="saving">
        <legend class="visually-hidden">{{ isStatus ? "目标状态" : "目标风险等级" }}</legend>
        <label v-for="option in options" :key="option.value" class="choice" :class="{ selected: selected === option.value }">
          <input v-model="selected" type="radio" :name="formId" :value="option.value" />
          <StatusBadge v-if="isStatus" :status="option.value" :label="option.label" />
          <AccountRiskBadge v-else :level="option.value" />
          <small v-if="option.value === current">当前</small>
          <small v-else-if="option.hint">{{ option.hint }}</small>
        </label>
      </fieldset>
    </form>
    <div v-if="error" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ error }}</span></div>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="!canSubmit">
        <Icon v-if="saving" icon="lucide:loader-circle" class="spin" />{{ saving ? "保存中…" : "确认" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import AccountRiskBadge from "@/components/account/AccountRiskBadge.vue";
import {
  accountConflictMessages,
  accountRiskLevels,
  accountStatuses,
  describeAccountError,
  isStaleAccountError,
  riskLabel,
  statusLabel,
  type AccountStateField,
} from "@/components/account/accountMeta";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset } from "@/types";

const props = defineProps<{ open: boolean; account: AccountAsset | null; field: AccountStateField }>();
const emit = defineEmits<{ close: []; saved: [account: AccountAsset]; stale: [] }>();

const session = useSessionStore();
const formId = `account-state-${useId()}`;
const choices = ref<HTMLFieldSetElement>();
const selected = ref("");
const saving = ref(false);
const error = ref("");

const isStatus = computed(() => props.field === "status");
const options = computed(() => (isStatus.value ? accountStatuses : accountRiskLevels));
const current = computed(() => {
  const value = isStatus.value ? props.account?.status : props.account?.riskLevel || "unknown";
  return (value || "").toLowerCase();
});
const description = computed(() => {
  if (!props.account) return "";
  const label = isStatus.value ? statusLabel(current.value) : riskLabel(current.value);
  return `${props.account.name} · 当前${isStatus.value ? "状态" : "等级"}：${label}`;
});
const canSubmit = computed(() => Boolean(props.account) && !saving.value && Boolean(selected.value) && selected.value !== current.value);

watch(() => props.open, async (open) => {
  if (!open) return;
  selected.value = current.value;
  error.value = "";
  await nextTick();
  (choices.value?.querySelector<HTMLInputElement>("input:checked") ?? choices.value?.querySelector<HTMLInputElement>("input"))?.focus();
}, { immediate: true });

const close = () => {
  if (!saving.value) emit("close");
};

const submit = async () => {
  // Read the version at submit time: after a conflict the parent reloads the
  // account, and confirming again applies the choice to the fresh version.
  const account = props.account;
  if (!account || !canSubmit.value) return;
  saving.value = true;
  error.value = "";
  try {
    const path = `${session.workspaceBase}/accounts/${encodeURIComponent(account.id)}`;
    const updated = isStatus.value
      ? await api.post<AccountAsset>(`${path}/status`, { status: selected.value, expectedVersion: account.version })
      : await api.post<AccountAsset>(`${path}/risk-level`, { riskLevel: selected.value, expectedVersion: account.version });
    emit("saved", updated);
  } catch (err) {
    error.value = describeAccountError(err, isStatus.value ? "账号状态更新失败" : "风险等级更新失败", accountConflictMessages);
    if (isStaleAccountError(err)) emit("stale");
  } finally {
    saving.value = false;
  }
};
</script>

<style scoped>
.choice-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: 0;
  padding: 0;
  border: 0;
}
.choice {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 42px;
  padding: 8px 10px;
  border: 1px solid var(--line);
  border-radius: 8px;
  cursor: pointer;
}
.choice:hover {
  background: var(--surface-2);
}
.choice.selected {
  border-color: var(--blue);
  background: #f5f8ff;
}
.choice input {
  margin: 0;
}
.choice small {
  margin-left: auto;
  color: var(--muted);
  font-size: 11px;
}
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
.modal-alert {
  margin-top: 16px;
}
</style>
