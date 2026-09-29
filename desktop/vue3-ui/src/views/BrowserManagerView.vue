<template>
  <div class="page-stack">
    <PageHeader title="浏览器实例" :description="`当前工作空间共 ${instances.length} 个隔离环境`">
      <template v-if="canOperate">
        <button class="button button-secondary" type="button" :disabled="!selected.size || !!batchPending" @click="batchCommand('stop')">
          <Icon :icon="batchPending === 'stop' ? 'lucide:loader-circle' : 'lucide:square'" :class="{ spin: batchPending === 'stop' }" />批量停止{{ selectionSuffix }}
        </button>
        <button class="button button-secondary" type="button" :disabled="!selected.size || !!batchPending" @click="batchCommand('start')">
          <Icon :icon="batchPending === 'start' ? 'lucide:loader-circle' : 'lucide:play'" :class="{ spin: batchPending === 'start' }" />批量启动{{ selectionSuffix }}
        </button>
      </template>
      <button v-if="canCreate" class="button button-primary" type="button" @click="openCreateDialog"><Icon icon="lucide:plus" />新建实例</button>
    </PageHeader>

    <div v-if="loadError" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ loadError }}</span></div>
    <div v-if="notice" class="alert" :class="`alert-${notice.tone}`" :role="notice.tone === 'success' ? 'status' : 'alert'">
      <Icon :icon="noticeIcons[notice.tone]" /><span>{{ notice.text }}</span>
      <button class="icon-button notice-dismiss" type="button" aria-label="关闭提示" @click="notice = null"><Icon icon="lucide:x" /></button>
    </div>

    <section class="stat-grid compact">
      <article class="stat-card"><div><p>配置总数</p><strong>{{ instances.length }}</strong></div><span class="stat-icon blue"><Icon icon="lucide:files" /></span></article>
      <article class="stat-card"><div><p>运行中</p><strong>{{ runningCount }}</strong></div><span class="stat-icon green"><Icon icon="lucide:activity" /></span></article>
      <article class="stat-card"><div><p>已停止</p><strong>{{ instances.length - runningCount }}</strong></div><span class="stat-icon"><Icon icon="lucide:square" /></span></article>
    </section>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索名称或标签…" aria-label="搜索浏览器实例" /></label>
        <select v-model="statusFilter" aria-label="状态筛选"><option value="">全部状态</option><option value="running">运行中</option><option value="starting">启动中</option><option value="offline">离线</option><option value="failed">异常</option></select>
        <button class="button button-quiet" type="button" :disabled="loading" @click="load()"><Icon icon="lucide:refresh-cw" :class="{ spin: loading }" />刷新</button>
      </div>
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th v-if="canOperate" class="checkbox-cell"><input type="checkbox" :checked="allSelected" aria-label="全选" @change="toggleAll" /></th>
              <th>实例名称</th><th>状态</th><th>运行设备</th><th>云端档案</th><th>代理</th><th>标签</th><th>更新时间</th><th v-if="hasRowActions" class="actions-cell">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="{ item, state, active, menu } in rows" :key="item.id">
              <td v-if="canOperate" class="checkbox-cell"><input type="checkbox" :checked="selected.has(item.id)" :aria-label="`选择 ${item.name}`" @change="toggle(item.id)" /></td>
              <td><div class="primary-cell"><span class="row-icon"><Icon icon="lucide:monitor" /></span><div><strong>{{ item.name }}</strong><small>{{ shortId(item.id) }}</small></div></div></td>
              <td>
                <StatusBadge :status="state.status" :label="state.label" />
                <span v-if="rowErrors[item.id]" class="row-error" role="alert">{{ rowErrors[item.id] }}</span>
              </td>
              <td>
                <template v-if="item.assignedDeviceId">
                  <span :title="item.assignedDeviceId">{{ deviceLabel(item.assignedDeviceId) }}</span>
                  <small class="cell-subtitle">{{ deviceHint(item.assignedDeviceId) }}</small>
                </template>
                <span v-else class="muted-text">未分配</span>
              </td>
              <td>
                <RouterLink v-if="item.profileId" class="text-link" :to="{ name: 'profile-detail', params: { profileId: item.profileId } }" :title="item.profileId">{{ profileLabel(item.profileId) }}</RouterLink>
                <span v-else class="muted-text">未绑定</span>
              </td>
              <td>
                <template v-if="instanceProxy(item)">
                  <span :title="instanceProxy(item)?.proxyId">{{ instanceProxy(item)?.proxyName || "已分配代理" }}</span>
                  <small class="cell-subtitle">{{ assignmentSourceLabel(instanceProxy(item)) }}</small>
                </template>
                <template v-else-if="item.proxyAssignmentId">
                  <span>已分配代理</span>
                  <small class="cell-subtitle" :title="item.proxyAssignmentId">分配 {{ shortId(item.proxyAssignmentId) }}</small>
                </template>
                <span v-else class="muted-text">直连</span>
              </td>
              <td><div class="tag-list"><span v-for="tag in item.tags ?? []" :key="tag" class="tag">{{ tag }}</span><span v-if="!item.tags?.length" class="muted-text">—</span></div></td>
              <td>{{ formatDateTime(item.updatedAt) }}</td>
              <td v-if="hasRowActions" class="actions-cell">
                <button
                  v-if="canOperate"
                  class="icon-button dark"
                  type="button"
                  :disabled="!item.assignedDeviceId || !!rowBusy[item.id]"
                  :title="toggleTitle(item, active)"
                  :aria-label="`${active ? '停止' : '启动'} ${item.name}`"
                  @click="runRowCommand(item, active ? 'instance.stop' : 'instance.start')"
                >
                  <Icon :icon="rowBusy[item.id] ? 'lucide:loader-circle' : active ? 'lucide:square' : 'lucide:play'" :class="{ spin: !!rowBusy[item.id] }" />
                </button>
                <ActionMenu v-if="menu.length" :items="menu" :disabled="!!rowBusy[item.id]" :label="`${item.name} 的更多操作`" @select="onRowAction(item, $event)" />
              </td>
            </tr>
          </tbody>
        </table>
        <EmptyState
          v-if="!rows.length && !loading && !loadError"
          icon="lucide:monitor"
          :title="instances.length ? '没有匹配的浏览器实例' : '还没有浏览器实例'"
          :description="instances.length ? '调整搜索词或状态筛选后重试。' : '创建隔离环境后，可在这里统一启动、停止和批量管理。'"
        >
          <button v-if="canCreate && !instances.length" class="button button-primary" type="button" @click="openCreateDialog">新建实例</button>
        </EmptyState>
      </div>
    </article>

    <ModalDialog :open="createOpen" title="新建浏览器实例" description="创建隔离环境并选择可选的指纹模板，账号和代理可在后续流程中绑定。" @close="closeCreateDialog">
      <div class="form-grid">
        <label class="field field-span"><span>实例名称</span><input v-model.trim="newName" maxlength="100" placeholder="例如：Amazon US 运营 01" /></label>
        <label class="field field-span">
          <span>指纹模板（可选）</span>
          <select v-model="newFingerprintTemplateId" :disabled="fingerprintTemplatesLoading || !!fingerprintTemplatesError" :aria-busy="fingerprintTemplatesLoading">
            <option value="">不绑定指纹模板</option>
            <option v-for="template in fingerprintTemplates" :key="template.id" :value="template.id">
              {{ template.name }} · Chromium {{ template.browserMajor }} · {{ platformLabel(template.platform) }}
            </option>
          </select>
          <small v-if="fingerprintTemplatesLoading">正在加载当前工作空间的指纹模板…</small>
          <small v-else-if="!fingerprintTemplatesError && !fingerprintTemplates.length">当前工作空间暂无可用模板，仍可创建不绑定指纹的实例。</small>
          <small v-else-if="selectedFingerprintTemplate">{{ selectedFingerprintTemplate.locale }} · {{ selectedFingerprintTemplate.timezone }} · {{ selectedFingerprintTemplate.mode }}</small>
          <small v-else>不选择模板时，实例将保留默认浏览器指纹配置。</small>
        </label>
        <label class="field field-span">
          <span>运行设备（可选）</span>
          <select v-model="newAssignedDeviceId">
            <option value="">暂不分配</option>
            <option v-for="device in workspaceDevices" :key="device.id" :value="device.id">{{ deviceOptionLabel(device) }}</option>
          </select>
          <small>{{ workspaceDevices.length ? "只有分配了有效设备的实例才能启动。" : "当前工作空间尚未注册桌面 Agent，可先创建配置后再分配。" }}</small>
        </label>
      </div>
      <div v-if="fingerprintTemplatesError" class="alert alert-danger" role="alert">
        <Icon icon="lucide:triangle-alert" />
        <span>{{ fingerprintTemplatesError }}</span>
        <button class="button button-secondary small" type="button" :disabled="fingerprintTemplatesLoading" @click="loadFingerprintTemplates">重试</button>
      </div>
      <div v-if="createError" class="alert alert-danger" role="alert"><Icon icon="lucide:triangle-alert" /><span>{{ createError }}</span></div>
      <p class="form-note"><Icon icon="lucide:info" />新实例默认使用当前系统连接栈，不会在 xray 组合栈与 mihomo 栈之间自动切换。</p>
      <template #footer><button class="button button-secondary" type="button" :disabled="saving" @click="closeCreateDialog">取消</button><button class="button button-primary" type="button" :disabled="saving || fingerprintTemplatesLoading || !newName" @click="createInstance">{{ saving ? "正在创建…" : "创建实例" }}</button></template>
    </ModalDialog>

    <InstanceEditDialog
      :open="!!editingId"
      :instance="editing"
      :devices="workspaceDevices"
      :device-name="deviceLabel"
      :templates="fingerprintTemplates"
      :templates-loading="fingerprintTemplatesLoading"
      :templates-error="fingerprintTemplatesError"
      :taken-names="instanceNames"
      :workspace-base="session.workspaceBase"
      @close="editingId = ''"
      @saved="onEdited"
      @refresh="load({ background: true })"
      @retry-templates="loadFingerprintTemplates"
    />
    <InstanceCloneDialog
      :open="!!cloningId"
      :instance="cloning"
      :devices="workspaceDevices"
      :device-name="deviceLabel"
      :taken-names="instanceNames"
      :workspace-base="session.workspaceBase"
      @close="cloningId = ''"
      @cloned="onCloned"
    />
    <InstanceMigrateDialog
      :open="!!migratingId"
      :instance="migrating"
      :devices="workspaceDevices"
      :device-name="deviceLabel"
      :workspace-base="session.workspaceBase"
      @close="migratingId = ''"
      @migrated="onMigrated"
      @refresh="load({ background: true })"
    />
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { Icon } from "@iconify/vue";
import { ApiError, api, describeError, formatBatchOutcome, idempotencyKey, summarizeBatch } from "@/api/client";
import ActionMenu, { type ActionMenuItem } from "@/components/ActionMenu.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import InstanceCloneDialog from "@/components/browser/InstanceCloneDialog.vue";
import InstanceEditDialog from "@/components/browser/InstanceEditDialog.vue";
import InstanceMigrateDialog from "@/components/browser/InstanceMigrateDialog.vue";
import {
  MAX_BATCH_ITEMS,
  commandWindowMs,
  deviceOptionLabel,
  deviceStatusLabel,
  instanceState,
  isActive,
  isMigrating,
  isRuntimeActive,
  isTransitional,
  isUsableDevice,
  migrationTargets,
  platformLabel,
} from "@/components/browser/instanceState";
import { useConfirm } from "@/composables/useConfirm";
import { useSessionStore } from "@/stores/session";
import type { AgentDevice, BatchResult, BrowserInstance, BrowserInstanceAction, CloudProfile, FingerprintTemplate, InstanceCommandResult, ProxyAssignment } from "@/types";
import { formatDateTime, shortId } from "@/utils/format";

