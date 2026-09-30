<template>
  <div class="page-stack">
    <div class="detail-heading">
      <RouterLink class="back-link" :to="{ name: 'profiles' }"><Icon icon="lucide:arrow-left" />云端档案</RouterLink>
      <PageHeader v-if="!notFound" :title="profile?.name || '档案详情'" description="版本历史与同步冲突">
        <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon :class="{ spin: loading }" icon="lucide:refresh-cw" />刷新</button>
      </PageHeader>
    </div>

    <article v-if="notFound" class="panel">
      <EmptyState icon="lucide:cloud-off" title="档案不存在" description="该档案可能已被删除，或不属于当前工作空间。">
        <RouterLink class="button button-secondary" :to="{ name: 'profiles' }"><Icon icon="lucide:arrow-left" />返回云端档案</RouterLink>
      </EmptyState>
    </article>

    <template v-else>
      <div v-if="errorMessage" class="alert alert-danger" role="alert">
        <Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span>
        <button class="button button-secondary small" type="button" :disabled="loading" @click="load">重试</button>
      </div>
      <div v-if="successMessage" class="alert alert-success" role="status"><Icon icon="lucide:circle-check" /><span>{{ successMessage }}</span></div>
      <article v-if="!profile && loading" class="panel"><p class="panel-empty"><Icon class="spin" icon="lucide:loader-circle" /> 正在加载档案…</p></article>

      <template v-if="profile">
        <article class="panel">
          <dl class="detail-list">
            <div><dt>状态</dt><dd><StatusBadge :status="profile.status" :label="profileStatusLabel(profile.status)" /></dd></div>
            <div><dt>当前版本</dt><dd>{{ currentRevisionLabel }}</dd></div>
            <div><dt>待处理冲突</dt><dd :class="{ 'text-warning': openConflictCount > 0 }">{{ openConflictCount }}</dd></div>
            <div><dt>档案 ID</dt><dd class="mono">{{ profile.id }}</dd></div>
            <div><dt>创建于</dt><dd>{{ formatFullDateTime(profile.createdAt) }}</dd></div>
            <div><dt>更新于</dt><dd>{{ formatFullDateTime(profile.updatedAt) }}</dd></div>
          </dl>
        </article>

        <article class="panel">
          <header class="panel-header">
            <div><h2>版本历史</h2><p>恢复历史版本需在桌面端代理中执行。</p></div>
            <span class="tag">{{ revisions.length }} 个版本</span>
          </header>
          <div class="table-wrap">
            <table v-if="revisions.length" class="data-table">
              <thead><tr><th>版本</th><th>状态</th><th v-if="showMode">模式</th><th>设备</th><th>创建于</th><th>提交于</th></tr></thead>
              <tbody>
                <tr v-for="item in revisions" :key="item.id" :class="{ 'is-current': isCurrent(item) }">
                  <td>
                    <div class="revision-cell"><strong>#{{ item.revision }}</strong><span v-if="isCurrent(item)" class="tag current-tag">当前</span></div>
                    <small class="cell-subtitle mono" :title="item.id">{{ shortId(item.id) }}</small>
                  </td>
                  <td><StatusBadge :status="item.status" :label="revisionStatusLabel(item.status)" /></td>
                  <td v-if="showMode">{{ revisionModeLabel(item.mode) }}</td>
                  <td><span :class="{ 'muted-text': !item.deviceId }" :title="item.deviceId">{{ deviceLabel(item.deviceId) }}</span></td>
                  <td :title="formatFullDateTime(item.createdAt)">{{ formatDateTime(item.createdAt) }}</td>
                  <td :title="item.committedAt ? formatFullDateTime(item.committedAt) : undefined"><span :class="{ 'muted-text': !item.committedAt }">{{ formatDateTime(item.committedAt) }}</span></td>
                </tr>
              </tbody>
            </table>
            <p v-else class="panel-empty">还没有版本，桌面端代理首次同步后会显示在这里。</p>
          </div>
        </article>

        <article class="panel">
          <header class="panel-header">
            <div><h2>同步冲突</h2><p>设备基于旧版本上传时产生，需选择保留哪一方。</p></div>
            <span v-if="openConflictCount" class="tag open-tag">{{ openConflictCount }} 个待处理</span>
          </header>
          <div class="table-wrap">
            <table v-if="conflicts.length" class="data-table">
              <thead><tr><th>状态</th><th>本地版本</th><th>云端版本</th><th>发生于</th><th>处理结果</th><th v-if="canSync" class="actions-cell">操作</th></tr></thead>
              <tbody>
                <tr v-for="item in conflicts" :key="item.id">
                  <td><StatusBadge :status="item.status" :label="conflictStatusLabel(item.status)" /></td>
                  <td>
                    <strong :title="item.localRevisionId">{{ revisionLabel(item.localRevisionId) }}</strong>
                    <small v-if="conflictSource(item)" class="cell-subtitle">来自 {{ conflictSource(item) }}</small>
                  </td>
                  <td><strong :title="item.remoteRevisionId">{{ revisionLabel(item.remoteRevisionId) }}</strong></td>
                  <td :title="formatFullDateTime(item.createdAt)">{{ formatDateTime(item.createdAt) }}</td>
                  <td>
                    <template v-if="item.resolution">{{ conflictResolutionLabel(item.resolution) }}<small class="cell-subtitle">{{ formatDateTime(item.resolvedAt) }}</small></template>
                    <span v-else class="muted-text">—</span>
                  </td>
                  <td v-if="canSync" class="actions-cell">
                    <template v-if="isOpen(item)">
                      <button class="button button-secondary small" type="button" @click="resolve(item, 'keep_local')">保留本地版本</button>
                      <button class="button button-secondary small" type="button" @click="resolve(item, 'keep_remote')">保留云端版本</button>
                    </template>
                  </td>
                </tr>
              </tbody>
            </table>
            <p v-else class="panel-empty">没有同步冲突。</p>
          </div>
        </article>
      </template>
    </template>

    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { Icon } from "@iconify/vue";
