<template>
  <div class="page-stack settings-page">
    <PageHeader title="系统设置" description="管理 Cloud 连接、通知偏好与商业授权" />
    <nav class="settings-tabs"><button v-for="item in tabs" :key="item.id" :class="{active:tab===item.id}" @click="tab=item.id"><Icon :icon="item.icon" />{{ item.label }}</button></nav>
    <section v-if="tab==='connection'" class="panel settings-panel">
      <header class="panel-header"><div><h2>Cloud 连接</h2><p>桌面端通过安全 API 与 WebSocket 连接控制面。</p></div></header>
      <div class="settings-form"><label class="field"><span>Cloud API 地址</span><input v-model.trim="apiURL" type="url" /><small>生产环境应使用受信任的 HTTPS 域名。</small></label><div class="settings-row"><div><strong>连接状态</strong><p>{{ session.activeWorkspace?.name || '当前工作空间' }}</p></div><StatusBadge status="active" label="已连接" /></div><div class="settings-actions"><button class="button button-primary" @click="saveConnection">保存连接</button></div></div>
    </section>
    <section v-else-if="tab==='notifications'" class="panel settings-panel">
      <header class="panel-header"><div><h2>通知偏好</h2><p>系统内消息持久保存，实时与邮件渠道可独立配置。</p></div></header>
      <div class="preference-list"><label v-for="item in notificationOptions" :key="item.key" class="preference-row"><span class="preference-icon"><Icon :icon="item.icon" /></span><span><strong>{{ item.label }}</strong><small>{{ item.description }}</small></span><input v-model="preferences[item.key]" type="checkbox" /></label></div>
      <div class="settings-actions"><button class="button button-primary" :disabled="saving" @click="savePreferences">保存偏好</button></div>
    </section>
    <section v-else class="settings-billing-grid">
      <article class="panel plan-card"><p class="page-eyebrow">CURRENT PLAN</p><h2>{{ planName }}</h2><p>组织内共享实例、席位、自动化、存储与 API 配额。</p><StatusBadge :status="String(subscription.status||'active')" /></article>
      <article class="panel entitlement-panel"><header class="panel-header"><div><h2>组织配额</h2><p>授权服务不可用时受限资源默认拒绝。</p></div></header><div class="entitlement-list"><div v-for="item in entitlements" :key="keyOf(item)" class="entitlement-row"><div><strong>{{ labelOf(keyOf(item)) }}</strong><small>{{ usedOf(item) }} / {{ limitOf(item) }}</small></div><div class="quota-bar"><i :style="{width:`${percentOf(item)}%`}" /></div></div></div><EmptyState v-if="!entitlements.length" title="暂无授权数据" description="请检查授权服务连接。" /></article>
      <article class="panel license-panel"><header class="panel-header"><div><h2>激活商业授权</h2><p>许可证会校验签名、设备和到期时间。</p></div></header><label class="field"><span>许可证密钥</span><input v-model.trim="licenseKey" autocomplete="off" placeholder="ANT-…" /></label><button class="button button-primary" :disabled="saving||!licenseKey" @click="activateLicense">激活</button></article>
    </section>
  </div>
</template>
<script setup lang="ts">
import {computed,onMounted,reactive,ref} from "vue";
import {Icon} from "@iconify/vue";
import {api} from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import {useSessionStore} from "@/stores/session";
type Row=Record<string,unknown>;
const session=useSessionStore(),tab=ref("connection"),apiURL=ref(session.apiBaseURL),saving=ref(false),licenseKey=ref("");
const subscription=reactive<Row>({}),entitlements=ref<Row[]>([]),preferences=reactive<Record<string,boolean>>({websocket:true,email:false,accountRisk:true,proxyFailure:true,taskFailure:true,securityEvent:true});
const tabs=[{id:"connection",label:"Cloud 连接",icon:"lucide:cloud"},{id:"notifications",label:"通知",icon:"lucide:bell"},{id:"billing",label:"订阅与授权",icon:"lucide:badge-check"}];
const notificationOptions=[{key:"websocket",label:"桌面实时通知",description:"短期用途票据 WebSocket",icon:"lucide:radio"},{key:"email",label:"邮件通知",description:"高优先级事件邮件",icon:"lucide:mail"},{key:"accountRisk",label:"账号风险",description:"异常、冻结与验证事件",icon:"lucide:badge-alert"},{key:"proxyFailure",label:"代理失效",description:"真实出口与 IP 健康异常",icon:"lucide:globe-lock"},{key:"taskFailure",label:"任务失败",description:"重试耗尽与死信事件",icon:"lucide:list-x"},{key:"securityEvent",label:"安全事件",description:"登录、权限与敏感操作",icon:"lucide:shield-alert"}];
const orgId=computed(()=>session.activeWorkspace?.organizationId||""),planName=computed(()=>String(subscription.planName||subscription.planCode||"Free"));
const keyOf=(row:Row)=>String(row.key||row.featureKey||row.feature||"unknown"),usedOf=(row:Row)=>Number(row.usedValue??row.used??0),limitOf=(row:Row)=>Number(row.limitValue??row.limit??0)||"∞";
const percentOf=(row:Row)=>{const limit=Number(row.limitValue??row.limit??0);return limit?Math.min(100,usedOf(row)/limit*100):0;};
const labelOf=(key:string)=>({instances:"浏览器实例",team_members:"团队席位",automation_runs:"自动化次数",storage_bytes:"云端存储",api_calls:"API 调用"}[key]||key);
const saveConnection=()=>session.updateAPIBaseURL(apiURL.value);
const load=async()=>{if(session.workspaceBase)Object.assign(preferences,await api.get<Row>(`${session.workspaceBase}/notification-preferences`).catch(()=>({})));if(orgId.value){Object.assign(subscription,await api.get<Row>(`/api/v1/organizations/${orgId.value}/billing/subscription`).catch(()=>({})));entitlements.value=await api.get<Row[]>(`/api/v1/organizations/${orgId.value}/billing/entitlements`).catch(()=>[]);}};
const savePreferences=async()=>{saving.value=true;try{await api.put(`${session.workspaceBase}/notification-preferences`,preferences);}finally{saving.value=false;}};
const activateLicense=async()=>{saving.value=true;try{await api.post(`/api/v1/organizations/${orgId.value}/billing/licenses/activate`,{licenseKey:licenseKey.value});licenseKey.value="";await load();}finally{saving.value=false;}};
onMounted(load);
</script>