type NoticeTone = "success" | "warning" | "danger";
type BatchAction = "start" | "stop";

const POLL_INTERVAL_MS = 5_000;
const noticeIcons: Record<NoticeTone, string> = { success: "lucide:circle-check", warning: "lucide:triangle-alert", danger: "lucide:circle-alert" };
const commandLabels: Record<BrowserInstanceAction, string> = {
  "instance.start": "启动",
  "instance.stop": "停止",
  "instance.restart": "重启",
  "instance.migrate": "迁移",
};
// RequestCommand answers instance_validation_failed when the assigned device is missing, revoked or foreign.
const commandErrorCopy = { instance_validation_failed: "运行设备无效或已被撤销，请重新分配设备" };

const queryText = (value: unknown) => (typeof value === "string" ? value.trim() : "");

const route = useRoute();
const session = useSessionStore();
const confirm = useConfirm();
const instances = ref<BrowserInstance[]>([]);
const devices = ref<AgentDevice[]>([]);
const profiles = ref<CloudProfile[]>([]);
const proxyAssignments = ref<ProxyAssignment[]>([]);
const selected = ref(new Set<string>());
const query = ref(queryText(route.query.q));
const statusFilter = ref("");
const loading = ref(false);
const loadError = ref("");
const notice = ref<{ tone: NoticeTone; text: string } | null>(null);
const batchPending = ref<BatchAction | "">("");
const rowBusy = reactive<Record<string, BrowserInstanceAction>>({});
const rowErrors = reactive<Record<string, string>>({});
const editingId = ref("");
const cloningId = ref("");
const migratingId = ref("");
const saving = ref(false);
const createOpen = ref(false);
const newName = ref("");
const newFingerprintTemplateId = ref("");
const newAssignedDeviceId = ref("");
const fingerprintTemplates = ref<FingerprintTemplate[]>([]);
const fingerprintTemplatesLoading = ref(false);
const fingerprintTemplatesError = ref("");
const createError = ref("");

