<template>
  <div class="page-stack">
    <PageHeader title="团队空间" description="管理工作空间、成员角色和协作边界">
      <button class="button button-secondary" type="button" @click="openJoin"><Icon icon="lucide:log-in" />加入团队空间</button>
      <button class="button button-primary" type="button" @click="openCreate"><Icon icon="lucide:plus" />新建工作空间</button>
    </PageHeader>

    <div v-if="errorMessage" class="alert alert-danger" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ errorMessage }}</span></div>
    <div v-if="successMessage" class="alert alert-success" role="status"><Icon icon="lucide:circle-check" /><span>{{ successMessage }}</span></div>

    <template v-if="workspace">
      <section class="workspace-summary panel">
        <div>
          <span class="workspace-avatar" aria-hidden="true">{{ initial(workspace.name) }}</span>
          <div>
            <h2>{{ workspace.name }}</h2>
            <p>{{ members.length }} 位成员 · 当前角色：{{ roleLabel(workspace.role) }}</p>
          </div>
        </div>
        <div class="summary-actions">
          <button v-if="session.can('workspace.update')" class="button button-secondary" type="button" @click="openRename"><Icon icon="lucide:pencil" />重命名</button>
          <button v-if="session.can('member.invite')" class="button button-secondary" type="button" @click="openInvite"><Icon icon="lucide:user-plus" />邀请成员</button>
        </div>
      </section>

      <article class="panel">
        <header class="panel-header"><div><h2>成员与权限</h2><p>角色权限按工作空间隔离，敏感操作会写入审计日志。</p></div></header>
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th>成员</th><th>角色</th><th>状态</th><th>加入时间</th><th class="actions-cell">操作</th></tr></thead>
            <tbody>
              <tr v-for="member in members" :key="member.id">
                <td>
                  <div class="identity-cell" :title="member.userId">
                    <span aria-hidden="true">{{ initial(memberName(member)) }}</span>
                    <div>
                      <strong>{{ memberName(member) }}<span v-if="isSelf(member)" class="self-marker">（我）</span></strong>
                      <small v-if="memberDetail(member)">{{ memberDetail(member) }}</small>
                    </div>
                  </div>
                </td>
                <td><span class="role-badge">{{ roleLabel(member.role) }}</span></td>
                <td><StatusBadge :status="member.status" :label="memberStatusLabels[member.status]" /></td>
                <td>{{ formatDateTime(member.joinedAt) }}</td>
                <td class="actions-cell">
                  <ActionMenu v-if="showMemberMenu(member)" :items="memberActions(member)" :label="`${memberName(member)}的操作`" @select="onMemberAction(member, $event)" />
                </td>
              </tr>
            </tbody>
          </table>
          <EmptyState v-if="!members.length && !loading" icon="lucide:users" title="工作空间还没有成员" description="添加协作者并为其分配最小必要权限。" />
        </div>
      </article>

      <article class="panel">
        <header class="panel-header"><div><h2>待处理邀请</h2><p>邀请码仅在创建时显示一次，列表不会再次返回。</p></div></header>
        <div class="table-wrap">
          <table class="data-table">
            <thead><tr><th>邮箱</th><th>角色</th><th>状态</th><th>有效期至</th><th class="actions-cell">操作</th></tr></thead>
            <tbody>
              <tr v-for="invitation in invitations" :key="invitation.id">
                <td>{{ invitation.email }}</td>
                <td><span class="role-badge">{{ roleLabel(invitation.role) }}</span></td>
                <td><StatusBadge v-bind="invitationBadge(invitation)" /></td>
                <td>{{ formatDateTime(invitation.expiresAt) }}</td>
                <td class="actions-cell">
                  <button v-if="invitation.status === 'pending' && session.can('member.invite')" class="button button-quiet small" type="button" @click="confirmRevoke(invitation)">撤销</button>
                </td>
              </tr>
            </tbody>
          </table>
          <EmptyState v-if="!invitations.length && !loading" icon="lucide:mail-check" title="没有待处理邀请" description="邀请成员后，对方使用邀请码加入当前工作空间。" />
        </div>
      </article>
    </template>

    <article v-else class="panel">
      <EmptyState icon="lucide:building-2" title="还没有工作空间" description="新建工作空间，或点击「加入团队空间」粘贴管理员发送的邀请码。" />
    </article>

    <ModalDialog :open="createOpen" title="新建工作空间" description="工作空间会隔离实例、账号、代理和任务数据。" @close="closeCreate">
      <form id="workspace-create-form" @submit.prevent="createWorkspace">
        <label class="field"><span>空间名称</span><input v-model="createName" :maxlength="WORKSPACE_NAME_MAX" placeholder="例如：北美运营组" required /></label>
      </form>
      <div v-if="createError" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ createError }}</span></div>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="creating" @click="closeCreate">取消</button>
        <button class="button button-primary" type="submit" form="workspace-create-form" :disabled="creating || !createName.trim()">{{ creating ? "创建中…" : "创建" }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="joinOpen" title="加入团队空间" description="粘贴管理员发送的邀请码，邀请码仅限受邀邮箱对应的账号使用。" @close="closeJoin">
      <form id="workspace-join-form" @submit.prevent="joinWorkspace">
        <label class="field">
          <span>邀请码</span>
          <input v-model="joinCode" class="mono" autocomplete="off" spellcheck="false" placeholder="空间 ID.令牌" required />
          <small v-if="session.user?.email">当前账号：{{ session.user.email }}</small>
        </label>
      </form>
      <div v-if="joinError" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ joinError }}</span></div>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="joining" @click="closeJoin">取消</button>
        <button class="button button-primary" type="submit" form="workspace-join-form" :disabled="joining || !joinCode.trim()">{{ joining ? "加入中…" : "加入" }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="renameOpen" title="重命名工作空间" @close="closeRename">
      <form id="workspace-rename-form" @submit.prevent="renameWorkspace">
        <label class="field"><span>空间名称</span><input v-model="renameName" :maxlength="WORKSPACE_NAME_MAX" required /></label>
      </form>
      <div v-if="renameError" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ renameError }}</span></div>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="renaming" @click="closeRename">取消</button>
        <button class="button button-primary" type="submit" form="workspace-rename-form" :disabled="renaming || !renameName.trim() || renameName.trim() === workspace?.name">{{ renaming ? "保存中…" : "保存" }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="inviteOpen" title="邀请成员" description="系统生成一次性邀请码，仅限该邮箱对应的账号接受。" @close="closeInvite">
      <form id="member-invite-form" class="form-grid" @submit.prevent="inviteMember">
        <label class="field field-span"><span>邮箱</span><input v-model.trim="inviteEmail" type="email" autocomplete="off" placeholder="operator@example.com" required /></label>
        <label class="field field-span">
          <span>角色</span>
          <select v-model="inviteRole"><option v-for="role in assignableRoles" :key="role" :value="role">{{ roleLabels[role] }}</option></select>
          <small>{{ roleScopes[inviteRole] }}</small>
        </label>
      </form>
      <div v-if="inviteError" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ inviteError }}</span></div>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="inviting" @click="closeInvite">取消</button>
        <button class="button button-primary" type="submit" form="member-invite-form" :disabled="inviting || !inviteEmail">{{ inviting ? "创建中…" : "创建邀请" }}</button>
      </template>
    </ModalDialog>

    <ModalDialog :open="Boolean(issued)" title="邀请码已生成" :description="issued ? `${issued.email} · ${roleLabel(issued.role)}` : ''" @close="closeIssued">
      <template v-if="issued">
        <div class="alert alert-warning" role="alert"><Icon icon="lucide:triangle-alert" /><span>邀请码只显示这一次，关闭后无法再次查看，请通过安全渠道发送。</span></div>
        <div class="secret-list invite-code">
          <div>
            <span>邀请码</span>
            <div class="secret-row">
              <code class="code-block invite-code-value">{{ issued.code }}</code>
              <button class="button button-secondary small" type="button" @click="copyInviteCode">
                <Icon :icon="codeCopied ? 'lucide:check' : 'lucide:copy'" />{{ codeCopied ? "已复制" : "复制" }}
              </button>
            </div>
          </div>
        </div>
        <p class="form-note"><Icon icon="lucide:info" /><span>对方使用 {{ issued.email }} 登录后，在「团队空间 → 加入团队空间」中粘贴邀请码。有效期至 {{ formatDateTime(issued.expiresAt) }}。</span></p>
        <div v-if="copyError" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ copyError }}</span></div>
      </template>
      <template #footer><button class="button button-primary" type="button" @click="closeIssued">完成</button></template>
    </ModalDialog>

    <ModalDialog :open="Boolean(roleTarget)" title="修改角色" :description="roleTarget ? `${memberName(roleTarget)} · 当前为${roleLabel(roleTarget.role)}` : ''" @close="closeRole">
      <form id="member-role-form" @submit.prevent="changeRole">
        <label class="field">
          <span>新角色</span>
          <select v-model="roleDraft"><option v-for="role in assignableRoles" :key="role" :value="role">{{ roleLabels[role] }}</option></select>
          <small>{{ roleScopes[roleDraft] }}</small>
        </label>
      </form>
      <p v-if="roleTarget && isSelf(roleTarget)" class="form-note"><Icon icon="lucide:info" /><span>修改自己的角色会立即改变你的权限。</span></p>
      <p v-else-if="roleTarget && isOwner(roleTarget)" class="form-note"><Icon icon="lucide:info" /><span>保存后该成员将不再是所有者。</span></p>
      <div v-if="roleError" class="alert alert-danger modal-alert" role="alert"><Icon icon="lucide:circle-alert" /><span>{{ roleError }}</span></div>
      <template #footer>
        <button class="button button-secondary" type="button" :disabled="changingRole" @click="closeRole">取消</button>
        <button class="button button-primary" type="submit" form="member-role-form" :disabled="changingRole || roleDraft === roleTarget?.role">{{ changingRole ? "保存中…" : "保存" }}</button>
      </template>
    </ModalDialog>

    <ConfirmDialog v-bind="confirm.state" @confirm="confirm.confirm" @cancel="confirm.cancel" />
  </div>
