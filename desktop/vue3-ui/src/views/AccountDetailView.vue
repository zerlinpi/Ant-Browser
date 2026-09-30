<template>
  <div class="page-stack">
    <div>
      <RouterLink class="back-link" :to="{ name: 'accounts' }"><Icon icon="lucide:arrow-left" />返回账号中心</RouterLink>
      <PageHeader v-if="account" :eyebrow="platformLabel(account.platform)" :title="account.name" :description="identityLine">
        <StatusBadge :status="account.status" :label="statusLabel(account.status)" />
        <AccountRiskBadge :level="account.riskLevel" />
        <button class="button button-secondary" type="button" :disabled="loading" @click="refresh">
          <Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新
        </button>
        <span v-if="busy" class="muted-text busy-note"><Icon icon="lucide:loader-circle" class="spin" />处理中…</span>
        <ActionMenu v-if="canManage" :items="menuItems" label="账号操作" :disabled="busy" @select="onMenu" />
      </PageHeader>
      <PageHeader v-else title="账号详情" />
    </div>

    <article v-if="!canRead" class="panel">
      <EmptyState icon="lucide:lock" title="无权查看账号" description="当前角色没有账号读取权限，请联系工作空间管理员。" />
    </article>

    <article v-else-if="notFound" class="panel">
      <EmptyState icon="lucide:search-x" title="账号不存在或已删除" description="该账号可能已被其他成员删除，或不属于当前工作空间。">
        <RouterLink class="button button-primary" :to="{ name: 'accounts' }">返回账号中心</RouterLink>
      </EmptyState>
    </article>

    <template v-else>
      <div v-if="loadError" class="alert alert-danger" role="alert">
        <Icon icon="lucide:circle-alert" /><span>{{ loadError }}</span>
        <button class="button button-secondary small" type="button" :disabled="loading" @click="refresh">重试</button>
      </div>
      <div v-if="actionError" class="alert alert-danger" role="alert">
        <Icon icon="lucide:circle-alert" /><span>{{ actionError }}</span>
        <button class="icon-button" type="button" aria-label="关闭提示" @click="actionError = ''"><Icon icon="lucide:x" /></button>
      </div>
      <p v-if="!account && loading" class="panel panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载账号…</p>

      <template v-if="account">
        <article class="panel">
          <header class="panel-header">
            <div><h2>基本信息</h2><p>v{{ account.version }} · 更新于 {{ formatDateTime(account.updatedAt) }}</p></div>
            <button v-if="canManage" class="button button-secondary small" type="button" :disabled="busy" @click="editOpen = true"><Icon icon="lucide:pencil" />编辑</button>
          </header>
          <dl class="detail-list">
            <div><dt>资产名称</dt><dd>{{ account.name }}</dd></div>
            <div><dt>平台</dt><dd>{{ platformLabel(account.platform) }}</dd></div>
            <div><dt>账号标识</dt><dd>{{ account.identifier || "—" }}</dd></div>
            <div><dt>用户名</dt><dd>{{ account.username || "—" }}</dd></div>
            <div><dt>邮箱</dt><dd>{{ account.email || "—" }}</dd></div>
            <div><dt>外部 ID</dt><dd>{{ account.externalId || "—" }}</dd></div>
            <div><dt>地区</dt><dd>{{ account.region || "—" }}</dd></div>
            <div><dt>创建时间</dt><dd>{{ formatFullDateTime(account.createdAt) }}</dd></div>
            <div><dt>账号 ID</dt><dd class="mono">{{ account.id }}</dd></div>
            <div v-if="metadataEntries.length" class="detail-wide">
              <dt>元数据</dt>
              <dd class="tag-list metadata-list"><span v-for="[key, value] in metadataEntries" :key="key" class="tag">{{ key }}：{{ value }}</span></dd>
            </div>
            <div class="detail-wide"><dt>备注</dt><dd class="notes">{{ account.notes || "—" }}</dd></div>
          </dl>
        </article>

        <AccountSecretsPanel ref="secretsPanel" :account="account" @stale="loadAccount" @loaded="onPanelLoaded('secrets')" />
        <AccountBindingsPanel id="bindings" ref="bindingsPanel" :account="account" @stale="loadAccount" @loaded="onPanelLoaded('bindings')" />
        <AccountRiskEventsPanel ref="riskEventsPanel" :account="account" @stale="loadAccount" />
      </template>
    </template>

    <AccountEditModal :open="editOpen" :account="account" @close="editOpen = false" @saved="onAccountSaved" @stale="loadAccount" />
    <AccountStateModal :open="stateField !== null" :account="account" :field="stateField ?? 'status'" @close="stateField = null" @saved="onAccountSaved" @stale="loadAccount" />
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import { Icon } from "@iconify/vue";
import { api, ApiError } from "@/api/client";
import ActionMenu from "@/components/ActionMenu.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import AccountBindingsPanel from "@/components/account/AccountBindingsPanel.vue";
import AccountEditModal from "@/components/account/AccountEditModal.vue";
import AccountRiskBadge from "@/components/account/AccountRiskBadge.vue";
import AccountRiskEventsPanel from "@/components/account/AccountRiskEventsPanel.vue";
import AccountSecretsPanel from "@/components/account/AccountSecretsPanel.vue";
import AccountStateModal from "@/components/account/AccountStateModal.vue";
import {
  accountConflictMessages,
  accountMenuItems,
  describeAccountError,
  isStaleAccountError,
  platformLabel,
  statusLabel,
  type AccountStateField,
} from "@/components/account/accountMeta";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset } from "@/types";
import { formatDateTime, formatFullDateTime } from "@/utils/format";

