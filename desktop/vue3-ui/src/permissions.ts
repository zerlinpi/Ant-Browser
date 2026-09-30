import type { WorkspaceRole } from "@/types";

export type Permission =
  | "workspace.read" | "workspace.update"
  | "member.read" | "member.invite" | "member.manage"
  | "instance.read" | "instance.create" | "instance.update" | "instance.operate" | "instance.delete"
  | "profile.read" | "profile.manage" | "profile.sync"
  | "fingerprint.read" | "fingerprint.manage"
  | "account.read" | "account.manage"
  | "proxy.read" | "proxy.manage"
  | "workflow.read" | "workflow.manage"
  | "task.operate" | "analytics.read"
  | "billing.read" | "billing.manage" | "audit.read";

const set = (...values: Permission[]) => new Set<Permission>(values);

// Mirrors rolePermissions in server/services/member-service/permissions.go.
// The server stays authoritative; this map only hides actions a role cannot use.
const rolePermissions: Record<WorkspaceRole, ReadonlySet<Permission>> = {
  owner: set(
    "workspace.read", "workspace.update", "member.read", "member.invite", "member.manage",
    "instance.read", "instance.create", "instance.update", "instance.operate", "instance.delete",
    "profile.read", "profile.manage", "profile.sync", "fingerprint.read", "fingerprint.manage",
    "account.read", "account.manage", "proxy.read", "proxy.manage",
    "workflow.read", "workflow.manage", "task.operate",
    "analytics.read", "billing.read", "billing.manage", "audit.read",
  ),
  admin: set(
    "workspace.read", "workspace.update", "member.read", "member.invite", "member.manage",
    "instance.read", "instance.create", "instance.update", "instance.operate", "instance.delete",
    "profile.read", "profile.manage", "profile.sync", "fingerprint.read", "fingerprint.manage",
    "account.read", "account.manage", "proxy.read", "proxy.manage",
    "workflow.read", "workflow.manage", "task.operate",
    "analytics.read", "billing.read", "audit.read",
  ),
  manager: set(
    "workspace.read", "member.read", "member.invite",
    "instance.read", "instance.create", "instance.update", "instance.operate",
    "profile.read", "profile.manage", "profile.sync", "fingerprint.read", "fingerprint.manage",
    "account.read", "account.manage", "proxy.read", "proxy.manage",
    "workflow.read", "workflow.manage", "task.operate",
    "analytics.read", "audit.read",
  ),
  operator: set(
    "workspace.read", "member.read",
    "instance.read", "instance.operate",
    "profile.read", "profile.sync", "fingerprint.read",
    "account.read", "proxy.read", "workflow.read", "task.operate",
  ),
  viewer: set(
    "workspace.read", "member.read", "instance.read",
    "profile.read", "fingerprint.read",
    "account.read", "proxy.read", "workflow.read", "analytics.read",
  ),
};

export const roleLabels: Record<WorkspaceRole, string> = {
  owner: "所有者",
  admin: "管理员",
  manager: "经理",
  operator: "运营",
  viewer: "只读",
};

/** Roles accepted by invitations and PATCH .../members/{userID}; owner cannot be granted. */
export const assignableRoles: WorkspaceRole[] = ["admin", "manager", "operator", "viewer"];

export const roleLabel = (role?: string) => (role ? roleLabels[role.toLowerCase() as WorkspaceRole] ?? role : "—");

export const hasPermission = (role: string, permission: Permission) =>
  rolePermissions[role.toLowerCase() as WorkspaceRole]?.has(permission) ?? false;