</template>

<script lang="ts">
// AppShell keys the routed view by workspace id, so switching the active
// workspace remounts this view; the finishing action reports through the
// instance that is mounted at that point.
let liveNotice: ((message: string) => void) | undefined;
</script>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from "vue";
import { Icon } from "@iconify/vue";
import { api, ApiError, describeError } from "@/api/client";
import ActionMenu, { type ActionMenuItem } from "@/components/ActionMenu.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import EmptyState from "@/components/EmptyState.vue";
import ModalDialog from "@/components/ModalDialog.vue";
import PageHeader from "@/components/PageHeader.vue";
import StatusBadge from "@/components/StatusBadge.vue";
import { useConfirm } from "@/composables/useConfirm";
import { assignableRoles, roleLabel, roleLabels } from "@/permissions";
import { useSessionStore } from "@/stores/session";
import type { Workspace, WorkspaceInvitation, WorkspaceMembership, WorkspaceRole } from "@/types";
import { copyToClipboard, formatDateTime, shortId } from "@/utils/format";

/** workspace-service rejects blank names and names longer than 120 characters. */
const WORKSPACE_NAME_MAX = 120;
const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
/** Invitation tokens are 32 random bytes encoded as unpadded base64url. */
const TOKEN_PATTERN = /^[A-Za-z0-9_-]{43}$/;

