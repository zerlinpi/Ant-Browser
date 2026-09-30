<template>
  <div class="page-stack">
    <PageHeader title="云端档案" description="浏览器数据的云端版本，由桌面端代理加密同步">
      <button v-if="canManage" class="button button-primary" type="button" @click="createOpen = true"><Icon icon="lucide:plus" />新建档案</button>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>
    <div v-if="created" class="alert alert-success" role="status">
      <Icon icon="lucide:circle-check" /><span>已创建档案「{{ created.name }}」</span>
      <RouterLink class="text-link" :to="{ name: 'profile-detail', params: { profileId: created.id } }">查看</RouterLink>
    </div>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索档案名称或 ID…" aria-label="搜索档案" /></label>
        <select v-model="statusFilter" aria-label="筛选状态">
          <option value="">全部状态</option>
          <option value="active">正常</option>
          <option value="syncing">同步中</option>
          <option value="conflict">有冲突</option>
        </select>
        <span class="toolbar-spacer"></span>
        <span class="muted-text profile-count">共 {{ profiles.length }} 个档案</span>
        <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon :class="{ spin: loading }" icon="lucide:refresh-cw" />刷新</button>
      </div>
      <div class="table-wrap">
        <table v-if="filtered.length" class="data-table profile-table">
          <thead><tr><th>档案</th><th>状态</th><th>当前版本</th><th>指纹模板</th><th>更新于</th></tr></thead>
          <tbody>
            <tr v-for="item in filtered" :key="item.id" @click="openFromRow($event, item)">
              <td>
                <div class="primary-cell">
                  <span class="row-icon"><Icon icon="lucide:cloud" /></span>
                  <div><RouterLink class="profile-link" :to="detailRoute(item)">{{ item.name }}</RouterLink><small class="mono" :title="item.id">{{ shortId(item.id) }}</small></div>
                </div>
              </td>
              <td><StatusBadge :status="item.status" :label="profileStatusLabel(item.status)" /></td>
              <td><code v-if="item.currentRevisionId" class="mono revision-id" :title="item.currentRevisionId">{{ shortId(item.currentRevisionId) }}</code><span v-else class="muted-text">尚未同步</span></td>
              <td><span :class="{ 'muted-text': !templateName(item.fingerprintTemplateId) }" :title="item.fingerprintTemplateId">{{ templateLabel(item.fingerprintTemplateId) }}</span></td>
              <td :title="formatFullDateTime(item.updatedAt)">{{ formatDateTime(item.updatedAt) }}</td>
            </tr>
          </tbody>
        </table>
        <EmptyState v-else-if="!loading && (profiles.length || !errorMessage)" icon="lucide:cloud" :title="profiles.length ? '没有匹配的档案' : '还没有云端档案'" :description="profiles.length ? '调整搜索条件或状态筛选。' : '创建档案后，桌面端代理即可将浏览器数据加密同步到云端。'">
          <button v-if="!profiles.length && canManage" class="button button-primary" type="button" @click="createOpen = true">新建档案</button>
        </EmptyState>
        <div v-if="loading && !profiles.length" class="panel-empty"><Icon class="spin" icon="lucide:loader-circle" /> 正在加载档案…</div>
      </div>
    </article>

    <CreateProfileDialog :open="createOpen" :templates="templates" :templates-loading="templatesLoading" :templates-error="templatesError" @close="createOpen = false" @created="onCreated" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Icon } from "@iconify/vue";
import { RouterLink, useRouter, type RouteLocationRaw } from "vue-router";
import { api, describeError } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import CreateProfileDialog from "@/components/profile/CreateProfileDialog.vue";
import { profileStatusLabel } from "@/components/profile/profileLabels";
import { useSessionStore } from "@/stores/session";
import type { CloudProfile, FingerprintTemplate } from "@/types";
import { formatDateTime, formatFullDateTime, shortId } from "@/utils/format";

const router = useRouter();
const session = useSessionStore();
const profiles = ref<CloudProfile[]>([]);
const templates = ref<FingerprintTemplate[]>([]);
const loading = ref(false);
const templatesLoading = ref(false);
const templatesError = ref("");
const errorMessage = ref("");
const created = ref<CloudProfile | null>(null);
const query = ref("");
const statusFilter = ref("");
const createOpen = ref(false);
let loadSequence = 0;

const canManage = computed(() => session.can("profile.manage"));
const templateNames = computed(() => new Map(templates.value.map((item) => [item.id, item.name])));
const templateName = (id?: string) => (id ? templateNames.value.get(id) : undefined);
const templateLabel = (id?: string) => (id ? templateName(id) ?? shortId(id) : "未绑定");
const detailRoute = (item: CloudProfile): RouteLocationRaw => ({ name: "profile-detail", params: { profileId: item.id } });

const filtered = computed(() => {
  const needle = query.value.toLowerCase();
  return profiles.value.filter((item) =>
    (!needle || `${item.name} ${item.id}`.toLowerCase().includes(needle)) &&
    (!statusFilter.value || item.status.toLowerCase() === statusFilter.value));
});

/** Template names are best-effort: a failure only degrades that column to IDs. */
const load = async () => {
  const base = session.workspaceBase;
  if (!base) return;
  const sequence = ++loadSequence;
  loading.value = true;
  templatesLoading.value = true;
  errorMessage.value = "";
  const [profileResult, templateResult] = await Promise.allSettled([
    api.list<CloudProfile>(`${base}/profiles`),
    api.list<FingerprintTemplate>(`${base}/fingerprint-templates`),
  ]);
  if (sequence !== loadSequence) return;
  if (profileResult.status === "fulfilled") profiles.value = profileResult.value;
  else errorMessage.value = describeError(profileResult.reason, "档案列表加载失败");
  if (templateResult.status === "fulfilled") {
    templates.value = templateResult.value;
    templatesError.value = "";
  } else {
    templatesError.value = "指纹模板加载失败";
  }
  loading.value = false;
  templatesLoading.value = false;
};

const openFromRow = (event: MouseEvent, item: CloudProfile) => {
  // Links and buttons inside the row handle their own activation.
  if ((event.target as HTMLElement | null)?.closest("a, button")) return;
  void router.push(detailRoute(item));
};

const onCreated = async (profile: CloudProfile) => {
  createOpen.value = false;
  created.value = profile;
  await load();
};

onMounted(() => void load());
</script>

<style scoped>
.profile-count{font-size:12px}.profile-table tbody tr{cursor:pointer}.profile-link{font-weight:650}.profile-link:hover{color:var(--blue);text-decoration:underline}.revision-id{padding:3px 7px;border-radius:6px;background:#f2f5f8;color:#4d5c72;font-size:11px}
</style>
