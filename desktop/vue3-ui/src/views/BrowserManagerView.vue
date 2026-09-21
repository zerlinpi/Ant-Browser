<template>
  <div class="page-stack">
    <PageHeader title="浏览器实例" :description="`当前工作空间共 ${instances.length} 个隔离环境`">
      <button class="button button-secondary" type="button" :disabled="!selected.size" @click="batchCommand('stop')"><Icon icon="lucide:square" />批量停止</button>
      <button class="button button-secondary" type="button" :disabled="!selected.size" @click="batchCommand('start')"><Icon icon="lucide:play" />批量启动</button>
      <button class="button button-primary" type="button" @click="createOpen = true"><Icon icon="lucide:plus" />新建实例</button>
    </PageHeader>

    <section class="stat-grid compact">
      <article class="stat-card"><div><p>配置总数</p><strong>{{ instances.length }}</strong></div><span class="stat-icon blue"><Icon icon="lucide:files" /></span></article>
      <article class="stat-card"><div><p>运行中</p><strong>{{ runningCount }}</strong></div><span class="stat-icon green"><Icon icon="lucide:activity" /></span></article>
      <article class="stat-card"><div><p>已停止</p><strong>{{ instances.length - runningCount }}</strong></div><span class="stat-icon"><Icon icon="lucide:square" /></span></article>
    </section>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索名称或标签…" /></label>
        <select v-model="statusFilter" aria-label="状态筛选"><option value="">全部状态</option><option value="running">运行中</option><option value="stopped">已停止</option><option value="error">异常</option></select>
        <button class="button button-quiet" type="button" @click="load"><Icon icon="lucide:refresh-cw" />刷新</button>
      </div>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th class="checkbox-cell"><input type="checkbox" :checked="allSelected" aria-label="全选" @change="toggleAll" /></th><th>实例名称</th><th>状态</th><th>连接栈</th><th>账号 / 代理</th><th>标签</th><th>更新时间</th><th class="actions-cell">操作</th></tr></thead>
          <tbody>
            <tr v-for="item in filtered" :key="item.id">
              <td class="checkbox-cell"><input type="checkbox" :checked="selected.has(item.id)" :aria-label="`选择 ${item.name}`" @change="toggle(item.id)" /></td>
              <td><div class="primary-cell"><span class="row-icon"><Icon icon="lucide:monitor" /></span><div><strong>{{ item.name }}</strong><small>{{ shortId(item.id) }}</small></div></div></td>
              <td><StatusBadge :status="item.status" /></td>
              <td><span class="stack-badge">{{ item.connectorType || "xray" }}</span></td>
              <td><span class="muted-text">{{ item.accountId ? shortId(item.accountId) : "未绑定账号" }} · {{ item.proxyId ? shortId(item.proxyId) : "直连" }}</span></td>
              <td><div class="tag-list"><span v-for="tag in item.tags || []" :key="tag" class="tag">{{ tag }}</span><span v-if="!item.tags?.length" class="muted-text">—</span></div></td>
              <td>{{ formatDate(item.updatedAt) }}</td>
              <td class="actions-cell"><button class="icon-button dark" type="button" :aria-label="isRunning(item) ? '停止' : '启动'" @click="command(item, isRunning(item) ? 'stop' : 'start')"><Icon :icon="isRunning(item) ? 'lucide:square' : 'lucide:play'" /></button><button class="icon-button" type="button" aria-label="更多设置"><Icon icon="lucide:settings" /></button></td>
            </tr>
          </tbody>
        </table>
        <EmptyState v-if="!filtered.length && !loading" icon="lucide:monitor" title="没有匹配的浏览器实例" description="创建隔离环境后，可在这里统一启动、停止和批量管理。"><button class="button button-primary" @click="createOpen = true">新建实例</button></EmptyState>
      </div>
    </article>

    <ModalDialog :open="createOpen" title="新建浏览器实例" description="先创建基础隔离环境，账号、代理和指纹可在后续流程中绑定。" @close="createOpen = false">
      <label class="field"><span>实例名称</span><input v-model.trim="newName" maxlength="100" placeholder="例如：Amazon US 运营 01" /></label>
      <p class="form-note"><Icon icon="lucide:info" />新实例默认使用当前系统连接栈，不会在 xray 组合栈与 mihomo 栈之间自动切换。</p>
      <template #footer><button class="button button-secondary" @click="createOpen = false">取消</button><button class="button button-primary" :disabled="saving || !newName" @click="createInstance">创建实例</button></template>
    </ModalDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { Icon } from "@iconify/vue";
import { api, idempotencyKey } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useSessionStore } from "@/stores/session";
import type { BrowserInstance } from "@/types";

const route = useRoute();
const session = useSessionStore();
const instances = ref<BrowserInstance[]>([]);
const selected = ref(new Set<string>());
const query = ref(typeof route.query.q === "string" ? route.query.q : "");
const statusFilter = ref("");
const loading = ref(false);
const saving = ref(false);
const createOpen = ref(false);
const newName = ref("");
const isRunning = (item: BrowserInstance) => ["running", "online", "starting"].includes(item.status?.toLowerCase());
const runningCount = computed(() => instances.value.filter(isRunning).length);
const filtered = computed(() => instances.value.filter((item) => {
  const matchesQuery = !query.value || `${item.name} ${(item.tags || []).join(" ")}`.toLowerCase().includes(query.value.toLowerCase());
  const matchesStatus = !statusFilter.value || (statusFilter.value === "running" ? isRunning(item) : item.status?.toLowerCase() === statusFilter.value);
  return matchesQuery && matchesStatus;
}));
const allSelected = computed(() => filtered.value.length > 0 && filtered.value.every((item) => selected.value.has(item.id)));
const shortId = (value: string) => value.length > 12 ? `${value.slice(0, 8)}…` : value;
const formatDate = (value?: string) => value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" }).format(new Date(value)) : "—";
const load = async () => { loading.value = true; instances.value = await api.get<BrowserInstance[]>(`${session.workspaceBase}/browser-instances`).finally(() => { loading.value = false; }); };
const toggle = (id: string) => { const next = new Set(selected.value); next.has(id) ? next.delete(id) : next.add(id); selected.value = next; };
const toggleAll = () => { selected.value = allSelected.value ? new Set() : new Set(filtered.value.map((item) => item.id)); };
const command = async (item: BrowserInstance, action: "start" | "stop") => {
  await api.post(`${session.workspaceBase}/browser-instances/${item.id}/commands`, { action }, idempotencyKey(`instance-${item.id}-${action}`));
  await load();
};
const batchCommand = async (action: "start" | "stop") => {
  await api.post(`${session.workspaceBase}/batch/browser-instances/${action}`, { instanceIds: [...selected.value] }, idempotencyKey(`batch-${action}`));
  selected.value = new Set();
  await load();
};
const createInstance = async () => {
  saving.value = true;
  try { await api.post(`${session.workspaceBase}/browser-instances`, { name: newName.value }); createOpen.value = false; newName.value = ""; await load(); }
  finally { saving.value = false; }
};
onMounted(load);
</script>