// The topbar search pushes /browsers?q=… onto the same route, which reuses this component.
watch(() => route.query.q, (value) => {
  query.value = queryText(value);
});

const canCreate = computed(() => session.can("instance.create"));
const canUpdate = computed(() => session.can("instance.update"));
const canOperate = computed(() => session.can("instance.operate"));
const canDelete = computed(() => session.can("instance.delete"));
const hasRowActions = computed(() => canOperate.value || canUpdate.value || canCreate.value || canDelete.value);

const selectedFingerprintTemplate = computed(() => fingerprintTemplates.value.find((item) => item.id === newFingerprintTemplateId.value));
const workspaceDevices = computed(() => devices.value.filter((device) => isUsableDevice(device, session.activeWorkspaceId)));
const devicesById = computed(() => new Map(devices.value.map((device) => [device.id, device])));
const profilesById = computed(() => new Map(profiles.value.map((profile) => [profile.id, profile])));
const instanceNames = computed(() => instances.value.map((item) => item.name));
const findInstance = (id: string) => (id ? instances.value.find((item) => item.id === id) ?? null : null);
const editing = computed(() => findInstance(editingId.value));
const cloning = computed(() => findInstance(cloningId.value));
const migrating = computed(() => findInstance(migratingId.value));

// GET /devices lists only devices registered by the current user.
const deviceLabel = (id: string) => devicesById.value.get(id)?.name ?? shortId(id);
const deviceHint = (id: string) => {
  const device = devicesById.value.get(id);
  return device ? deviceStatusLabel(device.status) : "其他成员的设备";
};
const profileLabel = (id: string) => profilesById.value.get(id)?.name ?? shortId(id);
// The proxy center assigns proxies to instances directly; proxyAssignmentId
// is an explicit reference that may name an account or profile assignment.
const proxyAssignmentLookup = computed(() => {
  const byInstance = new Map<string, ProxyAssignment>();
  const byId = new Map<string, ProxyAssignment>();
  for (const assignment of proxyAssignments.value) {
    byId.set(assignment.id, assignment);
    if (assignment.targetType === "browser_instance") byInstance.set(assignment.targetId, assignment);
  }
  return { byInstance, byId };
});
const instanceProxy = (item: BrowserInstance) =>
  proxyAssignmentLookup.value.byInstance.get(item.id) ??
  (item.proxyAssignmentId ? proxyAssignmentLookup.value.byId.get(item.proxyAssignmentId) : undefined);