// Summaries of rolePermissions in permissions.ts.
const roleScopes: Record<WorkspaceRole, string> = {
  owner: "全部权限，含计费管理",
  admin: "除计费管理外的全部权限",
  manager: "管理实例、档案、账号、代理与自动化，可邀请成员",
  operator: "启停实例、同步档案、执行任务，其余只读",
  viewer: "只读查看资源与运营分析",
};
const memberStatusLabels: Record<string, string> = { active: "正常" };
const invitationStatusLabels: Record<string, string> = { pending: "待接受", accepted: "已接受", revoked: "已撤销" };

const createErrors = { validation_failed: "名称无效或已被占用，请更换名称" };
const renameErrors = {
  validation_failed: "名称无效，或与同组织其他空间重复",
  version_conflict: "空间信息已被他人更新，已同步最新版本，请重试",
};
const inviteErrors = { invitation_invalid: "邮箱格式无效，或该角色不可邀请" };
const joinErrors = {
  invitation_invalid: "邀请码无效、已过期、已撤销、已被使用，或与当前登录邮箱不匹配",
  resource_conflict: "当前账号已是该团队空间成员，或此前已被移除",
  quota_exceeded: "该团队空间的成员席位已满，请联系管理员",
};
const memberErrors = { forbidden: "权限不足：需要成员管理权限，调整所有者需由所有者操作" };

const session = useSessionStore();
const confirm = useConfirm();
const workspace = computed(() => session.activeWorkspace);
const members = ref<WorkspaceMembership[]>([]);
const invitations = ref<WorkspaceInvitation[]>([]);
const loading = ref(false);
const errorMessage = ref("");
const successMessage = ref("");

const showNotice = (message: string) => {
  successMessage.value = message;
};
const resetMessages = () => {
  errorMessage.value = "";
  successMessage.value = "";
};