import { RouterLink, useRoute } from "vue-router";
import { api, ApiError, describeError } from "@/api/client";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import {
  conflictResolutionLabel, conflictStatusLabel, profileStatusLabel, revisionModeLabel, revisionStatusLabel,
} from "@/components/profile/profileLabels";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { AgentDevice, CloudProfile, ProfileConflict, ProfileConflictResolution, ProfileRevision } from "@/types";
import { formatDateTime, formatFullDateTime, shortId } from "@/utils/format";

/** profilesyncservice.Revision carries no `mode` today; the column appears only once the server returns it. */
type RevisionRow = ProfileRevision & { mode?: string };

// Profile IDs are UUIDs; anything else would only produce a database cast error.
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

const resolutionCopy: Record<ProfileConflictResolution, { title: string; message: string; success: string; danger: boolean }> = {
  keep_local: {
    title: "保留本地版本",
    message: "以设备上传的冲突版本覆盖云端当前版本，云端当前版本保留在历史中。",
    success: "已保留本地版本，该版本已成为云端当前版本",
    danger: false,
  },
  keep_remote: {
    title: "保留云端版本",
    message: "保留云端当前版本，丢弃该设备上传的冲突版本。",
    success: "已保留云端版本，冲突版本已丢弃",
    danger: true,
  },
};

const route = useRoute();
const session = useSessionStore();
const confirm = useConfirm();
const profile = ref<CloudProfile | null>(null);
const revisions = ref<RevisionRow[]>([]);
const conflicts = ref<ProfileConflict[]>([]);
const devices = ref<AgentDevice[]>([]);
const loading = ref(false);
const notFound = ref(false);
const errorMessage = ref("");
const successMessage = ref("");
let loadSequence = 0;

const profileId = computed(() => {
  const value = route.params.profileId;
  return (Array.isArray(value) ? value[0] : value) ?? "";
});
const isOpen = (item: ProfileConflict) => item.status.toLowerCase() === "open";
const isCurrent = (item: RevisionRow) => item.id === profile.value?.currentRevisionId;

