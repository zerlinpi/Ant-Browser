<template>
  <article class="panel">
    <header class="panel-header">
      <div><h2>风险事件</h2><p>{{ events.length ? `共 ${events.length} 条，按时间倒序` : "平台警告、验证与异常记录" }}</p></div>
      <button v-if="canManage" class="button button-secondary small" type="button" @click="modalOpen = true"><Icon icon="lucide:shield-plus" />记录事件</button>
    </header>
    <div v-if="error" class="alert alert-danger panel-alert" role="alert">
      <Icon icon="lucide:circle-alert" /><span>{{ error }}</span>
      <button class="button button-secondary small" type="button" :disabled="loading" @click="load">重试</button>
    </div>
    <div v-if="events.length" class="table-wrap">
      <table class="data-table">
        <thead><tr><th>时间</th><th>等级</th><th>事件</th><th>说明</th><th>记录人</th></tr></thead>
        <tbody>
          <tr v-for="item in events" :key="item.id">
            <td>{{ formatDateTime(item.createdAt) }}</td>
            <td><AccountRiskBadge :level="item.level" /></td>
            <td>
              <div class="event-cell">
                <strong>{{ riskEventCodeLabel(item.code) || riskEventCode(item.code) }}</strong>
                <small v-if="riskEventCodeLabel(item.code)" class="muted-text mono">{{ riskEventCode(item.code) }}</small>
              </div>
            </td>
            <td class="description-cell">{{ item.description || "—" }}</td>
            <td :title="item.createdBy">{{ item.createdBy && item.createdBy === session.user?.id ? "我" : shortId(item.createdBy) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
    <p v-else-if="loading" class="panel-empty"><Icon icon="lucide:loader-circle" class="spin" /> 正在加载风险事件…</p>
    <p v-else-if="!error" class="panel-empty">暂无风险事件</p>

    <AccountRiskEventModal :open="modalOpen" :account="account" @close="modalOpen = false" @saved="onSaved" @stale="emit('stale')" />
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import AccountRiskBadge from "@/components/account/AccountRiskBadge.vue";
import AccountRiskEventModal from "@/components/account/AccountRiskEventModal.vue";
import { describeAccountError, isNotFoundError, riskEventCode, riskEventCodeLabel } from "@/components/account/accountMeta";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset, AccountRiskEvent } from "@/types";
import { formatDateTime, shortId } from "@/utils/format";

const props = defineProps<{ account: AccountAsset }>();
/** A 404 here may mean the account itself is gone; the page re-checks it. */
const emit = defineEmits<{ stale: [] }>();

const session = useSessionStore();
const canManage = computed(() => session.can("account.manage"));
const events = ref<AccountRiskEvent[]>([]);
const loading = ref(false);
const error = ref("");
const modalOpen = ref(false);
const time = (value?: string) => Date.parse(value || "") || 0;

let sequence = 0;
const load = async () => {
  const request = ++sequence;
  loading.value = true;
  error.value = "";
  try {
    const list = await api.list<AccountRiskEvent>(`${session.workspaceBase}/accounts/${encodeURIComponent(props.account.id)}/risk-events`);
    if (request === sequence) events.value = [...list].sort((a, b) => time(b.createdAt) - time(a.createdAt));
  } catch (err) {
    if (request !== sequence) return;
    error.value = describeAccountError(err, "风险事件加载失败");
    if (isNotFoundError(err)) emit("stale");
  } finally {
    if (request === sequence) loading.value = false;
  }
};

const onSaved = async () => {
  modalOpen.value = false;
  await load();
};

watch(() => props.account.id, () => {
  events.value = [];
  void load();
}, { immediate: true });

defineExpose({ load });
</script>

<style scoped>
.panel-alert {
  margin: 14px 20px;
}
.event-cell {
  display: grid;
  gap: 3px;
}
.event-cell small {
  font-size: 11px;
}
.description-cell {
  min-width: 220px;
  max-width: 420px;
  white-space: normal;
  overflow-wrap: anywhere;
}
</style>