const assignmentSources: Record<string, string> = { browser_instance: "实例分配", account: "经账号分配", profile: "经档案分配" };
const assignmentSourceLabel = (assignment?: ProxyAssignment) => (assignment ? assignmentSources[assignment.targetType] ?? "已分配" : "");

const isUp = (item: BrowserInstance) => ["running", "starting"].includes(item.observedState.toLowerCase());
const runningCount = computed(() => instances.value.filter(isUp).length);
const filtered = computed(() => {
  const text = query.value.toLowerCase();
  return instances.value.filter((item) => {
    const matchesQuery = !text || `${item.name} ${(item.tags ?? []).join(" ")}`.toLowerCase().includes(text);
    const matchesStatus = !statusFilter.value || item.observedState.toLowerCase() === statusFilter.value;
    return matchesQuery && matchesStatus;
  });
});

const menuItems = (item: BrowserInstance): ActionMenuItem[] => {
  const items: ActionMenuItem[] = [];
  const noDevice = !item.assignedDeviceId;
  const moving = isMigrating(item);
  if (canUpdate.value) items.push({ key: "edit", label: "编辑", icon: "lucide:pencil" });
  if (canCreate.value) items.push({ key: "clone", label: "克隆", icon: "lucide:copy" });
  if (canOperate.value) {
    const restartBlock = noDevice ? "未分配运行设备" : moving ? "迁移进行中" : "";
    items.push({ key: "restart", label: "重启", icon: "lucide:rotate-cw", disabled: Boolean(restartBlock), hint: restartBlock || undefined });
    const migrateBlock = noDevice
      ? "未分配运行设备"
      : !item.profileId
        ? "需先绑定云端档案"
        : moving
          ? "迁移进行中"
          : !migrationTargets(workspaceDevices.value, item).length ? "没有其他可用设备" : "";
    items.push({ key: "migrate", label: "迁移到其他设备", icon: "lucide:arrow-right-left", disabled: Boolean(migrateBlock), hint: migrateBlock || undefined });
  }
  if (canDelete.value) {
    const deleteBlock = isRuntimeActive(item) ? "请先停止实例" : "";
    items.push({ key: "delete", label: "删除", icon: "lucide:trash-2", danger: true, disabled: Boolean(deleteBlock), hint: deleteBlock || undefined });
  }
  return items;
};
const rows = computed(() => filtered.value.map((item) => ({ item, state: instanceState(item), active: isActive(item), menu: menuItems(item) })));
const allSelected = computed(() => filtered.value.length > 0 && filtered.value.every((item) => selected.value.has(item.id)));
const selectionSuffix = computed(() => (selected.value.size ? `（${selected.value.size}）` : ""));
const toggleTitle = (item: BrowserInstance, active: boolean) => {
  if (!item.assignedDeviceId) return "请先为实例分配运行设备";
  if (isMigrating(item)) return "停止（将中止迁移）";
  return active ? "停止" : "启动";
};

