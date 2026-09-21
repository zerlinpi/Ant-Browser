<template>
  <div class="page-stack">
    <PageHeader title="自动化中心" description="编排、发布并运行 Playwright、Puppeteer 与原生 CDP 工作流">
      <button class="button button-primary" @click="createOpen = true"><Icon icon="lucide:plus" />新建工作流</button>
    </PageHeader>

    <section class="engine-strip">
      <div v-for="engine in engines" :key="engine.id"><span class="engine-icon"><Icon :icon="engine.icon" /></span><div><strong>{{ engine.name }}</strong><p>{{ engine.description }}</p></div><StatusBadge status="active" label="可用" /></div>
    </section>

    <article class="panel">
      <div class="toolbar"><label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" placeholder="搜索工作流…" /></label><select v-model="status"><option value="">全部状态</option><option value="draft">草稿</option><option value="published">已发布</option><option value="archived">已归档</option></select><button class="button button-quiet" @click="load"><Icon icon="lucide:refresh-cw" />刷新</button></div>
      <div class="workflow-grid">
        <article v-for="item in filtered" :key="item.id" class="workflow-card">
          <header><span class="workflow-icon"><Icon icon="lucide:workflow" /></span><StatusBadge :status="item.status" /></header>
          <h2>{{ item.name }}</h2>
          <p>{{ engineName(item.engine) }} · 已发布版本 {{ item.publishedVersion || '—' }}</p>
          <footer><small>{{ formatDate(item.updatedAt) }}</small><div><button class="button button-quiet small"><Icon icon="lucide:pencil" />编辑</button><button class="button button-primary small" :disabled="item.status !== 'published'" @click="run(item)"><Icon icon="lucide:play" />运行</button></div></footer>
        </article>
      </div>
      <EmptyState v-if="!filtered.length && !loading" icon="lucide:workflow" title="还没有自动化工作流" description="从打开网页、点击、输入、等待、截图和数据采集等步骤开始编排。"><button class="button button-primary" @click="createOpen = true">新建工作流</button></EmptyState>
    </article>

    <ModalDialog :open="createOpen" title="新建自动化工作流" description="创建后进入版本化编辑流程，发布版本才能被任务与批量操作调用。" @close="createOpen = false">
      <div class="form-grid"><label class="field field-span"><span>工作流名称</span><input v-model.trim="form.name" maxlength="120" placeholder="例如：Amazon 登录与订单采集" /></label><label class="field field-span"><span>执行引擎</span><select v-model="form.engine"><option value="playwright">Playwright</option><option value="puppeteer">Puppeteer</option><option value="cdp">Chrome DevTools Protocol</option></select></label></div>
      <template #footer><button class="button button-secondary" @click="createOpen = false">取消</button><button class="button button-primary" :disabled="saving || !form.name" @click="createWorkflow">创建工作流</button></template>
    </ModalDialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from "vue";
import { Icon } from "@iconify/vue";
import { api, idempotencyKey } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useSessionStore } from "@/stores/session";
import type { Workflow } from "@/types";

const session = useSessionStore();
const workflows = ref<Workflow[]>([]);
const query = ref("");
const status = ref("");
const loading = ref(false);
const saving = ref(false);
const createOpen = ref(false);
const form = reactive({ name: "", engine: "playwright" as "playwright" | "puppeteer" | "cdp" });
const engines = [
  { id: "playwright", name: "Playwright", description: "跨浏览器可靠流程", icon: "lucide:boxes" },
  { id: "puppeteer", name: "Puppeteer", description: "Chrome 高级控制", icon: "lucide:bot" },
  { id: "cdp", name: "原生 CDP", description: "低延迟 DevTools 指令", icon: "lucide:terminal" },
];
const filtered = computed(() => workflows.value.filter((item) => (!query.value || item.name.toLowerCase().includes(query.value.toLowerCase())) && (!status.value || item.status === status.value)));
const engineName = (value?: string) => engines.find((item) => item.id === value)?.name || "Playwright";
const formatDate = (value?: string) => value ? new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" }).format(new Date(value)) : "刚刚更新";
const load = async () => { loading.value = true; workflows.value = await api.get<Workflow[]>(`${session.workspaceBase}/workflows`).finally(() => { loading.value = false; }); };
const createWorkflow = async () => {
  saving.value = true;
  try {
    await api.post(`${session.workspaceBase}/workflows`, { name: form.name, engine: form.engine, definition: { schemaVersion: 1, engine: form.engine, steps: [] } });
    createOpen.value = false; form.name = ""; await load();
  } finally { saving.value = false; }
};
const run = async (item: Workflow) => { await api.post(`${session.workspaceBase}/batch/workflow-executions`, { items: [{ workflowId: item.id }] }, idempotencyKey(`workflow-${item.id}`)); };
onMounted(load);
</script>
