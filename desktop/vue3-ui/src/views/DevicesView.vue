<template>
  <div class="page-stack">
    <PageHeader title="设备管理" description="你在各工作空间注册的桌面端代理及其凭据">
      <button v-if="registrableWorkspaces.length" class="button button-primary" type="button" @click="registerOpen = true"><Icon icon="lucide:plus" />注册设备</button>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>
    <div v-if="successMessage" class="alert alert-success" role="status"><Icon icon="lucide:circle-check" /><span>{{ successMessage }}</span></div>

    <article class="panel">
      <div class="toolbar">
        <label class="search-field"><Icon icon="lucide:search" /><input v-model.trim="query" type="search" placeholder="搜索设备名称或 ID…" aria-label="搜索设备" /></label>
        <select v-if="session.workspaces.length > 1" v-model="workspaceFilter" aria-label="筛选工作空间">
          <option value="">全部工作空间</option>
          <option v-for="workspace in session.workspaces" :key="workspace.id" :value="workspace.id">{{ workspace.name }}</option>
        </select>
        <span class="toolbar-spacer"></span>
        <span class="muted-text device-count">{{ activeCount }} 台有效 · 共 {{ devices.length }} 台</span>
        <button class="button button-quiet" type="button" :disabled="loading" @click="load"><Icon :class="{ spin: loading }" icon="lucide:refresh-cw" />刷新</button>
      </div>
      <div class="table-wrap">
        <table v-if="filtered.length" class="data-table">
          <thead><tr><th>设备</th><th>平台</th><th>状态</th><th>代理版本</th><th>最近在线</th><th>工作空间</th><th>凭据</th><th class="actions-cell">操作</th></tr></thead>
          <tbody>
            <tr v-for="item in filtered" :key="item.id" :class="{ 'is-revoked': !!item.revokedAt }">
              <td><div class="primary-cell"><span class="row-icon"><Icon :icon="devicePlatformIcon(item.platform)" /></span><div><strong>{{ item.name }}</strong><small class="mono" :title="item.id">{{ shortId(item.id) }}</small></div></div></td>
              <td>{{ devicePlatformLabel(item.platform) }}</td>
              <td><StatusBadge :status="item.status" :label="deviceStatusLabel(item.status)" /></td>
              <td><span :class="{ 'muted-text': !item.agentVersion }">{{ item.agentVersion || "—" }}</span></td>
              <td><span :class="{ 'muted-text': !item.lastSeenAt }" :title="item.lastSeenAt ? formatFullDateTime(item.lastSeenAt) : undefined">{{ item.lastSeenAt ? formatDateTime(item.lastSeenAt) : "从未上线" }}</span></td>
              <td>{{ workspaceName(item.workspaceId) }}</td>
              <td>
                <template v-if="item.revokedAt"><span class="text-danger">已吊销</span><small class="cell-subtitle">{{ formatDateTime(item.revokedAt) }}</small></template>
                <span v-else class="muted-text">有效</span>
              </td>
              <td class="actions-cell"><ActionMenu v-if="!item.revokedAt" :items="deviceActions" :label="`${item.name} 的操作`" @select="onAction(item, $event)" /></td>
            </tr>
          </tbody>
        </table>
        <EmptyState v-else-if="!loading && (devices.length || !errorMessage)" icon="lucide:hard-drive" :title="devices.length ? '没有匹配的设备' : '还没有注册设备'" :description="emptyDescription">
          <button v-if="!devices.length && registrableWorkspaces.length" class="button button-primary" type="button" @click="registerOpen = true">注册设备</button>
        </EmptyState>
        <div v-if="loading && !devices.length" class="panel-empty"><Icon class="spin" icon="lucide:loader-circle" /> 正在加载设备…</div>
      </div>
    </article>

    <RegisterDeviceDialog :open="registerOpen" :workspaces="registrableWorkspaces" :default-workspace-id="session.activeWorkspaceId" @close="registerOpen = false" @registered="onRegistered" />
    <DeviceCredentialDialog :open="!!issued" :title="issued?.title ?? ''" :registration="issued?.registration ?? null" :workspace-name="issued ? workspaceName(issued.registration.device.workspaceId) : ''" @close="issued = null" />
    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ActionMenu, { type ActionMenuItem } from "@/components/ActionMenu.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import DeviceCredentialDialog from "@/components/device/DeviceCredentialDialog.vue";
import RegisterDeviceDialog from "@/components/device/RegisterDeviceDialog.vue";
import { devicePlatformIcon, devicePlatformLabel, deviceStatusLabel } from "@/components/device/deviceLabels";
import { useConfirm } from "@/composables/useConfirm";
import { hasPermission } from "@/permissions";
import { useSessionStore } from "@/stores/session";
import type { AgentDevice, DeviceRegistration, Workspace } from "@/types";
import { copyToClipboard, formatDateTime, formatFullDateTime, shortId } from "@/utils/format";

type DeviceAction = "rotate" | "copy-id" | "revoke";

const ROTATION_UNSUPPORTED = "服务端尚不支持凭据轮换，请升级控制平面";