/** Reports once AppShell has remounted the view for the newly active workspace. */
const noticeAfterSwitch = async (message: string) => {
  await nextTick();
  (liveNotice ?? showNotice)(message);
};

/** Re-reads workspace names, versions and roles; failures are reported instead of thrown. */
const refreshIdentity = async () => {
  try {
    await session.loadIdentity();
    return true;
  } catch (error) {
    errorMessage.value = describeError(error, "空间列表刷新失败，请刷新页面");
    return false;
  }
};

const load = async () => {
  const base = session.workspaceBase;
  if (!base) return;
  loading.value = true;
  const [memberResult, invitationResult] = await Promise.allSettled([
    api.list<WorkspaceMembership>(`${base}/members`),
    api.list<WorkspaceInvitation>(`${base}/invitations`),
  ]);
  if (memberResult.status === "fulfilled") members.value = memberResult.value;
  if (invitationResult.status === "fulfilled") invitations.value = invitationResult.value;
  const failure = [memberResult, invitationResult].find((result): result is PromiseRejectedResult => result.status === "rejected");
  if (failure) errorMessage.value = describeError(failure.reason, "团队空间加载失败，请稍后重试");
  loading.value = false;
};

// ---- Members ----

const initial = (value: string) => Array.from(value.trim())[0]?.toUpperCase() ?? "?";
const isSelf = (member: WorkspaceMembership) => Boolean(session.user?.id) && member.userId === session.user?.id;
const isOwner = (member: WorkspaceMembership) => member.role.toLowerCase() === "owner";
const ownerCount = computed(() => members.value.filter(isOwner).length);
const memberName = (member: WorkspaceMembership) => member.displayName?.trim() || member.email?.trim() || shortId(member.userId);
/** The email is shown under the display name; it is already the primary text otherwise. */
const memberDetail = (member: WorkspaceMembership) => (member.displayName?.trim() ? member.email?.trim() ?? "" : "");

const showMemberMenu = (member: WorkspaceMembership) =>
  session.can("member.manage") && !(isSelf(member) && isOwner(member) && ownerCount.value <= 1);

const memberActions = (member: WorkspaceMembership): ActionMenuItem[] => {
  const callerRole = workspace.value?.role?.toLowerCase();
  // The server answers 403 when a non-owner changes an owner and 409 last_owner for the sole owner.
  let hint: string | undefined;
  if (isOwner(member) && callerRole && callerRole !== "owner") hint = "仅所有者可调整所有者";
  else if (isOwner(member) && ownerCount.value <= 1) hint = "至少保留一位所有者";
  const disabled = Boolean(hint);
  return [
    { key: "role", label: "修改角色", icon: "lucide:user-cog", disabled, hint },
    { key: "remove", label: isSelf(member) ? "退出空间" : "移除成员", icon: "lucide:user-minus", danger: true, disabled, hint },
  ];
};

const onMemberAction = (member: WorkspaceMembership, key: string) => {
  if (key === "role") openRole(member);
  else if (key === "remove") confirmRemove(member);
};

const roleTarget = ref<WorkspaceMembership | null>(null);
const roleDraft = ref<WorkspaceRole>("operator");
const roleError = ref("");
const changingRole = ref(false);

const openRole = (member: WorkspaceMembership) => {
  const current = member.role.toLowerCase() as WorkspaceRole;
  roleDraft.value = assignableRoles.includes(current) ? current : "admin";
  roleError.value = "";
  roleTarget.value = member;
};
const closeRole = () => {
  if (!changingRole.value) roleTarget.value = null;
};

const changeRole = async () => {
  const target = roleTarget.value;
  const role = roleDraft.value;
  const base = session.workspaceBase;
  if (!target || !base || changingRole.value || role === target.role) return;
  changingRole.value = true;
  roleError.value = "";
  resetMessages();
  try {
    await api.patch<WorkspaceMembership>(`${base}/members/${target.userId}`, { role });
    roleTarget.value = null;
  } catch (error) {
    roleError.value = describeError(error, "角色修改失败，请稍后重试", memberErrors);
    return;
  } finally {
    changingRole.value = false;
  }
  // The caller's own permissions follow their role.
  if (isSelf(target)) await refreshIdentity();
  await load();
  showNotice(`已将 ${memberName(target)} 的角色改为${roleLabel(role)}`);
};