const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const confirm = useConfirm();

const canRead = computed(() => session.can("account.read"));
const canManage = computed(() => session.can("account.manage"));
const accountId = computed(() => (typeof route.params.accountId === "string" ? route.params.accountId : ""));
const account = ref<AccountAsset | null>(null);
const loading = ref(false);
const notFound = ref(false);
const loadError = ref("");
const actionError = ref("");
const busy = ref(false);
const editOpen = ref(false);
const stateField = ref<AccountStateField | null>(null);
const secretsPanel = ref<InstanceType<typeof AccountSecretsPanel> | null>(null);
const bindingsPanel = ref<InstanceType<typeof AccountBindingsPanel> | null>(null);
const riskEventsPanel = ref<InstanceType<typeof AccountRiskEventsPanel> | null>(null);

const accountPath = (id: string) => `${session.workspaceBase}/accounts/${encodeURIComponent(id)}`;
const identityLine = computed(() => {
  const item = account.value;
  if (!item) return "";
  return [item.identifier, item.username && item.username !== item.identifier ? item.username : "", item.email].filter(Boolean).join(" · ");
});
const menuItems = computed(() => (account.value ? accountMenuItems(account.value) : []));
const metadataEntries = computed(() => Object.entries(account.value?.metadata ?? {}));

const closeDialogs = () => {
  editOpen.value = false;
  stateField.value = null;
};

let sequence = 0;
const loadAccount = async () => {
  const id = accountId.value;
  if (!canRead.value || !session.workspaceBase || !id) return;
  const request = ++sequence;
  loading.value = true;
  loadError.value = "";
  try {
    const item = await api.get<AccountAsset>(accountPath(id));
    if (request !== sequence) return;
    account.value = item;
    notFound.value = false;
  } catch (err) {
    if (request !== sequence) return;
    // A malformed id in the URL fails the server's uuid cast as a 422.
    if (err instanceof ApiError && (err.status === 404 || (err.status === 422 && err.code === "validation_failed"))) {
      account.value = null;
      notFound.value = true;
      closeDialogs();
    } else {
      loadError.value = describeAccountError(err, "账号加载失败");
    }
  } finally {
    if (request === sequence) loading.value = false;
  }
};

const refresh = async () => {
  actionError.value = "";
  await Promise.all([
    loadAccount(),
    secretsPanel.value?.load(),
    bindingsPanel.value?.load(),
    riskEventsPanel.value?.load(),
  ]);
};

const onAccountSaved = async (updated: AccountAsset) => {
  closeDialogs();
  if (updated?.id === account.value?.id) account.value = updated;
  await loadAccount();
};

const setStatus = async (next: string) => {
  const item = account.value;
  if (!item || busy.value) return;
  busy.value = true;
  actionError.value = "";
  try {
    // Apply the response at once so follow-up actions carry the new version.
    const updated = await api.post<AccountAsset>(`${accountPath(item.id)}/status`, { status: next, expectedVersion: item.version });
    if (updated?.id === account.value?.id) account.value = updated;
  } catch (err) {
    actionError.value = describeAccountError(err, "账号状态更新失败", accountConflictMessages);
  } finally {
    busy.value = false;
  }
  await loadAccount();
};

const remove = () => {
  const item = account.value;
  if (!item) return;
  confirm.ask({
    title: "删除账号",
    message: `确认删除账号「${item.name}」？删除后将从账号中心移除，无法在客户端恢复。`,
    confirmText: "删除",
    danger: true,
  }, async () => {
    // Read the version when confirming (a conflict reloads the account), but
    // only for the account the dialog names.
    const latest = account.value?.id === item.id ? account.value : item;
    try {
      await api.delete(accountPath(latest.id), { expectedVersion: latest.version });
    } catch (err) {
      if (isStaleAccountError(err)) await loadAccount();
      throw new Error(describeAccountError(err, "账号删除失败", accountConflictMessages));
    }
    await router.replace({ name: "accounts" });
  });
};

const onMenu = (key: string) => {
  if (key === "lock") void setStatus("locked");
  else if (key === "unlock") void setStatus("active");
  else if (key === "status") stateField.value = "status";
  else if (key === "risk") stateField.value = "riskLevel";
  else if (key === "delete") remove();
};

// #bindings deep link (from the list): scroll once the panels above it have
// rendered their data, otherwise the section moves down after the jump.
let scrollToBindings = false;
const panelsLoaded = { secrets: false, bindings: false };
const onPanelLoaded = async (panel: keyof typeof panelsLoaded) => {
  panelsLoaded[panel] = true;
  if (!scrollToBindings || !panelsLoaded.secrets || !panelsLoaded.bindings) return;
  scrollToBindings = false;
  await nextTick();
  document.getElementById("bindings")?.scrollIntoView({ block: "start" });
};

// The component is reused when navigating between accounts.
watch(accountId, () => {
  account.value = null;
  notFound.value = false;
  loadError.value = "";
  actionError.value = "";
  closeDialogs();
  confirm.cancel();
  panelsLoaded.secrets = false;
  panelsLoaded.bindings = false;
  scrollToBindings = route.hash === "#bindings";
  void loadAccount();
}, { immediate: true });
</script>

<style scoped>
.busy-note {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
}
.detail-wide {
  grid-column: 1 / -1;
}
.notes {
  font-weight: 400;
  white-space: pre-wrap;
}
.metadata-list {
  flex-wrap: wrap;
}
</style>