// Polling: `${id}@${version}` → client time until which that row version is re-fetched.
// Each agent report bumps the version, so a watch ends as soon as the row changes;
// the window (the server command deadline) only bounds polling for silent devices.
const watchedUntil = new Map<string, number>();
const watchKey = (item: BrowserInstance) => `${item.id}@${item.version}`;
const watchInstance = (item: BrowserInstance, windowMs: number) => {
  const key = watchKey(item);
  if (!watchedUntil.has(key)) watchedUntil.set(key, Date.now() + windowMs);
};
const trackTransitions = (items: BrowserInstance[]) => {
  const keys = new Set(items.map(watchKey));
  for (const key of [...watchedUntil.keys()]) if (!keys.has(key)) watchedUntil.delete(key);
  for (const item of items) {
    if (isTransitional(item)) watchInstance(item, commandWindowMs(item));
  }
};
const needsPolling = () => {
  const now = Date.now();
  return instances.value.some((item) => (watchedUntil.get(watchKey(item)) ?? 0) > now);
};
let pollTimer: ReturnType<typeof setTimeout> | undefined;
let disposed = false;
const stopPolling = () => {
  if (pollTimer !== undefined) clearTimeout(pollTimer);
  pollTimer = undefined;
};
const schedulePoll = () => {
  stopPolling();
  if (disposed || !needsPolling()) return;
  pollTimer = setTimeout(() => {
    pollTimer = undefined;
    void load({ background: true });
  }, POLL_INTERVAL_MS);
};

