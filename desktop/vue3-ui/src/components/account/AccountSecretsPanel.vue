<template>
  <article class="panel">
    <header class="panel-header">
      <div><h2>凭据</h2><p>每种类型保留一份，加密保存且只写不读</p></div>
      <button v-if="canManage" class="button button-secondary small" type="button" :disabled="!loaded" @click="openModal()"><Icon icon="lucide:key-round" />写入凭据</button>
    </header>
    <div v-if="error" class="alert alert-danger panel-alert" role="alert">
      <Icon icon="lucide:circle-alert" /><span>{{ error }}</span>
      <button class="button button-secondary small" type="button" :disabled="loading" @click="load">重试</button>
    </div>
    <div v-if="items.length" class="table-wrap">
      <table class="data-table">
        <thead><tr><th>类型</th><th>版本</th><th>首次写入</th><th>最近更新</th><th class="actions-cell">操作</th></tr></thead>
        <tbody>
          <tr v-for="item in items" :key="item.id">
            <td>
              <div class="primary-cell">
                <span class="row-icon"><Icon icon="lucide:key-round" /></span>
                <div><strong>{{ secretKindLabel(item.kind) }}</strong><small class="mono">{{ item.kind }}</small></div>
              </div>
            </td>
            <td>v{{ item.version }}</td>
            <td>{{ formatDateTime(item.createdAt) }}</td>
            <td>{{ formatDateTime(item.updatedAt) }}</td>
            <td class="actions-cell">
              <template v-if="canManage">
                <button class="button button-quiet small" type="button" @click="openModal(item.kind)">替换</button>
                <button class="icon-button" type="button" :aria-label="`删除${secretKindLabel(item.kind)}`" title="删除" @click="remove(item)"><Icon icon="lucide:trash-2" /></button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else-if="loading" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载凭据…</p>
    <p v-else-if="!error" class="panel-empty">尚未写入凭据</p>

    <AccountSecretModal
      :open="modalOpen"
      :account="account"
      :initial-kind="modalKind"
      :existing-kinds="storedKinds"
      @close="modalOpen = false"
      @saved="onSaved"
      @stale="emit('stale')"
    />
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import AccountSecretModal from "@/components/account/AccountSecretModal.vue";
import { describeAccountError, isNotFoundError, secretKindLabel, secretKinds } from "@/components/account/accountMeta";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset, AccountSecretKind, AccountSecretMetadata } from "@/types";
import { formatDateTime } from "@/utils/format";

const props = defineProps<{ account: AccountAsset }>();
/** A 404 here may mean the account itself is gone; the page re-checks it. */
const emit = defineEmits<{ stale: []; loaded: [] }>();

const session = useSessionStore();
const confirm = useConfirm();
const canManage = computed(() => session.can("account.manage"));
const items = ref<AccountSecretMetadata[]>([]);
const loading = ref(false);
// Writes upsert per kind; the modal needs the stored kinds to warn before replacing one.
const loaded = ref(false);
const error = ref("");
const modalOpen = ref(false);
const modalKind = ref<AccountSecretKind | "">("");
const storedKinds = computed(() => items.value.map((item) => item.kind));
const basePath = () => `${session.workspaceBase}/accounts/${encodeURIComponent(props.account.id)}/secrets`;
const kindOrder = (kind: string) => {
  const index = secretKinds.findIndex((item) => item.value === kind);
  return index < 0 ? secretKinds.length : index;
};

let sequence = 0;
const load = async () => {
  const current = ++sequence;
  loading.value = true;
  error.value = "";
  try {
    const list = await api.list<AccountSecretMetadata>(basePath());
    if (current !== sequence) return;
    items.value = [...list].sort((a, b) => kindOrder(a.kind) - kindOrder(b.kind));
    loaded.value = true;
  } catch (err) {
    if (current !== sequence) return;
    error.value = describeAccountError(err, "凭据加载失败");
    if (isNotFoundError(err)) emit("stale");
  } finally {
    if (current === sequence) {
      loading.value = false;
      emit("loaded");
    }
  }
};

const openModal = (kind: string = "") => {
  modalKind.value = secretKinds.some((item) => item.value === kind) ? (kind as AccountSecretKind) : "";
  modalOpen.value = true;
};

const onSaved = async () => {
  modalOpen.value = false;
  await load();
};

const remove = (item: AccountSecretMetadata) => {
  const label = secretKindLabel(item.kind);
  confirm.ask({
    title: `删除${label}`,
    message: `确认删除该账号的${label}？删除后无法恢复，需要时请重新写入。`,
    confirmText: "删除",
    danger: true,
  }, async () => {
    try {
      await api.delete(`${basePath()}/${encodeURIComponent(item.id)}`);
    } catch (err) {
      if (isNotFoundError(err)) emit("stale");
      throw new Error(describeAccountError(err, "凭据删除失败", { not_found: "凭据已不存在，列表已刷新" }));
    } finally {
      await load();
    }
  });
};

watch(() => props.account.id, () => {
  items.value = [];
  loaded.value = false;
  void load();
}, { immediate: true });

defineExpose({ load });
</script>

<style scoped>
.panel-alert {
  margin: 14px 20px;
}
.panel-alert .button {
  margin-left: auto;
}
</style>
