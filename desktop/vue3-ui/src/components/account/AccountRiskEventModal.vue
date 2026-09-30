<template>
  <ModalDialog :open="open" title="记录风险事件" description="记录不会自动调整账号风险等级。" @close="close">
    <form :id="formId" class="form-grid" @submit.prevent="submit">
      <label class="field">
        <span>等级</span>
        <select ref="firstField" v-model="form.level">
          <option v-for="item in riskEventLevels" :key="item.value" :value="item.value">{{ item.label }}</option>
        </select>
      </label>
      <label class="field">
        <span>事件代码</span>
        <input v-model="form.code" required maxlength="64" :list="codeListId" autocomplete="off" spellcheck="false" placeholder="例如：login_challenge" />
      </label>
      <datalist :id="codeListId">
        <option v-for="item in riskEventCodes" :key="item.value" :value="item.value">{{ item.label }}</option>
      </datalist>
      <label class="field field-span">
        <span>说明（可选）</span>
        <textarea v-model="form.description" maxlength="500" rows="3" placeholder="发生了什么、如何发现" />
      </label>
    </form>
    <div v-if="error" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ error }}</span></div>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="!canSubmit">
        <Icon v-if="saving" icon="lucide:loader-circle" class="spin" />{{ saving ? "记录中…" : "记录" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, useId, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { describeAccountError, isNotFoundError, riskEventCodes, riskEventLevels } from "@/components/account/accountMeta";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset, AccountRiskEvent } from "@/types";

const props = defineProps<{ open: boolean; account: AccountAsset | null }>();
const emit = defineEmits<{ close: []; saved: [event: AccountRiskEvent]; stale: [] }>();

const session = useSessionStore();
const formId = `account-risk-event-${useId()}`;
const codeListId = `${formId}-codes`;
const firstField = ref<HTMLSelectElement>();
const saving = ref(false);
const error = ref("");
const form = reactive({ level: "medium", code: "", description: "" });
const canSubmit = computed(() => Boolean(props.account) && !saving.value && Boolean(form.code.trim()));

watch(() => props.open, async (open) => {
  if (!open) return;
  Object.assign(form, { level: "medium", code: "", description: "" });
  error.value = "";
  await nextTick();
  firstField.value?.focus();
}, { immediate: true });

const close = () => {
  if (!saving.value) emit("close");
};

const submit = async () => {
  const account = props.account;
  if (!account || !canSubmit.value) return;
  saving.value = true;
  error.value = "";
  try {
    const event = await api.post<AccountRiskEvent>(`${session.workspaceBase}/accounts/${encodeURIComponent(account.id)}/risk-events`, {
      level: form.level,
      code: form.code.trim(),
      description: form.description.trim(),
    });
    emit("saved", event);
  } catch (err) {
    error.value = describeAccountError(err, "风险事件记录失败", { not_found: "账号不存在或已被删除" });
    if (isNotFoundError(err)) emit("stale");
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