let loadSeq = 0;
const load = async (options: { background?: boolean } = {}) => {
  const base = session.workspaceBase;
  if (!base) return;
  const seq = ++loadSeq;
  if (!options.background) loading.value = true;
  try {
    // Devices, profiles and proxy assignments only improve labels, so their
    // failures are not fatal. Background polls skip the slow-changing ones.
    const [instanceItems, deviceItems, profileItems, assignmentItems] = await Promise.all([
      api.list<BrowserInstance>(`${base}/browser-instances`),
      api.list<AgentDevice>("/api/v1/devices").catch(() => null),
      options.background ? Promise.resolve(null) : api.list<CloudProfile>(`${base}/profiles`).catch(() => null),
      options.background ? Promise.resolve(null) : api.list<ProxyAssignment>(`${base}/proxy-assignments`).catch(() => null),
    ]);
    if (seq !== loadSeq) return;
    instances.value = instanceItems;
    if (deviceItems) devices.value = deviceItems;
    if (profileItems) profiles.value = profileItems;
    if (assignmentItems) proxyAssignments.value = assignmentItems;
    const ids = new Set(instanceItems.map((item) => item.id));
    selected.value = new Set([...selected.value].filter((id) => ids.has(id)));
    for (const id of Object.keys(rowErrors)) if (!ids.has(id)) delete rowErrors[id];
    trackTransitions(instanceItems);
    loadError.value = "";
  } catch (error) {
    if (seq === loadSeq) loadError.value = describeError(error, "浏览器实例加载失败");
  } finally {
    if (seq === loadSeq) {
      loading.value = false;
      schedulePoll();
    }
  }
};

const loadFingerprintTemplates = async () => {
  fingerprintTemplatesLoading.value = true;
  fingerprintTemplatesError.value = "";
  if (!session.workspaceBase) {
    fingerprintTemplates.value = [];
    newFingerprintTemplateId.value = "";
    fingerprintTemplatesError.value = "请先选择工作空间，再加载指纹模板。";
    fingerprintTemplatesLoading.value = false;
    return;
  }
  try {
    fingerprintTemplates.value = await api.list<FingerprintTemplate>(`${session.workspaceBase}/fingerprint-templates`);
    if (newFingerprintTemplateId.value && !fingerprintTemplates.value.some((item) => item.id === newFingerprintTemplateId.value)) {
      newFingerprintTemplateId.value = "";
    }
  } catch (error) {
    fingerprintTemplates.value = [];
    newFingerprintTemplateId.value = "";
    fingerprintTemplatesError.value = describeError(error, "指纹模板加载失败，请重试。");
  } finally {
    fingerprintTemplatesLoading.value = false;
  }
};
const openCreateDialog = () => {
  createError.value = "";
  createOpen.value = true;
  if (!fingerprintTemplatesLoading.value) void loadFingerprintTemplates();
};
const closeCreateDialog = () => {
  if (saving.value) return;
  createOpen.value = false;
  newName.value = "";
  newFingerprintTemplateId.value = "";
  newAssignedDeviceId.value = "";
  createError.value = "";
};
const toggle = (id: string) => {
  const next = new Set(selected.value);
  if (next.has(id)) next.delete(id);
  else next.add(id);
  selected.value = next;
};
const toggleAll = () => {
  selected.value = allSelected.value ? new Set() : new Set(filtered.value.map((item) => item.id));
};

