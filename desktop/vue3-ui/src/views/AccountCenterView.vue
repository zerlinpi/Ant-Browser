<template>
  <div class="page-stack">
    <PageHeader title="账号中心" description="集中管理跨境平台账号、生命周期与风险状态">
      <button v-if="canManage" class="button button-primary" type="button" @click="openCreate"><Icon icon="lucide:plus" />添加账号</button>
    </PageHeader>

    <article v-if="!canRead" class="panel">
      <EmptyState icon="lucide:lock" title="无权查看账号" description="当前角色没有账号读取权限，请联系工作空间管理员。" />
    </article>

    <template v-else>
      <div v-if="loadError" class="alert alert-danger" role="alert">
        <Icon icon="lucide:circle-alert" /><span>{{ loadError }}</span>
        <button class="button button-secondary small" type="button" :disabled="loading" @click="load">重试</button>
      </div>
      <div v-if="actionError" class="alert alert-danger" role="alert">
        <Icon icon="lucide:circle-alert" /><span>{{ actionError }}</span>
        <button class="icon-button" type="button" aria-label="关闭提示" @click="actionError = ''"><Icon icon="lucide:x" /></button>
      </div>

      <section class="stat-grid compact">
        <article class="stat-card"><div><p>账号总数</p><strong>{{ accounts.length }}</strong></div><span class="stat-icon blue"><Icon icon="lucide:contact-round" /></span></article>
        <article class="stat-card"><div><p>正常</p><strong>{{ statusCount("active") }}</strong></div><span class="stat-icon green"><Icon icon="lucide:shield-check" /></span></article>
        <article class="stat-card"><div><p>需要处理</p><strong>{{ attentionCount }}</strong></div><span class="stat-icon orange"><Icon icon="lucide:triangle-alert" /></span></article>
      </section>

      <article class="panel">
        <div class="toolbar">
          <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索名称、标识、用户名或邮箱…" aria-label="搜索账号" /></label>
          <select v-model="platform" aria-label="按平台筛选">
            <option value="">全部平台</option>
            <option v-for="item in accountPlatforms" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
          <select v-model="status" aria-label="按状态筛选">
            <option value="">全部状态</option>
            <option v-for="item in accountStatuses" :key="item.value" :value="item.value">{{ item.label }}</option>
          </select>
          <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新</button>
        </div>
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th>账号</th><th>平台</th><th>状态</th><th>风险</th><th>环境绑定</th><th>更新时间</th><th class="actions-cell">操作</th></tr></thead>
            <tbody>
              <tr v-for="item in filtered" :key="item.id">
                <td>
                  <div class="primary-cell">
                    <span class="platform-avatar">{{ platformInitial(item.platform) }}</span>
                    <div>
                      <RouterLink class="text-link account-link" :to="detailRoute(item)">{{ item.name }}</RouterLink>
                      <small>{{ item.username || item.identifier }} · {{ item.email || "未设置邮箱" }}</small>
                    </div>
                  </div>
                </td>
                <td>{{ platformLabel(item.platform) }}</td>
                <td><StatusBadge :status="item.status" :label="statusLabel(item.status)" /></td>
                <td><AccountRiskBadge :level="item.riskLevel" /></td>
                <td><RouterLink class="text-link" :to="detailRoute(item, '#bindings')">{{ canManage ? "管理绑定" : "查看绑定" }}</RouterLink></td>
                <td>{{ formatDateTime(item.updatedAt) }}</td>
                <td class="actions-cell">
                  <template v-if="canManage">
                    <button class="icon-button" type="button" :aria-label="`编辑 ${item.name}`" title="编辑" :disabled="busyIds.has(item.id)" @click="editId = item.id">
                      <Icon :icon="busyIds.has(item.id) ? 'lucide:loader-circle' : 'lucide:pencil'" :class="{ spin: busyIds.has(item.id) }" />
                    </button>
                    <ActionMenu :items="accountMenuItems(item)" :label="`${item.name} 的更多操作`" :disabled="busyIds.has(item.id)" @select="onMenu(item, $event)" />
                  </template>
                </td>
              </tr>
            </tbody>
          </table>
          <p v-if="loading && !accounts.length" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载账号…</p>
          <EmptyState v-else-if="!accounts.length && !loadError" icon="lucide:contact-round" title="还没有账号资产" description="添加账号后可绑定独立浏览器环境和固定代理。">
            <button v-if="canManage" class="button button-primary" type="button" @click="openCreate">添加账号</button>
          </EmptyState>
          <EmptyState v-else-if="accounts.length && !filtered.length" icon="lucide:search-x" title="没有匹配的账号资产" description="调整搜索词或筛选条件后重试。">
            <button class="button button-secondary" type="button" @click="resetFilters">清除筛选</button>
          </EmptyState>
        </div>
      </article>
    </template>

    <ModalDialog :open="createOpen" title="添加账号资产" description="基础信息用于团队识别；密码、2FA 和 Cookie 可在创建后于账号详情页加密写入。" @close="closeCreate">
      <form :id="createFormId" class="form-grid" @submit.prevent="createAccount">
        <label class="field field-span"><span>资产名称</span><input ref="createFirstField" v-model.trim="form.name" required maxlength="120" placeholder="例如：Amazon US 主店" /></label>
        <label class="field"><span>平台</span><select v-model="form.platform"><option v-for="item in accountPlatforms" :key="item.value" :value="item.value">{{ item.label }}</option></select></label>
        <label class="field"><span>用户名</span><input v-model.trim="form.username" required maxlength="160" autocomplete="off" /></label>
        <label class="field"><span>账号标识（可选）</span><input v-model.trim="form.identifier" maxlength="255" autocomplete="off" placeholder="店铺 URL 或卖家 ID，默认同用户名" /></label>
        <label class="field"><span>邮箱（可选）</span><input v-model.trim="form.email" type="email" maxlength="254" autocomplete="off" /></label>
      </form>
      <div v-if="createError" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ createError }}</span></div>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="saving" @click="closeCreate">取消</button>
        <button class="button button-primary" type="submit" :form="createFormId" :disabled="saving || !form.platform || !form.name || !form.username">
          <Icon v-if="saving" icon="lucide:loader-circle" class="spin" />{{ saving ? "添加中…" : "添加账号" }}
        </button>
      </template>
    </ModalDialog>

    <AccountEditModal :open="Boolean(editTarget)" :account="editTarget" @close="editId = ''" @saved="onAccountSaved" @stale="load" />
    <AccountStateModal :open="Boolean(stateTarget)" :account="stateTarget" :field="stateField" @close="stateId = ''" @saved="onAccountSaved" @stale="load" />
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref } from "vue";
import { RouterLink, type RouteLocationRaw } from "vue-router";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import ActionMenu from "@/components/ActionMenu.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import AccountEditModal from "@/components/account/AccountEditModal.vue";
import AccountRiskBadge from "@/components/account/AccountRiskBadge.vue";
import AccountStateModal from "@/components/account/AccountStateModal.vue";
import {
  accountConflictMessages,
  accountMenuItems,
  accountPlatforms,
  accountStatuses,
  describeAccountError,
  isStaleAccountError,
  needsAttention,
  platformLabel,
  statusLabel,
  type AccountStateField,
} from "@/components/account/accountMeta";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset } from "@/types";
import { formatDateTime } from "@/utils/format";