const session = useSessionStore();
const confirm = useConfirm();
const devices = ref<AgentDevice[]>([]);
const loading = ref(false);
const errorMessage = ref("");
const successMessage = ref("");
const query = ref("");
const workspaceFilter = ref("");
const registerOpen = ref(false);
/** The one-time credential being shown; cleared as soon as its dialog closes. */
const issued = ref<{ title: string; registration: DeviceRegistration } | null>(null);
let loadSequence = 0;

const deviceActions: ActionMenuItem[] = [
  { key: "rotate", label: "轮换凭据", icon: "lucide:refresh-ccw" },
  { key: "copy-id", label: "复制设备 ID", icon: "lucide:copy" },
  { key: "revoke", label: "吊销设备", icon: "lucide:ban", danger: true },
];

// Same rule as session.can(), applied to the target workspace instead of the active one.
const canRegisterIn = (workspace: Workspace) => !workspace.role || hasPermission(workspace.role, "instance.operate");
const registrableWorkspaces = computed(() => session.workspaces.filter(canRegisterIn));
const workspaceName = (id: string) => session.workspaces.find((item) => item.id === id)?.name ?? shortId(id);
const activeCount = computed(() => devices.value.filter((item) => !item.revokedAt).length);

const filtered = computed(() => {
  const needle = query.value.toLowerCase();
  return devices.value
    .filter((item) => (!needle || `${item.name} ${item.id}`.toLowerCase().includes(needle)) && (!workspaceFilter.value || item.workspaceId === workspaceFilter.value))
    // Stable sort: revoked devices move to the end, server order is kept otherwise.
    .sort((a, b) => Number(Boolean(a.revokedAt)) - Number(Boolean(b.revokedAt)));
});

const emptyDescription = computed(() => {
  if (devices.value.length) return "调整搜索条件或工作空间筛选。";
  if (!session.workspaces.length) return "加入或创建工作空间后即可注册设备。";
  if (!registrableWorkspaces.value.length) return "当前角色无权注册设备（需要运营及以上角色）。";
  return "注册后在桌面端代理中配置设备凭据，即可接入云端控制平面。";
});

/** Never throws, so post-action refreshes cannot mask a successful action. */
const load = async () => {
  const sequence = ++loadSequence;
  loading.value = true;
  errorMessage.value = "";
  try {
    const items = await api.list<AgentDevice>("/api/v1/devices");
    if (sequence === loadSequence) devices.value = items;
  } catch (error) {
    if (sequence === loadSequence) errorMessage.value = describeError(error, "设备列表加载失败");
  } finally {
    if (sequence === loadSequence) loading.value = false;
  }
};

const reveal = (title: string, registration: DeviceRegistration) => {
  issued.value = { title, registration };
};

const onRegistered = async (registration: DeviceRegistration) => {
  registerOpen.value = false;
  successMessage.value = `已注册设备「${registration.device.name}」`;
  reveal("设备已注册", registration);
  await load();
};

// A missing route answers with the mux's plain-text 404/405 (no JSON error code);
// a JSON `not_found` comes from the handler and means the device itself is gone.
const rotationUnsupported = (error: unknown) =>
  error instanceof ApiError && (error.status === 405 || (error.status === 404 && error.code !== "not_found"));

const rotate = (device: AgentDevice) => confirm.ask({
  title: "轮换设备凭据",
  message: `将为「${device.name}」生成新凭据，旧凭据立即失效。桌面端代理需改用新凭据后才能继续连接。`,
  confirmText: "轮换凭据",
}, async () => {
  let registration: DeviceRegistration;
  try {
    // Empty object: accepted whether or not the handler decodes a JSON body.
    registration = await api.post<DeviceRegistration>(`/api/v1/devices/${encodeURIComponent(device.id)}/rotate-credential`, {});
  } catch (error) {
    if (rotationUnsupported(error)) throw new Error(ROTATION_UNSUPPORTED);
    if (error instanceof ApiError && error.status === 404) void load();
    throw error;
  }
  successMessage.value = `已轮换「${device.name}」的凭据`;
  reveal("凭据已轮换", registration);
  await load();
});

const revoke = (device: AgentDevice) => confirm.ask({
  title: "吊销设备",
  message: `吊销后「${device.name}」的凭据立即失效，桌面端代理将无法再连接控制平面。此操作不可撤销。`,
  confirmText: "吊销设备",
  danger: true,
}, async () => {
  await api.delete(`/api/v1/devices/${encodeURIComponent(device.id)}`);
  successMessage.value = `已吊销设备「${device.name}」`;
  await load();
});

const onAction = async (device: AgentDevice, key: string) => {
  successMessage.value = "";
  switch (key as DeviceAction) {
    case "rotate":
      rotate(device);
      break;
    case "revoke":
      revoke(device);
      break;
    case "copy-id":
      if (await copyToClipboard(device.id)) successMessage.value = `已复制「${device.name}」的设备 ID`;
      else errorMessage.value = "无法访问剪贴板，请手动复制设备 ID";
      break;
  }
};

onMounted(() => void load());
</script>

<style scoped>
.device-count{font-size:12px}.data-table tr.is-revoked td{color:var(--muted)}
</style>