const confirmRemove = (member: WorkspaceMembership) => {
  const base = session.workspaceBase;
  if (!base) return;
  const self = isSelf(member);
  const name = memberName(member);
  const space = workspace.value?.name ?? "当前工作空间";
  resetMessages();
  confirm.ask({
    title: self ? "退出团队空间" : "移除成员",
    message: self
      ? `你将退出「${space}」并失去访问权限。\n你在此空间注册的设备会被同时吊销。`
      : `${name} 将无法再访问「${space}」。\n其在此空间注册的设备会被同时吊销。`,
    confirmText: self ? "退出" : "移除",
    danger: true,
  }, async () => {
    try {
      await api.delete(`${base}/members/${member.userId}`);
    } catch (error) {
      throw new Error(describeError(error, "移除失败，请稍后重试", memberErrors));
    }
    if (self) {
      // The workspace leaves the caller's list, so the session falls back to another one.
      if (await refreshIdentity()) await noticeAfterSwitch(`已退出「${space}」`);
      return;
    }
    await load();
    showNotice(`已移除 ${name}`);
  });
};

// ---- Invitations ----

const isExpired = (invitation: WorkspaceInvitation) => {
  const expiresAt = Date.parse(invitation.expiresAt);
  return invitation.status === "pending" && Number.isFinite(expiresAt) && expiresAt <= Date.now();
};
const invitationBadge = (invitation: WorkspaceInvitation) => isExpired(invitation)
  ? { status: "expired", label: "已过期" }
  : { status: invitation.status, label: invitationStatusLabels[invitation.status] ?? invitation.status };

const inviteOpen = ref(false);
const inviteEmail = ref("");
const inviteRole = ref<WorkspaceRole>("operator");
const inviteError = ref("");
const inviting = ref(false);
/** The one-time invite code; dropped from memory when the dialog closes. */
const issued = ref<{ code: string; email: string; role: string; expiresAt: string } | null>(null);
const codeCopied = ref(false);
const copyError = ref("");

const openInvite = () => {
  inviteEmail.value = "";
  inviteRole.value = "operator";
  inviteError.value = "";
  inviteOpen.value = true;
};
const closeInvite = () => {
  if (!inviting.value) inviteOpen.value = false;
};

const inviteMember = async () => {
  const base = session.workspaceBase;
  const workspaceId = session.activeWorkspaceId;
  const email = inviteEmail.value.trim();
  if (!base || !email || inviting.value) return;
  inviting.value = true;
  inviteError.value = "";
  resetMessages();
  try {
    const invitation = await api.post<WorkspaceInvitation>(`${base}/invitations`, { email, role: inviteRole.value });
    inviteOpen.value = false;
    if (invitation.token) {
      codeCopied.value = false;
      copyError.value = "";
      issued.value = {
        code: `${invitation.workspaceId || workspaceId}.${invitation.token}`,
        email: invitation.email,
        role: invitation.role,
        expiresAt: invitation.expiresAt,
      };
    }
    showNotice(`已创建发给 ${invitation.email} 的邀请`);
  } catch (error) {
    inviteError.value = describeError(error, "邀请创建失败，请稍后重试", inviteErrors);
    return;
  } finally {
    inviting.value = false;
  }
  await load();
};

const copyInviteCode = async () => {
  if (!issued.value) return;
  copyError.value = "";
  codeCopied.value = await copyToClipboard(issued.value.code);
  if (!codeCopied.value) copyError.value = "无法访问剪贴板，请手动选中并复制";
};
const closeIssued = () => {
  issued.value = null;
};

const confirmRevoke = (invitation: WorkspaceInvitation) => {
  const base = session.workspaceBase;
  if (!base) return;
  resetMessages();
  confirm.ask({
    title: "撤销邀请",
    message: `撤销后，发给 ${invitation.email} 的邀请码将立即失效。`,
    confirmText: "撤销",
    danger: true,
  }, async () => {
    await api.delete(`${base}/invitations/${invitation.id}`);
    await load();
    showNotice(`已撤销发给 ${invitation.email} 的邀请`);
  });
};

// ---- Join with an invite code ----

const joinOpen = ref(false);
const joinCode = ref("");
const joinError = ref("");
const joining = ref(false);

/** Invite codes are `<workspaceId>.<token>`; whitespace from chat or mail clients is ignored. */
const parseInviteCode = (value: string) => {
  const parts = value.replace(/\s+/g, "").split(".");
  if (parts.length !== 2) return null;
  const [workspaceId, token] = parts;
  if (!UUID_PATTERN.test(workspaceId) || !TOKEN_PATTERN.test(token)) return null;
  return { workspaceId: workspaceId.toLowerCase(), token };
};

