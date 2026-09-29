<template>
  <ModalDialog :open="open" title="写入凭据" description="由服务端加密保存；提交后界面不会再显示明文。" @close="close">
    <form :id="formId" class="form-grid" @submit.prevent="submit">
      <label class="field field-span">
        <span>凭据类型</span>
        <select ref="firstField" v-model="form.kind">
          <option v-for="item in secretKinds" :key="item.value" :value="item.value">{{ item.label }}{{ existingKinds.includes(item.value) ? "（已写入）" : "" }}</option>
        </select>
      </label>
      <label class="field field-span">
        <span>{{ secretKindLabel(form.kind) }}</span>
        <input
          v-model="form.value"
          type="password"
          required
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          data-1p-ignore
          data-lpignore="true"
          data-bwignore
          :placeholder="placeholder"
        />
      </label>
    </form>
    <p v-if="replacing" class="form-note"><Icon icon="lucide:refresh-cw" />该账号已有{{ secretKindLabel(form.kind) }}，保存后将替换原值。</p>
    <div v-if="error" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ error }}</span></div>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="!canSubmit">
        <Icon :icon="saving ? 'lucide:loader-circle' : 'lucide:lock'" :class="{ spin: saving }" />{{ saving ? "加密保存中…" : "加密保存" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, useId, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { describeAccountError, isNotFoundError, secretKindLabel, secretKinds } from "@/components/account/accountMeta";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset, AccountSecretKind, AccountSecretMetadata } from "@/types";

const props = withDefaults(defineProps<{
  open: boolean;
  account: AccountAsset | null;
  /** Preselected kind, for example when replacing an existing secret. */
  initialKind?: AccountSecretKind | "";
  /** Kinds already stored; the server keeps one secret per kind and replaces it. */
  existingKinds?: string[];
}>(), { initialKind: "", existingKinds: () => [] });
const emit = defineEmits<{ close: []; saved: [secret: AccountSecretMetadata]; stale: [] }>();

const session = useSessionStore();
const formId = `account-secret-${useId()}`;
const firstField = ref<HTMLSelectElement>();
const saving = ref(false);
const error = ref("");
const form = reactive<{ kind: AccountSecretKind; value: string }>({ kind: "password", value: "" });

const placeholders: Record<AccountSecretKind, string> = {
  password: "输入登录密码",
  cookie: "粘贴 JSON 格式的 Cookie",
  totp_seed: "输入 2FA（TOTP）密钥",
  api_key: "粘贴 API Key",
  oauth_token: "粘贴 OAuth 令牌",
};
const placeholder = computed(() => placeholders[form.kind]);
const replacing = computed(() => props.existingKinds.includes(form.kind));
const canSubmit = computed(() => Boolean(props.account) && !saving.value && form.value.trim().length > 0);

watch(() => props.open, async (open) => {
  // The plaintext only lives in this form: drop it whenever the dialog closes.
  form.value = "";
  if (!open) return;
  error.value = "";
  form.kind = props.initialKind || secretKinds.find((item) => !props.existingKinds.includes(item.value))?.value || "password";
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
    // Not trimmed: surrounding whitespace can be significant. (A password input
    // already drops line breaks, so multi-line material arrives on one line.)
    const secret = await api.post<AccountSecretMetadata>(`${session.workspaceBase}/accounts/${encodeURIComponent(account.id)}/secrets`, {
      kind: form.kind,
      value: form.value,
    });
    form.value = "";
    emit("saved", secret);
  } catch (err) {
    error.value = describeAccountError(err, "凭据保存失败", {
      not_found: "账号不存在或已被删除",
      service_unavailable: "凭据服务暂不可用，请稍后重试",
    });
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