const replaceInstance = (updated: BrowserInstance) => {
  instances.value = instances.value.map((item) => (item.id === updated.id ? updated : item));
};
const applyCommandResult = (result: InstanceCommandResult | null | undefined, action: BrowserInstanceAction) => {
  const updated = result?.instance;
  if (!updated?.id) return;
  replaceInstance(updated);
  // Covers restart too: its desired state never differs from the observed one.
  watchInstance(updated, commandWindowMs(updated, action));
};

const runRowCommand = async (item: BrowserInstance, action: BrowserInstanceAction) => {
  if (rowBusy[item.id]) return;
  rowBusy[item.id] = action;
  delete rowErrors[item.id];
  try {
    const result = await api.post<InstanceCommandResult>(
      `${session.workspaceBase}/browser-instances/${item.id}/commands`,
      { action, expectedVersion: item.version },
      idempotencyKey(`instance-${action}`),
    );
    applyCommandResult(result, action);
    if (action === "instance.restart") notice.value = { tone: "success", text: `已向「${item.name}」发送重启指令` };
  } catch (error) {
    rowErrors[item.id] = `${commandLabels[action]}失败：${describeError(error, "请稍后重试", commandErrorCopy)}`;
  } finally {
    delete rowBusy[item.id];
  }
  await load({ background: true });
};

const batchCommand = async (action: BatchAction) => {
  if (batchPending.value) return;
  const label = action === "start" ? "批量启动" : "批量停止";
  const chosen = instances.value.filter((item) => selected.value.has(item.id));
  const eligible = chosen.filter((item) => item.assignedDeviceId);
  const skipped = chosen.length - eligible.length;
  const skippedText = skipped ? `已跳过 ${skipped} 个未分配运行设备的实例` : "";
  notice.value = null;
  if (!eligible.length) {
    notice.value = { tone: "warning", text: `${label}未执行：${skippedText}` };
    return;
  }
  if (eligible.length > MAX_BATCH_ITEMS) {
    notice.value = { tone: "warning", text: `${label}单次最多 ${MAX_BATCH_ITEMS} 个实例，请减少选择` };
    return;
  }
  const commandAction: BrowserInstanceAction = action === "start" ? "instance.start" : "instance.stop";
  batchPending.value = action;
  for (const item of eligible) rowBusy[item.id] = commandAction;
  try {
    // HTTP 207 still resolves; per-item failures live in the BatchResult.
    const result = await api.post<BatchResult<InstanceCommandResult>>(
      `${session.workspaceBase}/batch/browser-instances/${action}`,
      { items: eligible.map((item) => ({ instanceId: item.id, expectedVersion: item.version })) },
      idempotencyKey(`batch-${action}`),
    );
    for (const entry of result?.items ?? []) {
      if (entry.status === "succeeded") applyCommandResult(entry.value, commandAction);
    }
    const outcome = summarizeBatch(result);
    notice.value = {
      tone: outcome.failed || skipped ? "warning" : "success",
      text: [`${label}：${formatBatchOutcome(outcome)}`, skippedText].filter(Boolean).join("；"),
    };
    // itemId defaults to instanceId; failed rows stay selected for a retry.
    selected.value = new Set(outcome.failures.map((failure) => failure.itemId).filter((id): id is string => Boolean(id)));
  } catch (error) {
    notice.value = { tone: "danger", text: `${label}失败：${describeError(error, "请稍后重试")}` };
  } finally {
    batchPending.value = "";
    for (const item of eligible) delete rowBusy[item.id];
  }
  await load({ background: true });
};