const session = useSessionStore();
const confirm = useConfirm();
const canRead = computed(() => session.can("account.read"));
const canManage = computed(() => session.can("account.manage"));

const accounts = ref<AccountAsset[]>([]);
const query = ref("");
const platform = ref("");
const status = ref("");
const loading = ref(false);
const loadError = ref("");
const actionError = ref("");
/** Rows with a request in flight; their actions stay disabled until it settles. */
const busyIds = reactive(new Set<string>());

// Dialog targets are tracked by id so a reload hands them the fresh row.
const editId = ref("");
const stateId = ref("");
const stateField = ref<AccountStateField>("status");
const findAccount = (id: string) => (id ? accounts.value.find((item) => item.id === id) ?? null : null);
const editTarget = computed(() => findAccount(editId.value));
const stateTarget = computed(() => findAccount(stateId.value));

const createFormId = "account-create-form";
const createOpen = ref(false);
const createFirstField = ref<HTMLInputElement>();
const saving = ref(false);
const createError = ref("");
const form = reactive({ name: "", platform: "amazon", username: "", identifier: "", email: "" });

const statusCount = (value: string) => accounts.value.filter((item) => item.status?.toLowerCase() === value).length;
const attentionCount = computed(() => accounts.value.filter(needsAttention).length);
const filtered = computed(() => {
  const keyword = query.value.toLowerCase();
  return accounts.value.filter((item) =>
    (!keyword || `${item.name} ${item.identifier} ${item.username || ""} ${item.email || ""} ${item.platform}`.toLowerCase().includes(keyword))
    && (!platform.value || item.platform?.toLowerCase() === platform.value)
    && (!status.value || item.status?.toLowerCase() === status.value));
});
const platformInitial = (value?: string) => platformLabel(value).slice(0, 1).toUpperCase();
const detailRoute = (item: AccountAsset, hash = ""): RouteLocationRaw => ({ name: "account-detail", params: { accountId: item.id }, hash });
const resetFilters = () => {
  query.value = "";
  platform.value = "";
  status.value = "";
};

