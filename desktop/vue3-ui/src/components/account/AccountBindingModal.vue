<template>
  <ModalDialog :open="open" title="绑定环境" description="每种类型只保留一个绑定，重新绑定会替换原目标。" @close="close">
    <form :id="formId" class="bind-form" @submit.prevent="submit">
      <fieldset ref="typePicker" class="type-picker" :disabled="saving">
        <legend>绑定类型</legend>
        <label v-for="item in bindingTypes" :key="item.value" class="type-option" :class="{ selected: type === item.value }">
          <input v-model="type" type="radio" :name="formId" :value="item.value" />
          <Icon :icon="item.icon" />
          <span>{{ item.label }}</span>
          <small v-if="current[item.value]">已绑定</small>
        </label>
      </fieldset>
      <label class="field">
        <span>绑定目标</span>
        <select v-model="targetId" required :disabled="saving || optionsLoading || !options.length">
          <option value="" disabled>{{ targetPlaceholder }}</option>
          <option v-for="item in options" :key="item.id" :value="item.id">
            {{ item.name }}{{ item.detail ? ` · ${item.detail}` : "" }}{{ item.id === currentTargetId ? "（当前）" : "" }}
          </option>
        </select>
      </label>
    </form>
    <div v-if="optionsError" class="alert alert-warning modal-alert" role="alert">
      <Icon icon="lucide:triangle-alert" /><span>{{ optionsError }}</span>
      <button v-if="canReadType" class="button button-secondary small" type="button" :disabled="optionsLoading" @click="loadOptions">重试</button>
    </div>
    <p v-else-if="!optionsLoading && !options.length" class="form-note">
      <Icon icon="lucide:info" />当前工作空间没有可绑定的{{ typeMeta.label }}。<RouterLink class="text-link" :to="{ name: typeListRoute[type] }">前往创建</RouterLink>
    </p>
    <p v-else-if="currentTargetId" class="form-note"><Icon icon="lucide:refresh-cw" />已绑定「{{ currentTargetName }}」，保存后将替换为所选目标。</p>
    <div v-if="error" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ error }}</span></div>
    <template #footer>
      <button class="button button-secondary" type="button" :disabled="saving" @click="close">取消</button>
      <button class="button button-primary" type="submit" :form="formId" :disabled="!canSubmit">
        <Icon v-if="saving" icon="lucide:loader-circle" class="spin" />{{ saving ? "绑定中…" : "绑定" }}
      </button>
    </template>
  </ModalDialog>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from "vue";
import { RouterLink } from "vue-router";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import ModalDialog from "@/components/ModalDialog.vue";
import { bindingTypes, describeAccountError, isNotFoundError, listBindingTargets, type BindingTarget } from "@/components/account/accountMeta";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset, AccountBinding, AccountBindingType } from "@/types";
import { shortId } from "@/utils/format";

const props = withDefaults(defineProps<{
  open: boolean;
  account: AccountAsset | null;
  initialType?: AccountBindingType;
  /** Currently bound target id per type. */
  current?: Partial<Record<AccountBindingType, string>>;
}>(), { initialType: "browser_instance", current: () => ({}) });
const emit = defineEmits<{ close: []; saved: [binding: AccountBinding]; stale: [] }>();

const typeListRoute: Record<AccountBindingType, string> = { browser_instance: "browsers", profile: "profiles", proxy: "proxies" };

const session = useSessionStore();
const formId = `account-binding-${useId()}`;
const typePicker = ref<HTMLFieldSetElement>();
const type = ref<AccountBindingType>("browser_instance");
const targetId = ref("");
const options = ref<BindingTarget[]>([]);
const optionsLoading = ref(false);
const optionsError = ref("");
const saving = ref(false);
const error = ref("");

const typeMeta = computed(() => bindingTypes.find((item) => item.value === type.value) ?? bindingTypes[0]);
const canReadType = computed(() => session.can(typeMeta.value.readPermission));
const currentTargetId = computed(() => props.current[type.value] || "");
const currentTargetName = computed(() => options.value.find((item) => item.id === currentTargetId.value)?.name || shortId(currentTargetId.value));
const targetPlaceholder = computed(() => {
  if (optionsLoading.value) return "正在加载…";
  return options.value.length ? `选择${typeMeta.value.label}` : `暂无可用${typeMeta.value.label}`;
});
const canSubmit = computed(() => Boolean(props.account) && !saving.value && Boolean(targetId.value) && targetId.value !== currentTargetId.value);

let sequence = 0;
const loadOptions = async () => {
  const request = ++sequence;
  const selectedType = type.value;
  const label = typeMeta.value.label;
  options.value = [];
  targetId.value = "";
  optionsError.value = "";
  if (!canReadType.value) {
    optionsLoading.value = false;
    optionsError.value = `当前角色无权读取${label}列表`;
    return;
  }
  optionsLoading.value = true;
  try {
    const list = await listBindingTargets(session.workspaceBase, selectedType);
    if (request === sequence) options.value = list;
  } catch (err) {
    if (request === sequence) optionsError.value = describeAccountError(err, `${label}列表加载失败`);
  } finally {
    if (request === sequence) optionsLoading.value = false;
  }
};

watch(() => props.open, async (open) => {
  if (!open) return;
  error.value = "";
  type.value = props.initialType;
  await nextTick();
  typePicker.value?.querySelector<HTMLInputElement>("input:checked")?.focus();
}, { immediate: true });
// One load per open and per type switch (the open watcher above runs first).
watch([() => props.open, type], ([open]) => {
  if (open) void loadOptions();
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
    const binding = await api.post<AccountBinding>(`${session.workspaceBase}/accounts/${encodeURIComponent(account.id)}/bindings`, {
      bindingType: type.value,
      targetId: targetId.value,
    });
    emit("saved", binding);
  } catch (err) {
    error.value = describeAccountError(err, "绑定失败", {
      not_found: "账号或所选目标不存在，可能已被删除",
      service_unavailable: "账号绑定服务暂不可用",
    });
    // A 404 is either the account or the target: re-check the account, refresh the targets.
    if (isNotFoundError(err)) {
      emit("stale");
      void loadOptions();
    }
  } finally {
    saving.value = false;
  }
};
</script>

<style scoped>
.bind-form {
  display: grid;
  gap: 15px;
}
.type-picker {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: 0;
  padding: 0;
  border: 0;
}
.type-picker legend {
  margin-bottom: 7px;
  padding: 0;
  font-size: 12px;
  font-weight: 650;
}
.type-option {
  display: flex;
  align-items: center;
  gap: 7px;
  min-height: 42px;
  padding: 8px 10px;
  border: 1px solid var(--line);
  border-radius: 8px;
  cursor: pointer;
}
.type-option:hover {
  background: var(--surface-2);
}
.type-option.selected {
  border-color: var(--blue);
  background: #f5f8ff;
}
.type-option input {
  margin: 0;
}
.type-option small {
  margin-left: auto;
  color: var(--muted);
  font-size: 11px;
}
.modal-alert {
  margin-top: 16px;
}
.modal-alert .button {
  margin-left: auto;
}
</style>