const openJoin = () => {
  joinCode.value = "";
  joinError.value = "";
  joinOpen.value = true;
};
const closeJoin = () => {
  if (joining.value) return;
  joinOpen.value = false;
  joinCode.value = "";
};

const joinWorkspace = async () => {
  if (joining.value) return;
  const code = parseInviteCode(joinCode.value);
  if (!code) {
    joinError.value = "邀请码格式不正确，应为「空间 ID.令牌」";
    return;
  }
  if (session.workspaces.some((item) => item.id === code.workspaceId)) {
    joinError.value = "当前账号已是该团队空间成员";
    return;
  }
  joining.value = true;
  joinError.value = "";
  resetMessages();
  let joined = false;
  try {
    await api.post<WorkspaceMembership>(`/api/v1/workspaces/${code.workspaceId}/invitations/accept`, { token: code.token });
    joined = true;
    await session.loadIdentity();
    joinOpen.value = false;
    joinCode.value = "";
    session.selectWorkspace(code.workspaceId);
    const name = session.workspaces.find((item) => item.id === code.workspaceId)?.name;
    await noticeAfterSwitch(name ? `已加入「${name}」` : "已加入团队空间");
  } catch (error) {
    if (joined) {
      joinOpen.value = false;
      joinCode.value = "";
      errorMessage.value = `已加入团队空间，但空间列表刷新失败：${describeError(error, "请刷新页面")}`;
    } else {
      joinError.value = describeError(error, "加入失败，请稍后重试", joinErrors);
    }
  } finally {
    joining.value = false;
  }
};

// ---- Workspace create and rename ----

const createOpen = ref(false);
const createName = ref("");
const createError = ref("");
const creating = ref(false);

const openCreate = () => {
  createName.value = "";
  createError.value = "";
  createOpen.value = true;
};
const closeCreate = () => {
  if (!creating.value) createOpen.value = false;
};

const createWorkspace = async () => {
  const name = createName.value.trim();
  if (!name || creating.value) return;
  creating.value = true;
  createError.value = "";
  resetMessages();
  let created: Workspace | undefined;
  try {
    created = await api.post<Workspace>("/api/v1/workspaces", { name });
    await session.loadIdentity();
    createOpen.value = false;
    session.selectWorkspace(created.id);
    await noticeAfterSwitch(`已创建「${created.name}」`);
  } catch (error) {
    if (created) {
      createOpen.value = false;
      errorMessage.value = `工作空间已创建，但空间列表刷新失败：${describeError(error, "请刷新页面")}`;
    } else {
      createError.value = describeError(error, "工作空间创建失败，请稍后重试", createErrors);
    }
  } finally {
    creating.value = false;
  }
};

const renameOpen = ref(false);
const renameName = ref("");
const renameError = ref("");
const renaming = ref(false);

const openRename = () => {
  renameName.value = workspace.value?.name ?? "";
  renameError.value = "";
  renameOpen.value = true;
};
const closeRename = () => {
  if (!renaming.value) renameOpen.value = false;
};

const renameWorkspace = async () => {
  const current = workspace.value;
  const name = renameName.value.trim();
  if (!current || !name || name === current.name || renaming.value) return;
  renaming.value = true;
  renameError.value = "";
  resetMessages();
  let saved = false;
  try {
    await api.patch<Workspace>(`/api/v1/workspaces/${current.id}`, { name, version: current.version });
    saved = true;
    await session.loadIdentity();
    renameOpen.value = false;
    showNotice(`空间已重命名为「${name}」`);
  } catch (error) {
    if (saved) {
      renameOpen.value = false;
      errorMessage.value = `名称已更新，但空间列表刷新失败：${describeError(error, "请刷新页面")}`;
    } else {
      // Someone saved first: resync so a retry carries the current version.
      if (error instanceof ApiError && error.code === "version_conflict") await session.loadIdentity().catch(() => undefined);
      renameError.value = describeError(error, "重命名失败，请稍后重试", renameErrors);
    }
  } finally {
    renaming.value = false;
  }
};

onMounted(() => {
  liveNotice = showNotice;
  void load();
});
onBeforeUnmount(() => {
  if (liveNotice === showNotice) liveNotice = undefined;
});
</script>

<style scoped>
.summary-actions {
  display: flex;
  gap: 8px;
}

.self-marker {
  margin-left: 2px;
  color: var(--muted);
  font-weight: 500;
}

.modal-alert {
  margin-top: 12px;
}

.invite-code {
  margin-top: 16px;
}

.invite-code-value {
  user-select: all;
}
</style>
