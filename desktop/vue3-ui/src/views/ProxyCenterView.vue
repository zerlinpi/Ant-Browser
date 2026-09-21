<template>
  <div class="page-stack">
    <PageHeader title="代理中心" description="按当前连接栈管理节点、测速与真实出口健康">
      <button class="button button-secondary" type="button" :disabled="!proxies.length" @click="checkAll"><Icon icon="lucide:gauge" />测试全部</button>
      <button class="button button-primary" type="button" @click="createOpen = true"><Icon icon="lucide:plus" />添加代理</button>
    </PageHeader>

    <div class="stack-notice">
      <Icon icon="lucide:split" />
      <div><strong>连接栈严格隔离</strong><p><b>xray</b> 是 Xray + sing-box 组合栈；hysteria2、tuic、anytls 由组合栈中的 sing-box 执行。<b>mihomo</b> 为独立栈，实例启动、测速、预热与下载均不会跨栈回退。</p></div>
    </div>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索名称、主机或地区…" /></label>
        <select v-model="stackFilter"><option value="">全部连接栈</option><option value="xray">xray 组合栈</option><option value="mihomo">mihomo 独立栈</option></select>
        <select v-model="protocolFilter"><option value="">全部协议</option><option v-for="item in allProtocols" :key="item" :value="item">{{ item }}</option></select>
        <button class="button button-quiet" @click="load"><Icon icon="lucide:refresh-cw" />刷新</button>
      </div>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>代理名称</th><th>连接栈</th><th>协议 / 执行器</th><th>服务器</th><th>地区</th><th>延迟</th><th>IP 健康</th><th class="actions-cell">操作</th></tr></thead>
          <tbody><tr v-for="item in filtered" :key="item.id">
            <td><div class="primary-cell"><span class="row-icon"><Icon icon="lucide:globe-2" /></span><strong>{{ item.name }}</strong></div></td>
            <td><span class="stack-badge" :class="item.connectorType === 'mihomo' ? 'mihomo' : 'xray'">{{ item.connectorType || 'xray' }}</span></td>
            <td><strong>{{ item.protocol }}</strong><small class="cell-subtitle">{{ executorLabel(item.connectorType || 'xray', item.protocol) }}</small></td>
            <td>{{ item.host || '—' }}<span v-if="item.port">:{{ item.port }}</span></td>
            <td>{{ item.countryCode || '—' }}</td>
            <td><span :class="latencyTone(item.latencyMs)">{{ item.latencyMs ? `${item.latencyMs} ms` : '未测速' }}</span></td>
            <td><StatusBadge :status="item.status || 'unknown'" /></td>
            <td class="actions-cell"><button class="button button-quiet small" @click="healthCheck(item)">健康检查</button><button class="icon-button" aria-label="编辑"><Icon icon="lucide:pencil" /></button></td>
          </tr></tbody>
        </table>
        <EmptyState v-if="!filtered.length && !loading" icon="lucide:globe-2" title="没有匹配的代理节点" description="添加代理后，系统会按所属连接栈完成测速、出口检查和实例绑定。"><button class="button button-primary" @click="createOpen = true">添加代理</button></EmptyState>
      </div>
    </article>

    <ModalDialog :open="createOpen" title="添加代理" description="连接栈一经绑定即用于该节点的启动、检查、预热和下载流程。" @close="createOpen = false">
      <div class="form-grid">
        <label class="field field-span"><span>代理名称</span><input v-model.trim="form.name" maxlength="100" placeholder="例如：US-LA Residential 01" /></label>
        <label class="field"><span>连接栈</span><select v-model="form.connectorType"><option value="xray">xray 组合栈</option><option value="mihomo">mihomo 独立栈</option></select></label>
        <label class="field"><span>协议</span><select v-model="form.protocol"><option v-for="item in protocolsForStack" :key="item" :value="item">{{ item }}</option></select></label>
        <label class="field"><span>服务器</span><input v-model.trim="form.host" placeholder="proxy.example.com" /></label>
        <label class="field"><span>端口</span><input v-model.number="form.port" type="number" min="1" max="65535" /></label>
        <label class="field"><span>用户名（可选）</span><input v-model.trim="form.username" autocomplete="off" /></label>
        <label class="field"><span>密码（可选）</span><input v-model="form.password" type="password" autocomplete="new-password" /></label>
      </div>
      <p class="form-note"><Icon icon="lucide:cpu" />当前协议将由 {{ executorLabel(form.connectorType, form.protocol) }} 执行，不会自动改用另一套连接栈。</p>
      <template #footer><button class="button button-secondary" @click="createOpen = false">取消</button><button class="button button-primary" :disabled="saving || !form.name || !form.host || !form.port" @click="createProxy">添加并检查</button></template>
    </ModalDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useSessionStore } from "@/stores/session";
import type { ProxyNode } from "@/types";

const session = useSessionStore();
const allProtocols = ["http", "https", "socks5", "vmess", "vless", "trojan", "shadowsocks", "hysteria2", "tuic", "anytls"];
const xrayComboProtocols = [...allProtocols];
const mihomoProtocols = [...allProtocols];
const singBoxProtocols = new Set(["hysteria2", "tuic", "anytls"]);
const proxies = ref<ProxyNode[]>([]);
const query = ref("");
const stackFilter = ref("");
const protocolFilter = ref("");
const loading = ref(false);
const saving = ref(false);
const createOpen = ref(false);
const form = reactive({ name: "", connectorType: "xray" as "xray" | "mihomo", protocol: "http", host: "", port: 0, username: "", password: "" });
const protocolsForStack = computed(() => form.connectorType === "mihomo" ? mihomoProtocols : xrayComboProtocols);
watch(() => form.connectorType, () => { if (!protocolsForStack.value.includes(form.protocol)) form.protocol = protocolsForStack.value[0]; });
const filtered = computed(() => proxies.value.filter((item) => (!query.value || `${item.name} ${item.host || ""} ${item.countryCode || ""}`.toLowerCase().includes(query.value.toLowerCase())) && (!stackFilter.value || (item.connectorType || "xray") === stackFilter.value) && (!protocolFilter.value || item.protocol === protocolFilter.value)));
const executorLabel = (stack: "xray" | "mihomo", protocol: string) => stack === "mihomo" ? "Mihomo 独立栈" : singBoxProtocols.has(protocol) ? "xray 组合栈 · sing-box" : "xray 组合栈 · Xray";
const latencyTone = (value?: number) => !value ? "muted-text" : value < 250 ? "text-success" : value < 600 ? "text-warning" : "text-danger";
const load = async () => { loading.value = true; proxies.value = await api.get<ProxyNode[]>(`${session.workspaceBase}/proxies`).finally(() => { loading.value = false; }); };
const healthCheck = async (item: ProxyNode) => { await api.post(`${session.workspaceBase}/proxies/${item.id}/health-checks`); await load(); };
const checkAll = async () => { await Promise.allSettled(proxies.value.map(healthCheck)); };
const createProxy = async () => {
  saving.value = true;
  try {
    const created = await api.post<ProxyNode>(`${session.workspaceBase}/proxies`, { ...form, username: form.username || undefined, password: form.password || undefined });
    createOpen.value = false;
    Object.assign(form, { name: "", host: "", port: 0, username: "", password: "" });
    await healthCheck(created);
  } finally { saving.value = false; }
};
onMounted(load);
</script>