const canSync = computed(() => session.can("profile.sync"));
const revisionById = computed(() => new Map(revisions.value.map((item) => [item.id, item])));
const showMode = computed(() => revisions.value.some((item) => Boolean(item.mode)));
const openConflictCount = computed(() => conflicts.value.filter(isOpen).length);
const currentRevisionLabel = computed(() => {
  const id = profile.value?.currentRevisionId;
  if (!id) return "尚未同步";
  const current = revisionById.value.get(id);
  return current ? `#${current.revision} · ${shortId(id)}` : shortId(id);
});

const revisionLabel = (id: string) => {
  const revision = revisionById.value.get(id);
  return revision ? `#${revision.revision}` : shortId(id);
};
/** GET /api/v1/devices only lists the caller's own devices; others fall back to a short ID. */
const deviceLabel = (id?: string) => (id ? devices.value.find((item) => item.id === id)?.name ?? shortId(id) : "—");
/** Device that uploaded the conflicting (local) revision, when known. */
const conflictSource = (item: ProfileConflict) => {
  const deviceId = revisionById.value.get(item.localRevisionId)?.deviceId;
  return deviceId ? deviceLabel(deviceId) : "";
};

/** Never throws, so a refresh after resolving a conflict cannot mask the success. */
const load = async () => {
  const base = session.workspaceBase;
  const id = profileId.value;
  if (!base) return;
  const sequence = ++loadSequence;
  errorMessage.value = "";
  if (!UUID_PATTERN.test(id)) {
    profile.value = null;
    notFound.value = true;
    loading.value = false;
    return;
  }
  loading.value = true;
  const path = `${base}/profiles/${encodeURIComponent(id)}`;
  try {
    const [profileItem, revisionItems, conflictItems, deviceItems] = await Promise.all([
      api.get<CloudProfile>(path),
      api.list<RevisionRow>(`${path}/revisions`),
      api.list<ProfileConflict>(`${path}/conflicts`),
      // Device names are best-effort decoration.
      api.list<AgentDevice>("/api/v1/devices").catch(() => devices.value),
    ]);
    if (sequence !== loadSequence) return;
    profile.value = profileItem;
    revisions.value = revisionItems;
    conflicts.value = conflictItems;
    devices.value = deviceItems;
    notFound.value = false;
  } catch (error) {
    if (sequence !== loadSequence) return;
    if (error instanceof ApiError && error.status === 404) {
      profile.value = null;
      notFound.value = true;
    } else {
      errorMessage.value = describeError(error, "档案加载失败");
    }
  } finally {
    if (sequence === loadSequence) loading.value = false;
  }
};

const resolve = (conflict: ProfileConflict, resolution: ProfileConflictResolution) => {
  const copy = resolutionCopy[resolution];
  successMessage.value = "";
  confirm.ask({ title: copy.title, message: copy.message, confirmText: copy.title, danger: copy.danger }, async () => {
    const path = `${session.workspaceBase}/profiles/${encodeURIComponent(profileId.value)}/conflicts/${encodeURIComponent(conflict.id)}/resolve`;
    try {
      await api.post<ProfileConflict>(path, { resolution });
    } catch (error) {
      // Resolved elsewhere or the cloud revision moved on: refresh what is behind the dialog.
      if (error instanceof ApiError && (error.status === 404 || error.status === 409)) void load();
      throw error;
    }
    successMessage.value = copy.success;
    await load();
  });
};

// The view is reused across /profiles/:profileId; never show one profile's data under another's ID.
watch(profileId, () => {
  profile.value = null;
  revisions.value = [];
  conflicts.value = [];
  notFound.value = false;
  successMessage.value = "";
  void load();
});
onMounted(() => void load());
</script>

<style scoped>
.revision-cell{display:flex;align-items:center;gap:7px}.current-tag{background:#edf4ff;color:var(--blue)}.open-tag{background:#fff5e6;color:#b87300}.data-table tr.is-current td{background:#f7faff}
</style>
