<template>
  <div class="page-stack">
    <PageHeader title="任务中心" description="跟踪批量操作与自动化执行队列" />
    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" placeholder="搜索任务…" /></label>
        <select v-model="status"><option value="">全部状态</option><option v-for="item in statuses" :key="item">{{ item }}</option></select>
      </div>
      <div class="table-wrap">
        <table class="data-table"><thead><tr><th>任务</th><th>类型</th><th>状态</th><th>尝试</th><th>更新时间</th><th>操作</th></tr></thead><tbody><tr v-for="item in filtered" :key="item.id"><td class="mono">{{ item.id.slice(0,14) }}</td><td>{{ item.kind }}</td><td><StatusBadge :status="item.status" /></td><td>{{ item.attempt||0 }}</td><td>{{ formatDate(item.updatedAt) }}</td><td><button v-if="['queued','pending','running'].includes(item.status)" class="button button-quiet small" @click="cancel(item)">取消</button></td></tr></tbody></table>
        <EmptyState v-if="!filtered.length&&!loading" title="没有匹配的任务" description="批量操作与自动化运行都会进入统一任务队列。" />
      </div>
    </article>
  </div>
</template>
<script setup lang="ts">
import {computed,onMounted,ref} from "vue";
import {Icon} from "@iconify/vue";
import {api} from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import {useSessionStore} from "@/stores/session";
import type {TaskRun} from "@/types";
const session=useSessionStore(),tasks=ref<TaskRun[]>([]),query=ref(""),status=ref(""),loading=ref(false);
const statuses=["queued","running","completed","failed","cancelled"];
const filtered=computed(()=>tasks.value.filter(item=>(!query.value||`${item.id} ${item.kind}`.toLowerCase().includes(query.value.toLowerCase()))&&(!status.value||item.status===status.value)));
const formatDate=(value?:string)=>value?new Intl.DateTimeFormat("zh-CN",{dateStyle:"short",timeStyle:"short"}).format(new Date(value)):"—";
const load=async()=>{loading.value=true;tasks.value=await api.get<TaskRun[]>(`${session.workspaceBase}/tasks?limit=100`).finally(()=>loading.value=false);};
const cancel=async(item:TaskRun)=>{await api.post(`${session.workspaceBase}/tasks/${item.id}/cancel`);await load();};
onMounted(load);
</script>
