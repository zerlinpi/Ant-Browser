<template>
  <article class="panel">
    <header class="panel-header">
      <div><h2>环境绑定</h2><p>浏览器实例、云端档案、代理各绑定一个</p></div>
      <button v-if="canManage" class="button button-secondary small" type="button" :disabled="!loaded" @click="openModal()"><Icon icon="lucide:link" />绑定环境</button>
    </header>
    <div v-if="error" class="alert alert-danger panel-alert" role="alert">
      <Icon icon="lucide:circle-alert" /><span>{{ error }}</span>
      <button class="button button-secondary small" type="button" :disabled="loading" @click="load">重试</button>
    </div>
    <div class="table-wrap">
      <table class="data-table">
        <thead><tr><th>类型</th><th>绑定目标</th><th>绑定时间</th><th class="actions-cell">操作</th></tr></thead>
        <tbody>
          <tr v-for="row in rows" :key="row.type.value">
            <td>
              <div class="primary-cell"><span class="row-icon"><Icon :icon="row.type.icon" /></span><strong>{{ row.type.label }}</strong></div>
            </td>
            <td>
              <div v-if="row.binding" class="target-cell">
                <template v-if="row.target">
                  <RouterLink class="text-link" :to="bindingTargetRoute(row.type.value, row.binding.targetId, row.target)">{{ row.target.name }}</RouterLink>
                  <small class="muted-text">{{ row.target.detail }}</small>
                </template>
                <template v-else-if="row.target === null">
                  <span class="text-danger">目标已删除</span>
                  <small class="muted-text mono">{{ row.binding.targetId }}</small>
                </template>
                <span v-else class="mono" :title="row.binding.targetId">{{ shortId(row.binding.targetId) }}</span>
              </div>
              <span v-else class="muted-text">{{ loading && !loaded ? "加载中…" : "未绑定" }}</span>
            </td>
            <td>{{ row.binding ? formatDateTime(row.binding.updatedAt) : "—" }}</td>
            <td class="actions-cell">
              <template v-if="canManage">
                <button class="button button-quiet small" type="button" :disabled="!loaded" @click="openModal(row.type.value)">{{ row.binding ? "更换" : "绑定" }}</button>
                <button v-if="row.binding" class="button button-quiet small" type="button" @click="unbind(row)">解绑</button>
              </template>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <AccountBindingModal
      :open="modalOpen"
      :account="account"
      :initial-type="modalType"
      :current="currentTargets"
      @close="modalOpen = false"
      @saved="onSaved"
      @stale="emit('stale')"
    />
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { RouterLink } from "vue-router";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import AccountBindingModal from "@/components/account/AccountBindingModal.vue";
import {
  bindingTargetRoute,
  bindingTypes,
  describeAccountError,
  getBindingTarget,
  isBindingType,
  isNotFoundError,
  type BindingTarget,
  type BindingTypeOption,
} from "@/components/account/accountMeta";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset, AccountBinding, AccountBindingType } from "@/types";
import { formatDateTime, shortId } from "@/utils/format";

interface BindingRow {
  type: BindingTypeOption;
  binding?: AccountBinding;
  /** undefined: not resolved (loading or not readable); null: target deleted. */
  target?: BindingTarget | null;
}

const props = defineProps<{ account: AccountAsset }>();
/** A 404 here may mean the account itself is gone; the page re-checks it. */
const emit = defineEmits<{ stale: []; loaded: [] }>();

const session = useSessionStore();
const confirm = useConfirm();
const canManage = computed(() => session.can("account.manage"));
const bindings = ref<AccountBinding[]>([]);
const targets = ref<Record<string, BindingTarget | null>>({});
const loading = ref(false);
const loaded = ref(false);
const error = ref("");
const modalOpen = ref(false);
const modalType = ref<AccountBindingType>("browser_instance");
const basePath = () => `${session.workspaceBase}/accounts/${encodeURIComponent(props.account.id)}/bindings`;
const targetKey = (type: string, id: string) => `${type}:${id}`;
const time = (value?: string) => Date.parse(value || "") || 0;

// The server keeps one active binding per type (upsert); if an older store
// returns several, the most recently updated one is current.
const currentByType = computed(() => {
  const result: Partial<Record<AccountBindingType, AccountBinding>> = {};
  for (const item of bindings.value) {
    if (!isBindingType(item.bindingType) || (item.status && item.status !== "active")) continue;
    const existing = result[item.bindingType];
    if (!existing || time(item.updatedAt) > time(existing.updatedAt)) result[item.bindingType] = item;
  }
  return result;
});

const rows = computed<BindingRow[]>(() => bindingTypes.map((type) => {
  const binding = currentByType.value[type.value];
  return { type, binding, target: binding ? targets.value[targetKey(type.value, binding.targetId)] : undefined };
}));

const currentTargets = computed(() => {
  const result: Partial<Record<AccountBindingType, string>> = {};
  for (const [type, binding] of Object.entries(currentByType.value) as [AccountBindingType, AccountBinding][]) result[type] = binding.targetId;
  return result;
});

let sequence = 0;
const resolveTargets = async (request: number) => {
  const next: Record<string, BindingTarget | null> = {};
  await Promise.all(bindingTypes.map(async (type) => {
    const binding = currentByType.value[type.value];
    if (!binding || !session.can(type.readPermission)) return;
    try {
      next[targetKey(type.value, binding.targetId)] = await getBindingTarget(session.workspaceBase, type.value, binding.targetId);
    } catch (err) {
      // Names are best effort; only a 404 is conclusive.
      if (isNotFoundError(err)) next[targetKey(type.value, binding.targetId)] = null;
    }
  }));
  if (request === sequence) targets.value = next;
};

const load = async () => {
  const request = ++sequence;
  loading.value = true;
  error.value = "";
  try {
    const list = await api.list<AccountBinding>(basePath());
    if (request !== sequence) return;
    bindings.value = list;
    loaded.value = true;
    void resolveTargets(request);
  } catch (err) {
    if (request !== sequence) return;
    error.value = describeAccountError(err, "环境绑定加载失败");
    if (isNotFoundError(err)) emit("stale");
  } finally {
    if (request === sequence) {
      loading.value = false;
      emit("loaded");
    }
  }
};

const openModal = (type?: AccountBindingType) => {
  modalType.value = type ?? bindingTypes.find((item) => !currentByType.value[item.value])?.value ?? "browser_instance";
  modalOpen.value = true;
};

const onSaved = async () => {
  modalOpen.value = false;
  await load();
};

const unbind = (row: BindingRow) => {
  const binding = row.binding;
  if (!binding) return;
  const name = row.target?.name || shortId(binding.targetId);
  confirm.ask({
    title: `解绑${row.type.label}`,
    message: `确认解除该账号与「${name}」的绑定？`,
    confirmText: "解绑",
    danger: true,
  }, async () => {
    try {
      await api.delete(`${basePath()}/${encodeURIComponent(binding.id)}`);
    } catch (err) {
      if (isNotFoundError(err)) emit("stale");
      throw new Error(describeAccountError(err, "解绑失败", { not_found: "绑定已解除或不存在，列表已刷新" }));
    } finally {
      await load();
    }
  });
};

watch(() => props.account.id, () => {
  bindings.value = [];
  targets.value = {};
  loaded.value = false;
  void load();
}, { immediate: true });

defineExpose({ load });
</script>

<style scoped>
.panel-alert {
  margin: 14px 20px;
}
.target-cell {
  display: grid;
  gap: 3px;
}
.target-cell small {
  font-size: 11px;
}
</style>