let sequence = 0;
const load = async () => {
  if (!session.workspaceBase || !canRead.value) return;
  const request = ++sequence;
  loading.value = true;
  loadError.value = "";
  try {
    const list = await api.list<AccountAsset>(`${session.workspaceBase}/accounts`);
    if (request !== sequence) return;
    accounts.value = list;
    // An open dialog whose account was deleted elsewhere closes with a notice.
    if ((editId.value && !findAccount(editId.value)) || (stateId.value && !findAccount(stateId.value))) {
      editId.value = "";
      stateId.value = "";
      actionError.value = "账号不存在或已被删除，列表已刷新";
    }
  } catch (error) {
    if (request === sequence) loadError.value = describeAccountError(error, "账号资产加载失败");
  } finally {
    if (request === sequence) loading.value = false;
  }
};

const openCreate = async () => {
  createError.value = "";
  createOpen.value = true;
  await nextTick();
  createFirstField.value?.focus();
};
const closeCreate = () => {
  if (!saving.value) createOpen.value = false;
};
const createAccount = async () => {
  if (saving.value || !form.platform || !form.name || !form.username) return;
  saving.value = true;
  createError.value = "";
  try {
    await api.post(`${session.workspaceBase}/accounts`, {
      name: form.name,
      identifier: form.identifier || form.username,
      platform: form.platform,
      username: form.username,
      email: form.email || undefined,
    });
    createOpen.value = false;
    Object.assign(form, { name: "", username: "", identifier: "", email: "" });
    await load();
  } catch (error) {
    createError.value = describeAccountError(error, "账号创建失败");
  } finally {
    saving.value = false;
  }
};

/** Applies a mutation response right away so follow-up actions carry the new version. */
const applyAccount = (updated?: AccountAsset | null) => {
  const index = updated?.id ? accounts.value.findIndex((item) => item.id === updated.id) : -1;
  if (updated && index >= 0) accounts.value.splice(index, 1, updated);
};

const onAccountSaved = async (updated: AccountAsset) => {
  applyAccount(updated);
  editId.value = "";
  stateId.value = "";
  await load();
};

const setStatus = async (item: AccountAsset, next: string) => {
  if (busyIds.has(item.id)) return;
  busyIds.add(item.id);
  actionError.value = "";
  try {
    applyAccount(await api.post<AccountAsset>(`${session.workspaceBase}/accounts/${encodeURIComponent(item.id)}/status`, { status: next, expectedVersion: item.version }));
  } catch (error) {
    actionError.value = describeAccountError(error, "账号状态更新失败", accountConflictMessages);
  } finally {
    busyIds.delete(item.id);
  }
  // Refresh either way: a conflict also needs the current version.
  await load();
};

const remove = (item: AccountAsset) => {
  confirm.ask({
    title: "删除账号",
    message: `确认删除账号「${item.name}」？删除后将从账号中心移除，无法在客户端恢复。`,
    confirmText: "删除",
    danger: true,
  }, async () => {
    // Read the version when confirming: a conflict reloads the list first.
    const latest = findAccount(item.id) ?? item;
    busyIds.add(item.id);
    try {
      await api.delete(`${session.workspaceBase}/accounts/${encodeURIComponent(latest.id)}`, { expectedVersion: latest.version });
    } catch (error) {
      if (isStaleAccountError(error)) await load();
      throw new Error(describeAccountError(error, "账号删除失败", accountConflictMessages));
    } finally {
      busyIds.delete(item.id);
    }
    await load();
  });
};

const onMenu = (item: AccountAsset, key: string) => {
  if (key === "lock") void setStatus(item, "locked");
  else if (key === "unlock") void setStatus(item, "active");
  else if (key === "status" || key === "risk") {
    stateField.value = key === "status" ? "status" : "riskLevel";
    stateId.value = item.id;
  } else if (key === "delete") remove(item);
};

onMounted(load);
</script>

<style scoped>
.account-link {
  justify-self: start;
}
.actions-cell :deep(.icon-button:disabled) {
  opacity: 0.45;
  cursor: not-allowed;
}
.modal-alert {
  margin-top: 16px;
}
</style>