const openEdit = (item: BrowserInstance) => {
  editingId.value = item.id;
  if (!fingerprintTemplatesLoading.value) void loadFingerprintTemplates();
};
const askDelete = (item: BrowserInstance) => {
  confirm.ask(
    { title: "删除浏览器实例", message: `确定删除「${item.name}」？删除后无法恢复。`, confirmText: "删除", danger: true },
    async () => {
      // Re-read the row: agent state reports bump the version while the dialog is open.
      const current = findInstance(item.id) ?? item;
      try {
        await api.delete(`${session.workspaceBase}/browser-instances/${current.id}`, { expectedVersion: current.version });
      } catch (error) {
        if (error instanceof ApiError && error.code === "version_conflict") await load({ background: true });
        throw new Error(describeError(error, "删除失败，请稍后重试", {
          instance_state_conflict: "实例运行中或迁移中，请先停止后再删除。",
          version_conflict: "实例刚刚发生变化，已刷新最新版本，请再次确认。",
        }));
      }
      notice.value = { tone: "success", text: `已删除「${current.name}」` };
      await load({ background: true });
    },
  );
};
const onRowAction = (item: BrowserInstance, key: string) => {
  if (key === "edit") openEdit(item);
  else if (key === "clone") cloningId.value = item.id;
  else if (key === "restart") void runRowCommand(item, "instance.restart");
  else if (key === "migrate") migratingId.value = item.id;
  else if (key === "delete") askDelete(item);
};
const onEdited = (updated: BrowserInstance) => {
  editingId.value = "";
  replaceInstance(updated);
  notice.value = { tone: "success", text: `已保存「${updated.name}」` };
  void load({ background: true });
};
const onCloned = (created: BrowserInstance) => {
  cloningId.value = "";
  notice.value = { tone: "success", text: `已克隆为「${created.name}」` };
  void load({ background: true });
};
const onMigrated = (result: InstanceCommandResult, targetDeviceId: string) => {
  const name = migrating.value?.name ?? result.instance?.name ?? "";
  migratingId.value = "";
  applyCommandResult(result, "instance.migrate");
  notice.value = { tone: "success", text: `已开始将「${name}」迁移到「${deviceLabel(targetDeviceId)}」，完成后将自动启动` };
  void load({ background: true });
};

const createInstance = async () => {
  saving.value = true;
  createError.value = "";
  try {
    const created = await api.post<BrowserInstance>(`${session.workspaceBase}/browser-instances`, {
      name: newName.value,
      fingerprintTemplateId: newFingerprintTemplateId.value || undefined,
      assignedDeviceId: newAssignedDeviceId.value || undefined,
    });
    createOpen.value = false;
    notice.value = { tone: "success", text: `已创建「${created?.name ?? newName.value}」` };
    newName.value = "";
    newFingerprintTemplateId.value = "";
    newAssignedDeviceId.value = "";
    await load({ background: true });
  } catch (error) {
    createError.value = describeError(error, "浏览器实例创建失败，请检查配置后重试。", {
      validation_failed: "名称需为 1–120 个字符。",
      not_found: "所选设备或指纹模板不属于当前工作空间，请刷新后重试。",
    });
  } finally {
    saving.value = false;
  }
};

onMounted(() => {
  void load();
});
onBeforeUnmount(() => {
  disposed = true;
  stopPolling();
});
</script>

<style scoped>
.notice-dismiss {
  width: 26px;
  height: 26px;
  margin: -4px -6px -4px 0;
  color: inherit;
}

.actions-cell :deep(.icon-button:disabled) {
  opacity: 0.45;
  cursor: not-allowed;
}
</style>
