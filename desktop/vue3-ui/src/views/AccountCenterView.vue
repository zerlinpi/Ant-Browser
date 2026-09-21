<template>
  <div class="page-stack">
    <PageHeader title="账号中心" description="集中管理跨境平台账号、生命周期与风险状态">
      <button class="button button-primary" type="button" @click="createOpen = true"><Icon icon="lucide:plus" />添加账号</button>
    </PageHeader>

    <section class="stat-grid compact">
      <article class="stat-card"><div><p>账号总数</p><strong>{{ accounts.length }}</strong></div><span class="stat-icon blue"><Icon icon="lucide:badge-user" /></span></article>
      <article class="stat-card"><div><p>正常</p><strong>{{ statusCount('normal') + statusCount('active') }}</strong></div><span class="stat-icon green"><Icon icon="lucide:shield-check" /></span></article>
      <article class="stat-card"><div><p>需要处理</p><strong>{{ attentionCount }}</strong></div><span class="stat-icon orange"><Icon icon="lucide:triangle-alert" /></span></article>
    </section>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索平台、用户名或邮箱…" /></label>
        <select v-model="platform"><option value="">全部平台</option><option v-for="item in platforms" :key="item" :value="item">{{ item }}</option></select>
        <select v-model="status"><option value="">全部状态</option><option value="normal">正常</option><option value="abnormal">异常</option><option value="frozen">冻结</option><option value="verifying">待验证</option></select>
        <button class="button button-quiet" @click="load"><Icon icon="lucide:refresh-cw" />刷新</button>
      </div>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>账号</th><th>平台</th><th>状态</th><th>风险</th><th>绑定环境</th><th>更新时间</th><th class="actions-cell">操作</th></tr></thead>
          <tbody><tr v-for="item in filtered" :key="item.id">
            <td><div class="primary-cell"><span class="platform-avatar">{{ item.platform.slice(0, 1).toUpperCase() }}</span><div><strong>{{ item.username }}</strong><small>{{ item.email || '未设置邮箱' }}</small></div></div></td>
            <td>{{ item.platform }}</td>
            <td><StatusBadge :status="item.status" /></td>
            <td><span class="risk-badge" :class="`risk-${item.riskLevel || 'low'}`">{{ riskLabel(item.riskLevel) }}</span></td>
            <td class="muted-text">详情中管理</td>
            <td>{{ formatDate(item.updatedAt) }}</td>
            <td class="actions-cell"><button class="button button-quiet small" @click="setStatus(item, item.status === 'frozen' ? 'normal' : 'frozen')">{{ item.status === 'frozen' ? '恢复' : '冻结' }}</button><button class="icon-button" aria-label="编辑"><Icon icon="lucide:pencil" /></button></td>
          </tr></tbody>
        </table>
        <EmptyState v-if="!filtered.length && !loading" icon="lucide:badge-user" title="没有匹配的账号资产" description="添加账号后可绑定独立浏览器环境和固定代理。"><button class="button button-primary" @click="createOpen = true">添加账号</button></EmptyState>
      </div>
    </article>

    <ModalDialog :open="createOpen" title="添加账号资产" description="基础信息用于团队识别；密码、2FA 和 Cookie 请在创建后写入加密保管库。" @close="createOpen = false">
      <div class="form-grid"><label class="field"><span>平台</span><select v-model="form.platform"><option v-for="item in platforms" :key="item">{{ item }}</option></select></label><label class="field"><span>用户名</span><input v-model.trim="form.username" maxlength="160" /></label><label class="field field-span"><span>邮箱（可选）</span><input v-model.trim="form.email" type="email" /></label></div>
      <template #footer><button class="button button-secondary" @click="createOpen = false">取消</button><button class="button button-primary" :disabled="saving || !form.platform || !form.username" @click="createAccount">添加账号</button></template>
    </ModalDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useSessionStore } from "@/stores/session";
import type { AccountAsset } from "@/types";

const session = useSessionStore();
const platforms = ["Amazon", "Shopify", "TikTok", "Facebook", "Google", "eBay"];
const accounts = ref<AccountAsset[]>([]);
const query = ref("");
const platform = ref("");
const status = ref("");
const loading = ref(false);
const saving = ref(false);
const createOpen = ref(false);
const form = reactive({ platform: "Amazon", username: "", email: "" });
const statusCount = (value: string) => accounts.value.filter((item) => item.status?.toLowerCase() === value).length;
const attentionCount = computed(() => accounts.value.filter((item) => ["abnormal", "frozen", "verifying"].includes(item.status?.toLowerCase()) || ["high", "critical"].includes(item.riskLevel?.toLowerCase() || "")).length);
const filtered = computed(() => accounts.value.filter((item) => (!query.value || `${item.username} ${item.email || ""} ${item.platform}`.toLowerCase().includes(query.value.toLowerCase())) && (!platform.value || item.platform === platform.value) && (!status.value || item.status === status.value)));
const formatDate = (value?: string) => value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" }).format(new Date(value)) : "—";
const riskLabel = (value?: string) => ({ low: "低风险", medium: "中风险", high: "高风险", critical: "严重" }[value || "low"] || value || "低风险");
const load = async () => { loading.value = true; accounts.value = await api.get<AccountAsset[]>(`${session.workspaceBase}/accounts`).finally(() => { loading.value = false; }); };
const createAccount = async () => {
  saving.value = true;
  try { await api.post(`${session.workspaceBase}/accounts`, { platform: form.platform.toLowerCase(), username: form.username, email: form.email || undefined }); createOpen.value = false; form.username = ""; form.email = ""; await load(); }
  finally { saving.value = false; }
};
const setStatus = async (item: AccountAsset, next: string) => { await api.post(`${session.workspaceBase}/accounts/${item.id}/status`, { status: next }); await load(); };
onMounted(load);
</script>
