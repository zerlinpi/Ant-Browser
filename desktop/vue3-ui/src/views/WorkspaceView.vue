<template>
  <div class="page-stack">
    <PageHeader title="团队空间" description="管理工作空间、成员角色和协作边界">
      <button class="button button-primary" type="button" @click="workspaceModal = true"><Icon icon="lucide:plus" />新建工作空间</button>
    </PageHeader>

    <section class="workspace-summary panel">
      <div><span class="workspace-avatar">{{ session.activeWorkspace?.name?.slice(0, 1) || "A" }}</span><div><h2>{{ session.activeWorkspace?.name || "尚未创建工作空间" }}</h2><p>{{ members.length }} 位成员 · 当前角色 {{ session.activeWorkspace?.role || "owner" }}</p></div></div>
      <button class="button button-secondary" type="button" @click="memberModal = true" :disabled="!session.activeWorkspaceId"><Icon icon="lucide:user-plus" />添加成员</button>
    </section>

    <article class="panel">
      <header class="panel-header"><div><h2>成员与权限</h2><p>角色权限按工作空间隔离，敏感操作会写入审计日志。</p></div></header>
      <div class="table-wrap">
        <table class="data-table">
          <thead><tr><th>成员</th><th>角色</th><th>状态</th><th>加入时间</th></tr></thead>
          <tbody><tr v-for="member in members" :key="String(member.id || member.userId)">
            <td><div class="identity-cell"><span>{{ initials(member) }}</span><div><strong>{{ value(member, 'displayName', 'email', 'userId') }}</strong><small>{{ value(member, 'email') }}</small></div></div></td>
            <td><span class="role-badge">{{ value(member, 'role', 'roleCode') || 'viewer' }}</span></td>
            <td><StatusBadge :status="value(member, 'status') || 'active'" /></td>
            <td>{{ formatDate(value(member, 'createdAt', 'joinedAt')) }}</td>
          </tr></tbody>
        </table>
        <EmptyState v-if="!members.length && !loading" icon="lucide:users" title="工作空间还没有成员" description="添加协作者并为其分配最小必要权限。" />
      </div>
    </article>

    <ModalDialog :open="workspaceModal" title="新建工作空间" description="工作空间会隔离实例、账号、代理和任务数据。" @close="workspaceModal = false">
      <label class="field"><span>空间名称</span><input v-model.trim="workspaceName" maxlength="80" placeholder="例如：北美运营组" /></label>
      <template #footer><button class="button button-secondary" @click="workspaceModal = false">取消</button><button class="button button-primary" :disabled="saving || !workspaceName" @click="createWorkspace">创建</button></template>
    </ModalDialog>

    <ModalDialog :open="memberModal" title="添加成员" description="输入已注册用户 ID 并分配工作空间角色。" @close="memberModal = false">
      <div class="form-grid"><label class="field field-span"><span>用户 ID</span><input v-model.trim="memberUserId" placeholder="UUID" /></label><label class="field field-span"><span>角色</span><select v-model="memberRole"><option value="admin">Admin</option><option value="manager">Manager</option><option value="operator">Operator</option><option value="viewer">Viewer</option></select></label></div>
      <template #footer><button class="button button-secondary" @click="memberModal = false">取消</button><button class="button button-primary" :disabled="saving || !memberUserId" @click="addMember">添加</button></template>
    </ModalDialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue";
import { Icon } from "@iconify/vue";
import { api } from "@/api/client";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useSessionStore } from "@/stores/session";

type Member = Record<string, unknown>;
const session = useSessionStore();
const members = ref<Member[]>([]);
const loading = ref(false);
const saving = ref(false);
const workspaceModal = ref(false);
const memberModal = ref(false);
const workspaceName = ref("");
const memberUserId = ref("");
const memberRole = ref("operator");
const value = (item: Member, ...keys: string[]) => keys.map((key) => item[key]).find((item) => typeof item === "string") as string | undefined;
const initials = (item: Member) => (value(item, "displayName", "email", "userId") || "U").slice(0, 1).toUpperCase();
const formatDate = (value?: string) => value ? new Intl.DateTimeFormat("zh-CN").format(new Date(value)) : "—";
const load = async () => {
  if (!session.workspaceBase) return;
  loading.value = true;
  members.value = await api.get<Member[]>(`${session.workspaceBase}/members`).catch(() => []);
  loading.value = false;
};
const createWorkspace = async () => {
  saving.value = true;
  try {
    const created = await api.post<{ id: string }>("/api/v1/workspaces", { name: workspaceName.value });
    await session.loadIdentity();
    session.selectWorkspace(created.id);
    workspaceModal.value = false;
    workspaceName.value = "";
    await load();
  } finally { saving.value = false; }
};
const addMember = async () => {
  saving.value = true;
  try {
    await api.post(`${session.workspaceBase}/members`, { userId: memberUserId.value, role: memberRole.value });
    memberModal.value = false;
    memberUserId.value = "";
    await load();
  } finally { saving.value = false; }
};
onMounted(load);
</script>
